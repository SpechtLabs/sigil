// A kind defined in TypeScript, the twin of Go's policy.NewKind: the
// inputs a policy reads, the decisions it may construct, the host
// functions it may call. schema() writes it as a kind file, byte for byte
// what Go's Kind.Schema writes for the same kind, so a repository can
// check the file in and the CLI can type-check policies against it.

import { Decision, Outcome, type PayloadSpec, type ResultShape } from "./decision.js";
import { SigilError } from "./errors.js";
import { type AnyType, Defaulted, EnumType, type Fields, type FieldsIn, type In, OptionalType, type Out, StructType } from "./schema.js";
import { type Policy, setResultShape, Sigil } from "./sigil.js";
import type { CompileOptions, Diagnostic, HostFunction, SourceFile } from "./types.js";
import type { SigilWorker, WorkerCompileOptions, WorkerPolicy } from "./worker.js";

/** A host function's declaration: its parameter and result types, and optionally its implementation. */
export class Fn<P extends readonly AnyType[] = readonly AnyType[], R extends AnyType = AnyType> {
  constructor(
    readonly params: P,
    readonly result: R,
    readonly impl: HostFunction | undefined,
  ) {}
}

/** The arguments a host function receives: each parameter as the module hands it over. */
export type ArgsOf<P extends readonly AnyType[]> = { -readonly [I in keyof P]: Out<P[I]> };

/**
 * Declares a host function, `fn split(string, string) -> list<string>` in
 * a kind file. The implementation runs synchronously inside evaluations
 * of policies the kind compiles; leave it out to only declare the
 * signature, for stubs or another host.
 *
 *     split: fn([t.string, t.string], t.list(t.string), (s, sep) => s.split(sep))
 */
export function fn<const P extends readonly AnyType[], R extends AnyType>(
  params: P,
  result: R,
  impl?: (...args: ArgsOf<P>) => NoInfer<In<R>>,
): Fn<P, R> {
  return new Fn(params, result, impl as HostFunction | undefined);
}

/** Anything `exclusive` can name: a whole decision, or one of its reasons. */
export type OutcomeRef = Decision<string, string, PayloadSpec> | Outcome;

// Any decision, whatever its reasons and payload.
type AnyDecision = Decision<string, string, PayloadSpec>;

/** What {@link defineKind} takes. */
export interface KindSpec {
  /** The contract version, from 1; every change bumps it. */
  version: number;
  /** The oldest version a policy may pin with `Kind@N`; every version when left out. */
  accepts?: number;
  /** The inputs, in declaration order. */
  inputs: Fields;
  /**
   * Enums to declare even though no input, function or payload uses
   * them, like Go's WithEnum for an enum nothing reaches. They print after
   * the ones something uses.
   */
  enums?: readonly EnumType<string, string>[];
  /** The host functions, in declaration order. */
  functions?: Record<string, Fn>;
  /**
   * The decisions of a `collect one` kind, highest precedence first: the
   * outcome is the one candidate of the highest decision that fired, or
   * the default.
   */
  decisions?: readonly AnyDecision[];
  /**
   * The decisions of a `collect all` kind, where every decision that fired
   * applies; read results with matchAll.
   */
  collect?: readonly AnyDecision[];
  /** Ranks a collecting kind's decisions, which makes the outcome the top rank. Lists every decision. */
  precedence?: readonly AnyDecision[];
  /**
   * Ranks the reasons of decisions, one ranking per decision: a decision
   * ranks its reasons in declared order, or a list of one decision's
   * reasons ranks them in that order.
   */
  reasonPrecedence?: readonly (AnyDecision | readonly Outcome[])[];
  /** Sets of outcomes that can't fire together; each names at least two. */
  exclusive?: readonly (readonly OutcomeRef[])[];
  /** The outcome when no rule fires; required for a `collect one` kind. */
  default?: Outcome;
  /** The outcome of a conflict, instead of the default; `collect one` only. */
  conflict?: Outcome;
}

/** What a host passes to evaluate a policy of the kind. */
export type InputOf<K> = K extends Kind<infer S> ? FieldsIn<S["inputs"]> : never;

