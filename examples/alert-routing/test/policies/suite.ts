// Runs sigil test files (`*_test.yaml`) through @spechtlabs/sigil, the way
// the stock `sigil test` runs them: compile the file's policy with the
// file's stubs, evaluate each case's input, and compare the outcome with the
// case's expect. It supports the fields the CLI's runner reads: policy,
// stubs, cases[].name/input/input_file/stubs and expect's
// decision/reason/payload, outcome, error and asserts.
import { readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import type { EvalEntry, EvalResult, JsonValue, Sigil, SourceFile, Stub } from "@spechtlabs/sigil";
import { parse } from "yaml";

/** One test file. */
export interface Suite {
  /** The file's path, relative to the policies directory. */
  file: string;
  policy: string;
  stubs?: Record<string, Stub>;
  cases: Case[];
}

/** One case of a test file. */
export interface Case {
  name: string;
  input?: Record<string, JsonValue>;
  input_file?: string;
  stubs?: Record<string, Stub>;
  expect: Expect;
}

/** What a case expects. */
export interface Expect {
  decision?: string;
  reason?: string;
  payload?: Record<string, JsonValue>;
  outcome?: { decision: string; reason: string; payload?: Record<string, JsonValue> }[];
  /** Text the runtime error's message contains. */
  error?: string;
  /** The reasons of the asserts that must fail, in any order. */
  asserts?: string[];
}

/** The project configuration of sigil.yaml that `sigil check` enforces. */
export interface Config {
  kinds?: string[];
  require?: { policy: string; trusted?: string[]; roots?: string[] }[];
  lints?: Record<string, "off" | "warn" | "error">;
}

/** Every `.sigil` file under dir, with paths relative to it, the kind file included. */
export function readSources(dir: string): SourceFile[] {
  return [...new Bun.Glob("**/*.sigil").scanSync({ cwd: dir, dot: false })]
    .sort()
    .map((path) => ({ path, source: readFileSync(join(dir, path), "utf8") }));
}

/** Every test file under dir, in path order. */
export function readSuites(dir: string): Suite[] {
  return [...new Bun.Glob("**/*_test.yaml").scanSync({ cwd: dir })].sort().map((path) => {
    const doc = parse(readFileSync(join(dir, path), "utf8")) as Omit<Suite, "file">;
    return { ...doc, file: path, cases: doc.cases ?? [] };
  });
}

/** sigil.yaml in dir. */
export function readConfig(dir: string): Config {
  return parse(readFileSync(join(dir, "sigil.yaml"), "utf8")) as Config;
}

/**
 * Runs one case and returns how its evaluation differs from what it
 * expects, empty when it passed. It compiles the suite's policy with the
 * case's stubs over the suite's, per function, like the CLI.
 */
export function runCase(sigil: Sigil, dir: string, files: SourceFile[], suite: Suite, c: Case): string[] {
  using policy = sigil.compile(files, { policy: suite.policy, stubs: { ...suite.stubs, ...c.stubs } });
  const res = policy.eval(caseInput(dir, suite, c));
  return compare(c.expect, res);
}

/** Where a case's input comes from: inline, or a JSON or YAML file next to the test file. */
function caseInput(dir: string, suite: Suite, c: Case): Record<string, JsonValue> {
  if (c.input_file === undefined) return c.input ?? {};
  const path = join(dir, dirname(suite.file), c.input_file);
  return parse(readFileSync(path, "utf8")) as Record<string, JsonValue>;
}

/** The CLI runner's comparison, rule for rule. */
export function compare(e: Expect, res: EvalResult): string[] {
  if (e.error !== undefined) {
    if (res.error?.kind === "runtime" && res.error.message.includes(e.error)) return [];
    return [`got ${describe(res)}, want a runtime error containing ${JSON.stringify(e.error)}`];
  }
  if (e.asserts !== undefined) {
    const got = [...new Set(res.error?.asserts?.map((a) => a.reason) ?? [])].sort();
    const want = [...new Set(e.asserts)].sort();
    if (res.error?.kind === "assertion" && JSON.stringify(got) === JSON.stringify(want)) return [];
    return [`got ${describe(res)}, want failing asserts ${want.join(", ")}`];
  }
  if (res.error !== undefined) {
    return [`got ${describe(res)}, want ${e.outcome === undefined ? call(e.decision, e.reason) : "an outcome"}`];
  }
  if (e.outcome !== undefined) return compareOutcome(e.outcome, res.outcome);

  const got = res.outcome[0];
  if (got === undefined) return [`got no decision, want ${call(e.decision, e.reason)}`];
  if (got.decision !== e.decision || got.reason !== e.reason) {
    return [`got ${call(got.decision, got.reason)}, want ${call(e.decision, e.reason)}`];
  }
  return comparePayload(e.payload, got.payload);
}

function compareOutcome(want: NonNullable<Expect["outcome"]>, got: EvalEntry[]): string[] {
  const used = got.map(() => false);
  const failures: string[] = [];
  for (const w of want) {
    const i = got.findIndex(
      (g, j) =>
        !used[j] &&
        g.decision === w.decision &&
        g.reason === w.reason &&
        comparePayload(w.payload, g.payload).length === 0,
    );
    if (i < 0) failures.push(`missing from the outcome: ${call(w.decision, w.reason)}`);
    else used[i] = true;
  }
  got.forEach((g, j) => {
    if (!used[j]) failures.push(`not expected in the outcome: ${call(g.decision, g.reason)} at ${g.position}`);
  });
  return failures;
}

// Only the fields the case lists are compared, as the CLI does.
function comparePayload(want: Record<string, JsonValue> | undefined, got: Record<string, JsonValue> | undefined) {
  const failures: string[] = [];
  for (const [field, w] of Object.entries(want ?? {})) {
    const g = got?.[field];
    if (!sameValue(w, g)) failures.push(`payload ${field} = ${JSON.stringify(g)}, want ${JSON.stringify(w)}`);
  }
  return failures;
}

// Values compare as JSON, except that two duration strings compare by
// length of time: the case may write 90m where the engine renders 1h30m0s.
function sameValue(want: JsonValue, got: JsonValue | undefined): boolean {
  if (typeof want === "string" && typeof got === "string") {
    const w = parseDuration(want);
    const g = parseDuration(got);
    if (w !== undefined && g !== undefined) return w === g;
  }
  return JSON.stringify(want) === JSON.stringify(got);
}

const UNITS: Record<string, number> = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1e3, m: 6e4, h: 3.6e6 };

/** A Go duration string in milliseconds, or undefined when it isn't one. */
export function parseDuration(s: string): number | undefined {
  const m = /^([+-])?((?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+|0)$/.exec(s);
  if (m === null) return undefined;
  let total = 0;
  for (const [, n, unit] of (m[2] as string).matchAll(/(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g)) {
    total += Number(n) * (UNITS[unit as string] as number);
  }
  return m[1] === "-" ? -total : total;
}

function call(decision: string | undefined, reason: string | undefined): string {
  return `${decision ?? ""}(reason: ${reason ?? ""})`;
}

function describe(res: EvalResult): string {
  if (res.error !== undefined) return `a ${res.error.kind} failure: ${res.error.message}`;
  const first = res.outcome[0];
  return first === undefined ? "no decision" : call(first.decision, first.reason);
}

/** The path of a suite's file for a report, relative to where bun test runs. */
export function displayPath(dir: string, suite: Suite): string {
  return relative(process.cwd(), join(dir, suite.file));
}
