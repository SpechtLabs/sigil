// The kind builder without the module: validation, the constant
// formatting schema() relies on, duration helpers, decision handles and
// the types InputOf derives.

import { describe, expect, test } from "bun:test";

import {
  decision,
  defineKind,
  duration,
  enumType,
  fn,
  type InputOf,
  type KindSpec,
  ms,
  SigilError,
  struct,
  t,
  toMs,
} from "../src/index.js";
import type { EvalResult } from "../src/index.js";
import { markResult } from "../src/decision.js";
import { formatFloat, goQuote } from "../src/schema.js";
import { snakeCase } from "../src/kind.js";
import { A, Collecting } from "./kinds/coverage.js";
import { AlertRouting, Drop, Notify, Page } from "./kinds/examples.js";

const Ok = decision("ok", ["yes", "no"], { note: t.string.default("") });
const Bare = decision("bare", ["only"], { target: t.string });

function problems(name: string, spec: KindSpec): string {
  try {
    defineKind(name, spec);
  } catch (err) {
    expect(err).toBeInstanceOf(SigilError);
    return (err as Error).message;
  }
  throw new Error("defineKind didn't throw");
}

describe("defineKind validation", () => {
  test.each([
    ["a name that isn't an identifier", "Bad Name", { version: 1, inputs: {}, decisions: [Ok], default: Ok.reason("yes") }, `kind name "Bad Name" isn't an identifier`],
    ["version 0", "K", { version: 0, inputs: {}, decisions: [Ok], default: Ok.reason("yes") }, "version 0 isn't a whole number from 1"],
    ["accepts above the version", "K", { version: 1, accepts: 2, inputs: {}, decisions: [Ok], default: Ok.reason("yes") }, "accepts 2 isn't from 1 to the version, 1"],
    ["no decisions", "K", { version: 1, inputs: {} }, "the kind declares no decisions"],
    ["decisions and collect", "K", { version: 1, inputs: {}, decisions: [Ok], collect: [Bare], default: Ok.reason("yes") }, "the kind sets both decisions and collect"],
    ["a collect one kind without a default", "K", { version: 1, inputs: {}, decisions: [Ok] }, "a `collect one` kind needs a default"],
    ["a default with a required field", "K", { version: 1, inputs: {}, decisions: [Bare], default: Bare.reason("only") }, "default bare.only: payload field target has no default"],
    ["a default of another decision", "K", { version: 1, inputs: {}, decisions: [Ok], default: Bare.reason("only") }, "default bare.only: decision bare isn't one of the kind's"],
    ["conflict on a collecting kind", "K", { version: 1, inputs: {}, collect: [Ok], conflict: Ok.reason("no") }, "conflict is for a `collect one` kind"],
    ["precedence on a collect one kind", "K", { version: 1, inputs: {}, decisions: [Ok], precedence: [Ok], default: Ok.reason("yes") }, "precedence is for a collecting kind"],
    ["precedence missing a decision", "K", { version: 1, inputs: {}, collect: [Ok, Bare], precedence: [Ok] }, "precedence must list every decision of the kind once"],
    ["a decision declared twice", "K", { version: 1, inputs: {}, decisions: [Ok, Ok], default: Ok.reason("yes") }, "decision ok is declared twice"],
    ["an incomplete reason ranking", "K", { version: 1, inputs: {}, decisions: [Ok], reasonPrecedence: [[Ok.reason("yes")]], default: Ok.reason("yes") }, "reasonPrecedence ok must name every reason of ok once"],
    ["a ranking mixing decisions", "K", { version: 1, inputs: {}, decisions: [Ok, Bare], reasonPrecedence: [[Ok.reason("yes"), Bare.reason("only"), Ok.reason("no")]], default: Ok.reason("yes") }, "reasonPrecedence ok: reason only belongs to decision bare"],
    ["a decision ranked twice", "K", { version: 1, inputs: {}, decisions: [Ok], reasonPrecedence: [Ok, Ok], default: Ok.reason("yes") }, "reasonPrecedence ranks ok twice"],
    ["an exclusive set of one", "K", { version: 1, inputs: {}, collect: [Ok], exclusive: [[Ok]] }, "an exclusive set names at least two outcomes"],
    ["two types of one name", "K", { version: 1, inputs: { a: struct("T", {}), b: struct("T", { x: t.int }) }, decisions: [Ok], default: Ok.reason("yes") }, "two different types are both named T"],
    ["an enum without values", "K", { version: 1, inputs: { e: enumType("E", []) }, decisions: [Ok], default: Ok.reason("yes") }, "enum E declares no values"],
    ["an optional function result", "K", { version: 1, inputs: {}, functions: { f: fn([], t.optional(t.int)) }, decisions: [Ok], default: Ok.reason("yes") }, "function f: the result can't be optional"],
    ["a `none` default", "K", { version: 1, inputs: {}, decisions: [decision("d", ["r"], { x: t.optional(t.int).default(null) })], default: decision("d", ["r"]).reason("r") }, "`none` isn't a constant"],
    ["a timestamp default", "K", { version: 1, inputs: {}, decisions: [decision("d", ["r"], { at: t.timestamp.default(new Date()) })], default: decision("d", ["r"]).reason("r") }, "a timestamp field can't have a default"],
    ["an invalid duration default", "K", { version: 1, inputs: {}, decisions: [decision("d", ["r"], { x: t.duration.default("1.5h") })], default: decision("d", ["r"]).reason("r") }, 'invalid duration "1.5h"'],
    ["an enum default outside the enum", "K", { version: 1, inputs: {}, decisions: [decision("d", ["r"], { x: enumType("E", ["a"]).default("b" as "a") })], default: decision("d", ["r"]).reason("r") }, `"b" isn't a constant of type E`],
  ] as [string, string, KindSpec, string][])("%s", (_case, name, spec, want) => {
    expect(problems(name, spec)).toContain(want);
  });

  test("lists every problem at once", () => {
    const msg = problems("Bad Name", { version: 0, inputs: {}, decisions: [Ok] });
    expect(msg).toStartWith("defineKind(Bad Name): invalid kind:\n");
    expect(msg.split("\n")).toHaveLength(5);
  });
});

