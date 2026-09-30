import { describe, expect, test } from "bun:test";
import { errorLines, errorOf } from "./client";
import { locationSteps, payloadValue, relativeTime, shortFingerprint } from "./format";

describe("relativeTime", () => {
  const now = new Date("2026-01-02T12:00:00Z");
  test.each([
    ["2026-01-02T12:00:00Z", "just now"],
    ["2026-01-02T11:59:18Z", "42s ago"],
    ["2026-01-02T11:55:00Z", "5m ago"],
    ["2026-01-02T09:00:00Z", "3h ago"],
    ["2025-12-30T12:00:00Z", "2025-12-30"],
    ["not a time", "not a time"],
  ])("%s is %s", (iso, want) => {
    expect(relativeTime(iso, now)).toBe(want);
  });
});

describe("locationSteps", () => {
  test("splits the chain, outermost first, the rule last", () => {
    expect(locationSteps("teams/checkout/alerts.sigil:7:1 → platform/paging.sigil:8:3")).toEqual([
      { file: "teams/checkout/alerts.sigil", line: "7", column: "1", raw: "teams/checkout/alerts.sigil:7:1" },
      { file: "platform/paging.sigil", line: "8", column: "3", raw: "platform/paging.sigil:8:3" },
    ]);
  });

  test.each([
    ["", 0],
    ["no-position", 1],
  ])("%p has %d steps", (loc, n) => {
    expect(locationSteps(loc)).toHaveLength(n);
  });
});

test.each<[unknown, string]>([
  ["#alerts", "#alerts"],
  [3, "3"],
  [["a", "b"], '["a","b"]'],
])("payloadValue(%p) is %s", (v, want) => {
  expect(payloadValue(v)).toBe(want);
});

test("shortFingerprint shortens long fingerprints only", () => {
  expect(shortFingerprint("0123456789abcdef")).toBe("0123456789ab…");
  expect(shortFingerprint("abc")).toBe("abc");
});

describe("errorOf", () => {
  test.each<[string, number, unknown, string]>([
    ["an ErrorEnvelope", 404, { error: { message: "no team search", advice: ["add it"] } }, "no team search"],
    ["a webhook result's plain error", 200, { error: "plain" }, "plain"],
    ["a body without an error", 502, null, "the server answered 502"],
  ])("reads %s", (_, status, body, message) => {
    expect(errorOf(status, body).message).toBe(message);
  });
});

test("errorLines walks the causes, outermost first", () => {
  expect(errorLines({ message: "reload failed", advice: ["fix it"], cause: { message: "parse error" } })).toEqual([
    { message: "reload failed", advice: ["fix it"] },
    { message: "parse error", advice: [] },
  ]);
});
