// The records the module returns, typed field for field after the JSON the
// stock `sigil` CLI prints with `-o json`. The module produces them with the
// same Go code, so a field that is optional here is one the CLI leaves out
// when it's empty.

/** A JSON value: what inputs, payloads, stubs and host function arguments are made of. */
export type JsonValue = string | number | boolean | null | JsonValue[] | { [key: string]: JsonValue };

/**
 * One virtual file. Paths appear in diagnostics and positions exactly as the
 * CLI prints them for the same relative path.
 */
export interface SourceFile {
  path: string;
  source: string;
}

/** How a lint is reported, as a configuration file spells it. */
export type LintLevel = "off" | "warn" | "error";

/**
 * A requirement on one policy, like a `require` entry in `sigil.yaml`: the
 * policy must be trusted, or reachable only from the given roots.
 */
export interface Requirement {
  policy: string;
  trusted?: string[];
  roots?: string[];
}

/** One error or lint finding, as `sigil check -o json` prints it. */
export interface Diagnostic {
  severity: "error" | "warning";
  /** The lint's name, for a lint finding. */
  lint?: string;
  file?: string;
  /** The document the diagnostic is in, when that's known. */
  document?: string;
  message: string;
  /** How to fix it. */
  help?: string;
  /** From 1; left out when the diagnostic has no position. */
  line?: number;
  /** In characters, from 1. */
  column?: number;
}

/** What `sigil version -o json` reports about the module's build. */
export interface VersionInfo {
  /** The release version, or `devel` for a build that isn't one. */
  version: string;
  commit: string;
  commitTime: string;
  dirty: boolean;
  goVersion: string;
  /** `wasip1/wasm`. */
  platform: string;
}

/**
 * A stand-in for one host function, in the format of a test file's
 * `stubs:`: a fixed result, a fixed error, or results for particular
 * arguments.
 */
export interface Stub {
  /** The result of a call no entry of `calls` matches. */
  returns?: JsonValue;
  /** The message a call no entry of `calls` matches fails with. */
  error?: string;
  /** Results for particular args, matched in order; the first match wins. */
  calls?: StubCall[];
}

/** One entry of a {@link Stub}'s `calls`: exactly one of `returns` and `error`. */
export interface StubCall {
  /** The args the entry answers, one per param, compared as values. */
  args: JsonValue[];
  returns?: JsonValue;
  error?: string;
}

/** One candidate or outcome entry of an evaluation. */
export interface EvalEntry {
  payload?: Record<string, JsonValue>;
  decision: string;
  reason: string;
  /** The policy whose rule produced it; left out for the default. */
  policy?: string;
  /** The rule's position, `file:line:column`; left out for the default. */
  position?: string;
  /** The invocations it was reached through, outermost first. */
  chain?: string[];
  /** The `when` conditions that held, for a candidate of a winning decision. */
  conditions?: string[];
  /** In the outcome the host acts on. */
  outcome?: boolean;
}

/** One failing assert. */
export interface FailedAssert {
  /** The assert's name. */
  reason: string;
  /** The policy the assert is in. */
  policy: string;
  /** Its position, after the invocations that reached it. */
  position: string;
  /** The runtime error its condition raised. */
  cause?: string;
  /** What to do about the cause, when it knows better than the failure's help. */
  help?: string;
  /** For an outcome assert, the candidates that formed the outcome it read. */
  outcome?: EvalEntry[];
}

/** Why an evaluation didn't produce an outcome. */
export interface EvalFailure {
  /**
   * What failed: a runtime error, a conflict, failing asserts, or `canceled`
   * when the evaluation ran past its `timeoutMs` (not a policy bug).
   */
  kind: "assertion" | "conflict" | "runtime" | "canceled";
  /** For an assertion failure: whether the input asserts or the outcome asserts failed. */
  phase?: "input" | "outcome";
  message: string;
  help: string;
  /** The failing asserts, for an assertion failure. */
  asserts?: FailedAssert[];
  /** The conflicting candidates, for a conflict. */
  candidates?: EvalEntry[];
}

/**
 * One evaluation, as `sigil eval -o json` prints it. A failed evaluation
 * still returns a result: `error` says why, and the outcome is the kind's
 * fallback (its default, or its conflict outcome after a conflict).
 */