describe("constant formatting", () => {
  test.each([
    ["plain", "#alerts", '"#alerts"'],
    ["quotes and backslashes", 'a "b" \\c', '"a \\"b\\" \\\\c"'],
    ["the short escapes", "\x07\b\f\n\r\t\v", '"\\a\\b\\f\\n\\r\\t\\v"'],
    ["other control characters", "\x00\x1f\x7f", '"\\x00\\x1f\\x7f"'],
    ["printable Unicode", "é✓😀", '"é✓😀"'],
    ["unprintable Unicode", "  \u{e0001}", '"\\u00a0\\u2028\\U000e0001"'],
    ["a lone surrogate", "\ud800", '"�"'],
  ])("goQuote: %s", (_name, s, want) => {
    expect(goQuote(s)).toBe(want);
  });

  test.each([
    [0, "0.0"],
    [-0, "-0.0"],
    [2, "2.0"],
    [0.5, "0.5"],
    [-1.25, "-1.25"],
    [1e21, "1000000000000000000000.0"],
    [1.5e-7, "0.00000015"],
    [123456.789, "123456.789"],
    [0.1 + 0.2, "0.30000000000000004"],
  ])("formatFloat(%p) is %p", (v, want) => {
    expect(formatFloat(v)).toBe(want);
  });

  test.each([
    ["AlertRouting", "alert_routing"],
    ["DeployApproval", "deploy_approval"],
    ["HTTPGate", "http_gate"],
    ["Minimal", "minimal"],
  ])("snakeCase(%p) is %p", (name, want) => {
    expect(snakeCase(name)).toBe(want);
  });
});

describe("durations", () => {
  test.each([
    ["90m", "1h30m", 5_400_000],
    ["1h30m", "1h30m", 5_400_000],
    ["2d", "2d", 172_800_000],
    ["36h", "1d12h", 129_600_000],
    ["1500ms", "1s500ms", 1_500],
    ["0s", "0s", 0],
    ["", "0s", 0],
  ])("%p", (text, canonical, milliseconds) => {
    expect(duration(text)).toBe(canonical);
    expect(toMs(text)).toBe(milliseconds);
    expect(ms(milliseconds)).toBe(canonical);
  });

  test.each([
    ["1.5h", "units are d, h, m, s and ms"],
    ["1h1h", "each unit may appear once"],
    ["30m1h", "write the largest unit first"],
    ["5", "units are d, h, m, s and ms"],
    ["1us", "units are d, h, m, s and ms"],
    ["-1h", "units are d, h, m, s and ms"],
    ["106752d", "at most about 292 years"],
  ])("rejects %p", (text, help) => {
    try {
      toMs(text);
      throw new Error("toMs didn't throw");
    } catch (err) {
      expect(err).toBeInstanceOf(SigilError);
      expect((err as SigilError).message).toBe(`invalid duration ${JSON.stringify(text)}`);
      expect((err as SigilError).help).toContain(help);
    }
  });

  test.each([[-1], [1.5], [Number.NaN], [1e13]])("ms(%p) throws", (v) => {
    expect(() => ms(v)).toThrow(SigilError);
  });
});

