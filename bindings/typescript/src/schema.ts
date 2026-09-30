// The types a kind is built from, the TypeScript twin of the Go types
// policy.NewKind reflects over: `t.string`, `t.list(t.int)`, enums and
// structs. Each carries two phantom types, what a host passes in (an
// input, a host function's result) and what comes back out (a payload, a
// host function's argument), which differ for timestamps (a Date goes in,
// a string comes out) and optionals (a key may be left out going in).

import { formatDuration, toMs } from "./duration.js";
import { SigilError } from "./errors.js";
import type { Duration } from "./duration.js";

/** A timestamp as it comes back: an RFC 3339 string. */
export type Timestamp = string;

/**
 * A Sigil type. `In` is what a host passes for it, `Out` what the module
 * hands back; both are JSON-shaped.
 */
export abstract class SigilType<In, Out = In> {
  /** Phantom: the type a host passes in. Never set at run time. */
  declare readonly "~in": In;
  /** Phantom: the type the module hands back. Never set at run time. */
  declare readonly "~out": Out;

  /** The type as a kind file writes it, like `list<string>` or `?Team`. */
  abstract readonly sigil: string;

  /**
   * A payload field of this type with a default, which the field takes
   * when a rule leaves it out, and which the kind's default and conflict
   * decisions take for every field.
   */
  default(value: In): Defaulted<this> {
    return new Defaulted(this, value);
  }

  /** @internal Renders a constant of this type as Go's constant.Format does. */
  abstract format(value: unknown): string;

  /** @internal The enums and structs this type refers to, outermost first. */
  named(): (EnumType<string, string> | StructType<string, Fields>)[] {
    return [];
  }

  toString(): string {
    return this.sigil;
  }
}

/** A payload field type with its default. */
export class Defaulted<T extends SigilType<unknown, unknown>> {
  constructor(
    readonly type: T,
    readonly value: T["~in"],
  ) {}
}

/** The fields of a struct: name to type. */
export type Fields = Record<string, AnyType>;

// Any Sigil type; `any` because SigilType is invariant in its phantoms.
export type AnyType = SigilType<any, any>;

/** What a host passes for a type. */
export type In<T> = T extends SigilType<infer I, unknown> ? I : never;
/** What the module hands back for a type. */
export type Out<T> = T extends SigilType<unknown, infer O> ? O : never;

type Simplify<T> = { [K in keyof T]: T[K] } & {};

/** An object of fields as a host passes it: optional fields may be left out. */
export type FieldsIn<F extends Fields> = Simplify<
  { -readonly [K in keyof F as F[K] extends OptionalType<AnyType> ? never : K]: In<F[K]> } & {
    -readonly [K in keyof F as F[K] extends OptionalType<AnyType> ? K : never]?: In<F[K]>;
  }
>;

/** An object of fields as the module hands it back. */
export type FieldsOut<F extends Fields> = Simplify<{ -readonly [K in keyof F]: Out<F[K]> }>;

/** A type without parts: `string`, `bool`, `int`, `float`, `duration` or `timestamp`. */
export class BasicType<In, Out = In> extends SigilType<In, Out> {
  readonly #formatter: (v: unknown) => string;

  constructor(
    readonly sigil: string,
    formatter: (v: unknown) => string,
  ) {
    super();
    this.#formatter = formatter;
  }

  format(value: unknown): string {
    return this.#formatter(value);
  }
}

/** `list<T>`. */
export class ListType<E extends AnyType> extends SigilType<readonly In<E>[], Out<E>[]> {
  readonly sigil: string;

  constructor(readonly elem: E) {
    super();
    this.sigil = `list<${elem.sigil}>`;
  }

  format(value: unknown): string {
    if (!Array.isArray(value)) throw badConstant(this.sigil, value);
    return `[${value.map((v) => this.elem.format(v)).join(", ")}]`;
  }

  override named(): ReturnType<AnyType["named"]> {
    return this.elem.named();
  }
}

/** The key of a map as a host writes it: an enum's values, or text. */
export type KeyIn<K> = K extends EnumType<string, infer V> ? V : K extends SigilType<number, number> ? number | string : string;

/** `map<K, V>`. Keys cross as JSON object keys: `"3"` for an int, `"45m"` for a duration. */
export class MapType<K extends AnyType, V extends AnyType> extends SigilType<
  Partial<Record<KeyIn<K>, In<V>>>,
  Partial<Record<KeyIn<K>, Out<V>>>