/** The options of {@link Kind.compile}: {@link CompileOptions}, with the kind's host functions filled in. */
export interface KindCompileOptions extends Omit<CompileOptions, "functions"> {
  /** Host functions to use instead of, or in addition to, the kind's implementations. */
  functions?: Record<string, HostFunction>;
  /** The path of the kind file added to the files when they don't hold it; `<snake_case name>.sigil` by default. */
  kindFile?: string;
}

/** The options of {@link Kind.compile} with the worker helper: host functions come from its functions module. */
export interface KindWorkerCompileOptions extends WorkerCompileOptions {
  kindFile?: string;
}

/**
 * A kind: the contract policies are checked against. Build one with
 * {@link defineKind}, once, at module level.
 */
export class Kind<S extends KindSpec = KindSpec> {
  readonly #source: string;
  readonly #shape: ResultShape;

  /** @internal Kinds come from {@link defineKind}. */
  constructor(
    readonly name: string,
    readonly spec: S,
  ) {
    const problems: string[] = [];
    this.#source = render(name, spec, problems);
    this.#shape = { collect: spec.collect !== undefined, ranked: (spec.precedence?.length ?? 0) > 0 };
    if (problems.length > 0) {
      throw new SigilError(`defineKind(${name}): invalid kind:\n${problems.map((p) => `  ${p}`).join("\n")}`, {
        help: "fix every problem listed; the kind can't be exported until then",
      });
    }
  }

  /** The kind version. */
  get version(): number {
    return this.spec.version;
  }

  /**
   * The kind as a kind file, in `sigil fmt`'s canonical style: byte for
   * byte what Go's Kind.Schema writes for the same kind. Check it in (see
   * {@link file}) so the CLI and other hosts can type-check policies.
   */
  schema(): string {
    return this.#source;
  }