describe("decision handles", () => {
  const result = (outcome: EvalResult["outcome"], extra: Partial<EvalResult> = {}): EvalResult => ({
    policy: "p",
    outcome,
    trace: [],
    ...extra,
  });
  const paged = result([{ decision: "page", reason: "sustained", payload: { target: "oncall-checkout" } }]);
  const dropped = result([{ decision: "drop", reason: "muted" }]);

  test("match returns the payload of a single outcome of the decision", () => {
    expect(Page.match(paged)).toEqual({ target: "oncall-checkout" });
    expect(Page.match(dropped)).toBeUndefined();
    expect(Drop.match(dropped)).toEqual({});
    expect(Page.match(undefined)).toBeUndefined();
    expect(Page.match(result([]))).toBeUndefined();
  });

  test("is compares decision and reason", () => {
    expect(Page.reason("sustained").is(paged)).toBe(true);
    expect(Page.reason("critical_alert").is(paged)).toBe(false);
    expect(Notify.reason("unrouted").is(paged)).toBe(false);
    expect(Notify.reason("unrouted").is(undefined)).toBe(false);
  });

  test("a misspelled reason is a type error and throws with a hint", () => {
    // @ts-expect-error: page has no reason sustaned
    expect(() => Page.reason("sustaned")).toThrow('decision page has no reason "sustaned"');
    try {
      // @ts-expect-error: page has no reason sustaned
      Page.reason("sustaned");
    } catch (err) {
      expect((err as SigilError).help).toBe('did you mean "sustained"? page declares: critical_alert, sustained');
    }
  });

  test("an unranked collecting kind's result reads with matchAll only", () => {
    const res = markResult(
      result(
        [
          { decision: "a", reason: "r1" },
          { decision: "b", reason: "x", payload: { weight: 2 } },
          { decision: "a", reason: "r2", policy: "p", position: "p.sigil:3:3" },
        ],
        { collect: true },
      ),
      { collect: true, ranked: false },
    );
    expect(() => A.match(res)).toThrow("match on a collecting kind's result");
    expect(() => A.reason("r1").is(res)).toThrow("is on a collecting kind's result");
    expect(A.matchAll(res)).toEqual([
      { payload: {}, reason: "r1", policy: undefined, position: undefined },
      { payload: {}, reason: "r2", policy: "p", position: "p.sigil:3:3" },
    ]);
    // Without the kind's mark, `collect` alone makes it unranked.
    expect(() => A.match(result([], { collect: true }))).toThrow(SigilError);
  });

  test("a ranked collecting kind's single top entry matches", () => {
    const res = markResult(result([{ decision: "a", reason: "r2" }], { collect: true }), { collect: true, ranked: true });
    expect(A.match(res)).toEqual({});
    expect(A.reason("r2").is(res)).toBe(true);
    void Collecting;
  });

  test("names and reasons read back", () => {
    expect(Page.name).toBe("page");
    expect(Page.reasons).toEqual(["critical_alert", "sustained"]);
    expect(`${Page.reason("sustained")}`).toBe("page.sustained");
    expect(`${Page}`).toBe("page");
  });
});

describe("types", () => {
  test("InputOf is what a host passes", () => {
    const input: InputOf<typeof AlertRouting> = {
      alert: { name: "HighErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "12m" },
      team: { name: "checkout", oncall: "oncall-checkout", channel: "#checkout" },
    };
    // @ts-expect-error: loud isn't a Severity
    const bad: InputOf<typeof AlertRouting>["alert"]["severity"] = "loud";
    expect(input.alert.severity).toBe("critical");
    void bad;
  });

  test("optional fields may be left out, and timestamps take a Date", () => {
    const K = defineKind("Opt", {
      version: 1,
      inputs: { at: t.timestamp, who: t.optional(t.string), tags: t.list(t.string) },
      decisions: [Ok],
      default: Ok.reason("yes"),
    });
    const a: InputOf<typeof K> = { at: new Date(0), tags: [] };
    const b: InputOf<typeof K> = { at: "2026-09-30T12:00:00Z", who: null, tags: ["x"] };
    // @ts-expect-error: tags is required
    const c: InputOf<typeof K> = { at: new Date(0) };
    expect([a, b, c]).toHaveLength(3);
  });

  test("host function implementations are typed from the declaration", () => {
    fn([t.string, t.int], t.list(t.string), (s, n) => s.repeat(n).split(""));
    // @ts-expect-error: the result must be a list of strings
    fn([t.string], t.list(t.string), (s) => s.length);
    expect(true).toBe(true);
  });

  test("payload types come from the decision", () => {
    const payload = Page.match(undefined);
    const target: string | undefined = payload?.target;
    // @ts-expect-error: page has no channel
    void payload?.channel;
    expect(target).toBeUndefined();
  });
});