> {
  readonly sigil: string;

  constructor(
    readonly key: K,
    readonly value: V,
  ) {
    super();
    this.sigil = `map<${key.sigil}, ${value.sigil}>`;
  }

  format(value: unknown): string {
    const entries = value instanceof Map ? [...value.entries()] : isObject(value) ? Object.entries(value) : undefined;
    if (entries === undefined) throw badConstant(this.sigil, value);
    // Go sorts the formatted entries by their bytes.
    const numeric = this.key.sigil === "int" || this.key.sigil === "float";
    const parts = entries.map(([k, v]) => `${this.key.format(numeric && typeof k === "string" ? Number(k) : k)}: ${this.value.format(v)}`);
    return `{${parts.sort(compareBytes).join(", ")}}`;
  }

  override named(): ReturnType<AnyType["named"]> {
    return [...this.key.named(), ...this.value.named()];
  }
}

/** `?T`: a value, or none. As a field, the key may be left out. */
export class OptionalType<E extends AnyType> extends SigilType<In<E> | null | undefined, Out<E> | null> {
  /** Phantom: tells an optional apart from a list, which has the same shape. */
  declare readonly "~optional": true;
  readonly sigil: string;

  constructor(readonly elem: E) {
    super();
    this.sigil = `?${elem.sigil}`;
  }

  format(value: unknown): string {
    return value === null || value === undefined ? "none" : this.elem.format(value);
  }

  override named(): ReturnType<AnyType["named"]> {
    return this.elem.named();
  }
}

/** An enum: a closed set of names, written bare in policies. */
export class EnumType<N extends string, V extends string> extends SigilType<V, V> {
  readonly sigil: N;

  constructor(
    readonly name: N,
    readonly values: readonly V[],
  ) {
    super();
    this.sigil = name;
  }

  format(value: unknown): string {
    if (typeof value !== "string" || !this.values.includes(value as V)) throw badConstant(this.sigil, value);
    return value;
  }

  override named(): ReturnType<AnyType["named"]> {
    return [this as EnumType<string, string>];
  }
}

/** A struct type: named fields, each with its type. */
export class StructType<N extends string, F extends Fields> extends SigilType<FieldsIn<F>, FieldsOut<F>> {
  readonly sigil: N;

  constructor(
    readonly name: N,
    readonly fields: F,
  ) {
    super();
    this.sigil = name;
  }

  format(value: unknown): string {
    throw new SigilError(`a default of struct type ${this.name} can't be written in a kind file`, {
      help: "give struct-typed payload fields no default",
      cause: value,
    });
  }

  // A struct lists itself before its fields' types, as Go registers the
  // shell before converting the fields.
  override named(): ReturnType<AnyType["named"]> {
    return [this as StructType<string, Fields>];
  }
}

/**
 * The Sigil types. Enums and structs come from {@link enumType} and
 * {@link struct}.
 */
export const t = {
  string: new BasicType<string>("string", (v) => {
    if (typeof v !== "string") throw badConstant("string", v);
    return goQuote(v);
  }),
  bool: new BasicType<boolean>("bool", (v) => {
    if (typeof v !== "boolean") throw badConstant("bool", v);
    return String(v);
  }),
  /** A 64-bit integer; beyond ±2^53 a JavaScript number loses precision. */
  int: new BasicType<number>("int", (v) => {
    if (typeof v !== "number" || !Number.isSafeInteger(v)) throw badConstant("int", v);
    return String(v);
  }),
  float: new BasicType<number>("float", (v) => {
    if (typeof v !== "number" || !Number.isFinite(v)) throw badConstant("float", v);
    return formatFloat(v);
  }),
  /** A duration in Sigil's syntax, like "1h30m"; see {@link Duration}. */
  duration: new BasicType<Duration>("duration", (v) => {
    if (typeof v !== "string") throw badConstant("duration", v);
    return formatDuration(toMs(v));
  }),
  /** A timestamp: an RFC 3339 string or a Date going in, an RFC 3339 string coming out. */
  timestamp: new BasicType<Timestamp | Date, Timestamp>("timestamp", () => {
    throw new SigilError("a timestamp field can't have a default", { help: "timestamps come from input; there's no literal for one" });
  }),
  list<E extends AnyType>(elem: E): ListType<E> {
    return new ListType(elem);
  },
  map<K extends AnyType, V extends AnyType>(key: K, value: V): MapType<K, V> {
    return new MapType(key, value);
  },
  optional<E extends AnyType>(elem: E): OptionalType<E> {
    return new OptionalType(elem);
  },
} as const;