  /** The kind file as a virtual file: `alert_routing.sigil` for AlertRouting, unless path says otherwise. */
  file(path = `${snakeCase(this.name)}.sigil`): SourceFile {
    return { path, source: this.#source };
  }

  /** The implementations of the kind's host functions. */
  functions(): Record<string, HostFunction> {
    const out: Record<string, HostFunction> = {};
    for (const [name, f] of Object.entries(this.spec.functions ?? {})) if (f.impl !== undefined) out[name] = f.impl;
    return out;
  }

  /**
   * Checks the kind file with the real engine: every rule of the model
   * that the builder leaves to it, such as reserved words or a cycle of
   * struct types. Returns the diagnostics; empty means the kind is sound.
   */
  check(sigil: Sigil): Diagnostic[] {
    return sigil.check([this.file()]);
  }

  /**
   * Compiles one policy of the files against this kind. The files, or the
   * trusted files, may hold the kind file; if neither does, it's added to
   * the files, and if one holds a kind file that differs from
   * {@link schema}, the compile fails, which catches a stale export. The
   * kind's host function implementations are passed along.
   */
  compile(sigil: Sigil, files: SourceFile[], options?: KindCompileOptions): Policy<InputOf<this>>;
  compile(sigil: SigilWorker, files: SourceFile[], options?: KindWorkerCompileOptions): Promise<WorkerPolicy<InputOf<this>>>;
  compile(
    sigil: Sigil | SigilWorker,
    files: SourceFile[],
    options: KindCompileOptions | KindWorkerCompileOptions = {},
  ): Policy<InputOf<this>> | Promise<WorkerPolicy<InputOf<this>>> {
    const { kindFile, ...rest } = options;
    const all = this.#withKindFile(files, rest.trustedFiles ?? [], kindFile);
    if (sigil instanceof Sigil) {
      const o = rest as Omit<KindCompileOptions, "kindFile">;
      const policy = sigil.compile(all, { ...o, functions: { ...this.functions(), ...o.functions } }) as Policy<InputOf<this>>;
      setResultShape(policy, this.#shape);
      return policy;
    }
    return sigil.compile(all, rest as WorkerCompileOptions).then((policy) => {
      policy.shape = this.#shape;
      return policy as unknown as WorkerPolicy<InputOf<this>>;
    });
  }

  #withKindFile(files: SourceFile[], trusted: SourceFile[], path: string | undefined): SourceFile[] {
    const own = [...files, ...trusted].filter((f) => kindNameOf(f.source) === this.name);
    for (const f of own) {
      if (f.source !== this.#source) {
        throw new SigilError(`${f.path} declares kind ${this.name}, but not as this program defines it`, {
          help: "the file is stale: export the kind again, or drop the file and let compile add the current one",
        });
      }
    }
    return own.length > 0 ? files : [this.file(path), ...files];
  }
}

/**
 * Defines a kind, the TypeScript twin of Go's policy.NewKind. Throws a
 * {@link SigilError} listing every problem when the kind can't be
 * exported, so a bad kind fails when the module loads.
 *
 *     export const AlertRouting = defineKind("AlertRouting", {
 *       version: 1,
 *       inputs: { alert: Alert, team: Team },
 *       decisions: [Page, Drop, Notify],
 *       reasonPrecedence: [Page, Drop, Notify],
 *       default: Notify.reason("unrouted"),
 *     });
 */
export function defineKind<const S extends KindSpec>(name: string, spec: S): Kind<S> {
  return new Kind(name, spec);
}

const IDENT = /^[A-Za-z_][A-Za-z0-9_]*$/;

/**
 * Renders the kind file, walking the kind in the order Go's builder does
 * so enums and struct types come out in the same order, and collects the
 * problems it meets.
 */
function render(name: string, spec: KindSpec, problems: string[]): string {
  const problem = (msg: string, help?: string) => problems.push(help === undefined ? msg : `${msg} (${help})`);

  if (!IDENT.test(name)) problem(`kind name ${JSON.stringify(name)} isn't an identifier`, "use letters, digits and underscores, like AlertRouting");
  if (!Number.isInteger(spec.version) || spec.version < 1) problem(`version ${spec.version} isn't a whole number from 1`);
  const accepts = spec.accepts ?? 1;
  if (!Number.isInteger(accepts) || accepts < 1 || accepts > spec.version) {
    problem(`accepts ${accepts} isn't from 1 to the version, ${spec.version}`);
  }

  // Decisions and how they collect.
  if (spec.decisions !== undefined && spec.collect !== undefined) {
    problem("the kind sets both decisions and collect", "decisions ranks them for `collect one`, collect applies them all; use one");
  }
  const collect = spec.collect !== undefined;
  const decisions = [...(spec.decisions ?? []), ...(spec.collect ?? [])];
  if (decisions.length === 0) problem("the kind declares no decisions", "set decisions, or collect for a collecting kind");
  const byName = new Map<string, AnyDecision>();
  for (const d of decisions) {
    if (byName.has(d.name)) problem(`decision ${d.name} is declared twice`);
    byName.set(d.name, d);
    if (!IDENT.test(d.name)) problem(`decision name ${JSON.stringify(d.name)} isn't an identifier`);
    if (d.reasons.length === 0) problem(`decision ${d.name} declares no reasons`);
    const seen = new Set<string>();
    for (const r of d.reasons) {
      if (seen.has(r)) problem(`decision ${d.name} declares reason ${r} twice`);
      seen.add(r);
      if (!IDENT.test(r)) problem(`decision ${d.name}: reason ${JSON.stringify(r)} isn't an identifier`);
    }
  }
  let precedence: string[] = [];
  if (!collect) {
    precedence = decisions.map((d) => d.name);
    if (spec.precedence !== undefined) problem("precedence is for a collecting kind", "decisions already ranks them in order");
  } else if (spec.precedence !== undefined && spec.precedence.length > 0) {
    precedence = spec.precedence.map((d) => d.name);
    const listed = new Set(precedence);
    if (listed.size !== precedence.length || decisions.some((d) => !listed.has(d.name)) || precedence.some((n) => !byName.has(n))) {
      problem("precedence must list every decision of the kind once", `the decisions are ${decisions.map((d) => d.name).join(", ")}`);
    }
  }

  const declared = (o: Outcome, what: string): AnyDecision | undefined => {
    const d = byName.get(o.decision);
    if (d === undefined) problem(`${what} ${o}: decision ${o.decision} isn't one of the kind's`);
    else if (!d.reasons.includes(o.reason)) problem(`${what} ${o}: decision ${o.decision} has no reason ${o.reason}`);
    return d;
  };
  const fallback = (o: Outcome | undefined, what: string) => {
    if (o === undefined) return;
    const d = declared(o, what);
    for (const [field, f] of Object.entries(d?.payload ?? {})) {
      if (!(f instanceof Defaulted)) problem(`${what} ${o}: payload field ${field} has no default`, `give it one with .default(...)`);
    }
  };
  if (!collect && spec.default === undefined && decisions.length > 0) problem("a `collect one` kind needs a default", "set default to the outcome when no rule fires");
  if (collect && spec.conflict !== undefined) problem("conflict is for a `collect one` kind", "a collecting kind applies every decision, so nothing conflicts over the one outcome");
  fallback(spec.default, "default");
  fallback(spec.conflict, "conflict");

  // Reason rankings.
  const ranked = new Map<string, string[]>();
  for (const r of spec.reasonPrecedence ?? []) {
    const reasons = r instanceof Decision ? r.reasons.map((x) => new Outcome(r.name, x)) : [...r];
    const first = reasons[0];
    if (first === undefined) {
      problem("a reasonPrecedence entry names no reasons");
      continue;
    }
    const d = byName.get(first.decision);
    if (d === undefined) {
      problem(`reasonPrecedence: decision ${first.decision} isn't one of the kind's`);
      continue;
    }
    for (const o of reasons) {
      if (o.decision !== d.name) problem(`reasonPrecedence ${d.name}: reason ${o.reason} belongs to decision ${o.decision}`, "rank each decision's reasons in an entry of its own");
    }
    const names = reasons.filter((o) => o.decision === d.name).map((o) => o.reason);
    if (ranked.has(d.name)) problem(`reasonPrecedence ranks ${d.name} twice`);
    if (new Set(names).size !== names.length || d.reasons.some((x) => !names.includes(x)) || names.some((x) => !d.reasons.includes(x))) {
      problem(`reasonPrecedence ${d.name} must name every reason of ${d.name} once`, `${d.name} declares: ${d.reasons.join(", ")}`);
    }
    ranked.set(d.name, names);
  }

  // Exclusive sets.
  const exclusive: string[] = [];
  for (const set of spec.exclusive ?? []) {
    if (set.length < 2) problem("an exclusive set names at least two outcomes");
    for (const o of set) {
      if (o instanceof Outcome) declared(o, "exclusive");
      else if (!byName.has(o.name)) problem(`exclusive: decision ${o.name} isn't one of the kind's`);
    }
    exclusive.push(set.map((o) => (o instanceof Outcome ? o.toString() : o.name)).join(", "));
  }

  // Walk the types as Go's builder converts them: the inputs, then each
  // function's parameters and result, then each decision's payload. A
  // struct registers before its fields, so a nested struct follows the
  // struct it's in; an enum lands where it's first reached.
  const enums: EnumType<string, string>[] = [];
  const structs: StructType<string, Fields>[] = [];
  const names = new Map<string, AnyType>();
  const claim = (type: EnumType<string, string> | StructType<string, Fields>): boolean => {
    const prev = names.get(type.name);
    if (prev === type) return false;
    if (prev !== undefined) {
      problem(`two different types are both named ${type.name}`, "rename one; each enum and struct needs a name of its own");
      return false;
    }
    names.set(type.name, type);
    return true;
  };
  const walk = (type: AnyType): void => {
    if (type instanceof EnumType) {
      if (claim(type)) enums.push(type);
    } else if (type instanceof StructType) {
      if (claim(type)) {
        structs.push(type);
        for (const f of Object.values(type.fields as Fields)) walk(f);
      }
    } else {
      for (const inner of innerTypes(type)) walk(inner);
    }
  };
  for (const type of Object.values(spec.inputs)) walk(type);
  for (const [fname, f] of Object.entries(spec.functions ?? {})) {
    if (!IDENT.test(fname)) problem(`function name ${JSON.stringify(fname)} isn't an identifier`);
    for (const p of f.params) walk(p);
    walk(f.result);
    if (f.result instanceof OptionalType) {
      problem(`function ${fname}: the result can't be optional`, "return the zero value and let the policy compare, or return a list");
    }
  }
  for (const d of decisions) for (const f of Object.values(d.payload)) walk(f instanceof Defaulted ? f.type : f);
  for (const e of spec.enums ?? []) if (claim(e)) enums.push(e);
  for (const e of enums) {
    if (e.values.length === 0) problem(`enum ${e.name} declares no values`);
    if (new Set(e.values).size !== e.values.length) problem(`enum ${e.name} declares a value twice`);
  }

  // The kind file, as internal/kind's Source writes it.
  let out = `kind ${name} version ${spec.version}`;
  if (accepts > 1) out += `, accepts: ${accepts}`;
  out += "\n";
  if (enums.length > 0) out += "\n";
  for (const e of enums) out += `enum ${e.name}: ${e.values.join(" | ")}\n`;
  for (const s of structs) {
    const fields = Object.entries(s.fields as Fields);
    out += `\ntype ${s.name} {${fields.length > 0 ? "\n" : ""}`;
    for (const [field, type] of fields) out += `  ${field}: ${type.sigil}\n`;
    out += "}\n";
  }
  const inputs = Object.entries(spec.inputs);
  if (inputs.length > 0) out += "\n";
  for (const [input, type] of inputs) out += `input ${input}: ${type.sigil}\n`;
  const functions = Object.entries(spec.functions ?? {});
  if (functions.length > 0) out += "\n";
  for (const [fname, f] of functions) out += `fn ${fname}(${f.params.map((p) => p.sigil).join(", ")}) -> ${f.result.sigil}\n`;
  for (const d of decisions) {
    out += `\ndecision ${d.name} {\n  reason: ${d.reasons.join(" | ")}\n`;
    for (const [field, f] of Object.entries(d.payload)) {
      if (f instanceof Defaulted) {
        let value: string;
        try {
          if (f.type instanceof OptionalType && (f.value === null || f.value === undefined)) {
            throw new SigilError("`none` isn't a constant", { help: "leave the default out; a field without one is required" });
          }
          value = f.type.format(f.value);
        } catch (err) {
          problem(`decision ${d.name}: field ${field}: ${err instanceof Error ? err.message : String(err)}`);
          value = "?";
        }
        out += `  ${field}: ${f.type.sigil} = ${value}\n`;
      } else {
        out += `  ${field}: ${f.sigil}\n`;
      }
    }
    out += "}\n";
  }
  const resolution = [
    ...(decisions.length > 0 ? [collect ? "collect all" : "collect one"] : []),
    ...(precedence.length > 0 ? [`precedence ${precedence.join(" > ")}`] : []),
    ...decisions.flatMap((d) => {
      const r = ranked.get(d.name);
      return r === undefined ? [] : [`precedence ${d.name}: ${r.join(" > ")}`];
    }),
    ...exclusive.map((set) => `exclusive ${set}`),
  ];
  if (resolution.length > 0) out += `\n${resolution.join("\n")}\n`;
  if (spec.default !== undefined) out += `\ndefault ${spec.default.decision}(reason: ${spec.default.reason})\n`;
  if (spec.conflict !== undefined) {
    if (spec.default === undefined) out += "\n";
    out += `conflict ${spec.conflict.decision}(reason: ${spec.conflict.reason})\n`;
  }
  return out;
}

/** The types a list, map or optional holds, in the order Go converts them. */
function innerTypes(type: AnyType): AnyType[] {
  const t = type as unknown as { elem?: AnyType; key?: AnyType; value?: AnyType };
  if (t.key !== undefined && t.value !== undefined) return [t.key, t.value];
  return t.elem === undefined ? [] : [t.elem];
}

/** The name of the kind a source declares, if it's a kind file. */
function kindNameOf(source: string): string | undefined {
  const m = /^(?:\s*\/\/[^\n]*\n)*\s*kind\s+([A-Za-z_][A-Za-z0-9_]*)\s+version\b/.exec(source);
  return m?.[1];
}

/** AlertRouting → alert_routing, as `sigil export` names a kind's file. */
export function snakeCase(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1_$2")
    .toLowerCase();
}