export interface EvalResult {
  /** For a kind that returns one decision, the outcome's payload. */
  payload?: Record<string, JsonValue>;
  error?: EvalFailure;
  /** The root policy. */
  policy: string;
  /** For a kind that returns one decision, the outcome's decision. */
  decision?: string;
  /** For a kind that returns one decision, the outcome's reason. */
  reason?: string;
  /** What the host acts on. */
  outcome: EvalEntry[];
  /** Every candidate the rules produced, winners first. */
  trace: EvalEntry[];
  /** The kind collects every candidate. */
  collect?: boolean;
}

/** One rule or assert of an explanation. */
export interface ExplainEntry {
  kind: "decision" | "assert";
  /** The decision a rule returns; left out for an assert. */
  decision?: string;
  /** A rule's reason, or an assert's name. */
  reason: string;
  /** `input` or `outcome`, for an assert. */
  phase?: "input" | "outcome";
  /** `policy:line`, outermost call first, the rule last. */
  chain: string[];
  /** Every `when` on the way, outermost first. */
  conditions: string[];
  /** An assert's own condition. */
  check?: string;
  /** A rule's payload arguments, as `name = expression`. */
  payload?: string[];
}

/** One policy flattened, as `sigil explain -o json` prints it. */
export interface Explanation {
  policy: string;
  /** The policy itself and every one it invokes. */
  policies: number;
  /** Every module those policies use. */
  modules: number;
  /** Every rule, then every assert. */
  rules: ExplainEntry[];
}

/** Options shared by the operations that read files. */
export interface CheckOptions {
  /** Name patterns of the policies to check, like `sigil check`'s arguments; every policy without them. */
  policies?: string[];
  /**
   * Policies every checked policy must invoke unconditionally, like
   * `require:` in `sigil.yaml`. `trusted` paths may point into
   * `trustedFiles`.
   */
  require?: Requirement[];
  /** Documents read as trusted, like the files a host passes to Go's `policy.From`. */
  trustedFiles?: SourceFile[];
  /** Overrides each named lint's default level, like `lints:` in `sigil.yaml`. */
  lints?: Record<string, LintLevel>;
}

/** A host function: called with the Sigil arguments, returns the result. */
export type HostFunction = (...args: any[]) => unknown;

export interface CompileOptions {
  /** The policy to compile; may be left out when the files hold exactly one. */
  policy?: string;
  /**
   * Policies the compiled policy must invoke unconditionally at its top
   * level, like Go's `policy.Require(name, policy.From(trusted))`. With
   * `trustedFiles`, a required policy must be defined there, and a policy
   * of the files that omits, gates or redefines it, or passes a param
   * out of its bounds, fails to compile. Without `trustedFiles`, any
   * policy of the files satisfies it.
   */
  require?: CompileRequirement[];
  /**
   * The documents a required policy is read from: the host's own, which
   * the files (say, a team's bundle) can't replace. A path is in `files`
   * or here, not both; trust comes from the list, not the path.
   */
  trustedFiles?: SourceFile[];
  /**
   * Stand-ins for host functions, as a test file's `stubs:` gives them. A
   * stub replaces an implementation in `functions` of the same name.
   */
  stubs?: Record<string, Stub>;
  /**
   * Implementations of the kind's host functions. They run synchronously,
   * inside the evaluation, with arguments in the JSON form of inputs
   * (durations as strings like "2h30m", timestamps as RFC 3339).
   */
  functions?: Record<string, HostFunction>;
}

export interface EvalOptions {
  /** Stops the evaluation after this many milliseconds, with a `canceled` failure. */
  timeoutMs?: number;
}

/** A policy a compiled policy must invoke; see {@link CompileOptions.require}. */
export interface CompileRequirement {
  policy: string;
}

export interface ExplainOptions {
  policy?: string;
  /** Documents read as trusted, as for {@link CompileOptions.trustedFiles}. */
  trustedFiles?: SourceFile[];
}

export interface FormatOptions {
  /** The file's path, for the diagnostics of a source that doesn't parse. */
  path?: string;
}