/**
 * Declares an enum: `enumType("Severity", ["critical", "warning", "info"])`.
 * Policies write its values bare, and the kind prints it before the types
 * that use it.
 */
export function enumType<const N extends string, const V extends readonly string[]>(name: N, values: V): EnumType<N, V[number]> {
  return new EnumType<N, V[number]>(name, values);
}

/**
 * Declares a struct type: `struct("Team", { name: t.string, oncall: t.string })`.
 * Field order is declaration order in the kind file.
 */
export function struct<const N extends string, const F extends Fields>(name: N, fields: F): StructType<N, F> {
  return new StructType(name, fields);
}

/**
 * Quotes a string as Go's %q verb does, which is how a kind file writes a
 * string constant: printable characters as they are, the rest escaped.
 */
export function goQuote(s: string): string {
  let out = '"';
  for (const ch of s) {
    const cp = ch.codePointAt(0) ?? 0;
    switch (ch) {
      case '"':
        out += '\\"';
        continue;
      case "\\":
        out += "\\\\";
        continue;
      case "\x07":
        out += "\\a";
        continue;
      case "\b":
        out += "\\b";
        continue;
      case "\f":
        out += "\\f";
        continue;
      case "\n":
        out += "\\n";
        continue;
      case "\r":
        out += "\\r";
        continue;
      case "\t":
        out += "\\t";
        continue;
      case "\v":
        out += "\\v";
        continue;
    }
    if (cp >= 0xd800 && cp <= 0xdfff) {
      // A lone surrogate is invalid UTF-8 on the Go side, where it
      // arrives as U+FFFD.
      out += "�";
    } else if (isPrint(ch, cp)) {
      out += ch;
    } else if (cp < 0x80) {
      out += `\\x${cp.toString(16).padStart(2, "0")}`;
    } else if (cp <= 0xffff) {
      out += `\\u${cp.toString(16).padStart(4, "0")}`;
    } else {
      out += `\\U${cp.toString(16).padStart(8, "0")}`;
    }
  }
  return `${out}"`;
}

// Go's unicode.IsPrint: letters, marks, numbers, punctuation, symbols and
// the ASCII space.
const PRINTABLE = /^[\p{L}\p{M}\p{N}\p{P}\p{S}]$/u;

function isPrint(ch: string, cp: number): boolean {
  return cp === 0x20 || PRINTABLE.test(ch);
}

/**
 * Formats a float as Go's strconv.FormatFloat(v, 'f', -1, 64) does, plus
 * a `.0` for a whole number: the shortest digits that round-trip, never
 * in exponent notation.
 */
export function formatFloat(v: number): string {
  let s: string;
  if (Object.is(v, -0)) {
    s = "-0";
  } else {
    const [mantissa = "", exp = "0"] = Math.abs(v).toExponential().split("e");
    const digits = mantissa.replace(".", "");
    const e = Number(exp);
    if (e >= digits.length - 1) {
      s = digits + "0".repeat(e - digits.length + 1);
    } else if (e >= 0) {
      s = `${digits.slice(0, e + 1)}.${digits.slice(e + 1)}`;
    } else {
      s = `0.${"0".repeat(-e - 1)}${digits}`;
    }
    if (v < 0) s = `-${s}`;
  }
  return s.includes(".") ? s : `${s}.0`;
}

function compareBytes(a: string, b: string): number {
  const x = new TextEncoder().encode(a);
  const y = new TextEncoder().encode(b);
  for (let i = 0; i < Math.min(x.length, y.length); i++) {
    const d = (x[i] ?? 0) - (y[i] ?? 0);
    if (d !== 0) return d;
  }
  return x.length - y.length;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function badConstant(sigil: string, value: unknown): SigilError {
  let shown: string;
  try {
    shown = JSON.stringify(value) ?? String(value);
  } catch {
    shown = String(value);
  }
  return new SigilError(`${shown} isn't a constant of type ${sigil}`, {
    help: `give the default as the value a host would pass for a ${sigil}`,
  });
}
