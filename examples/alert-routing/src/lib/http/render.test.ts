import { describe, expect, test } from "bun:test";
import type { EvalFailure, EvalResult } from "@spechtlabs/sigil";

import { candidateResult, classify, fallbackResponse, ordered, routeResponse, traceCandidates } from "./render";

const PAGE = {
  decision: "page",
  reason: "critical_alert",
  policy: "platform.paging",
  position: "platform/paging.sigil:8:3",
};

function result(over: Partial<EvalResult> = {}): EvalResult {
  return {
    policy: "checkout.alerts",
    decision: "page",
    reason: "critical_alert",
    payload: { target: "checkout-primary" },
    outcome: [{ ...PAGE, payload: { target: "checkout-primary" } }],
    trace: [
      {
        ...PAGE,
        chain: ["checkout/alerts.sigil:7:1"],
        conditions: ["not pre_production and alert.severity == critical"],
        payload: { target: "checkout-primary" },
        outcome: true,
      },
    ],
    ...over,
  };
}

describe("routeResponse", () => {
  test("reads the winner's payload through the decision handles", () => {
    expect(routeResponse("checkout", "checkout.alerts", result())).toEqual({
      team: "checkout",
      policy: "checkout.alerts",
      decision: "page",
      reason: "critical_alert",
      target: "checkout-primary",
      trace: [
        {
          decision: "page",
          reason: "critical_alert",
          policy: "platform.paging",
          location: "checkout/alerts.sigil:7:1 → platform/paging.sigil:8:3",
          conditions: ["not pre_production and alert.severity == critical"],
          payload: { target: "checkout-primary" },
          winner: true,
        },
      ],
    });
  });

  test("a notify carries its channel, #alerts when the payload leaves it empty", () => {
    const res = result({
      decision: "notify",
      reason: "unrouted",
      outcome: [{ decision: "notify", reason: "unrouted", payload: { channel: "" } }],
      trace: [],
    });
    expect(routeResponse("checkout", "checkout.alerts", res).channel).toBe("#alerts");
  });
});

test("each outcome entry claims one candidate, the first that matches", () => {
  const twice = result({ trace: [{ ...PAGE }, { ...PAGE }] });
  expect(traceCandidates(twice).map((c) => c.winner)).toEqual([true, false]);
});

test("a candidate without conditions or payload renders an empty payload and no conditions", () => {
  expect(candidateResult({ decision: "drop", reason: "muted" }, false)).toEqual({
    decision: "drop",
    reason: "muted",
    policy: "",
    location: "",
    payload: {},
    winner: false,
  });
});

test("fallbackResponse is the kind's default, with the team only when one owns it and the policy only when it ran", () => {
  expect(fallbackResponse("")).toEqual({ decision: "notify", reason: "unrouted", channel: "#alerts", trace: [] });
  expect(Object.keys(fallbackResponse("checkout"))).toEqual(["team", "decision", "reason", "channel", "trace"]);
  expect(Object.keys(fallbackResponse("checkout", "checkout.alerts")).slice(0, 2)).toEqual(["team", "policy"]);
});

test("ordered puts the fields in the Go service's order", () => {
  expect(
    Object.keys(
      ordered({ trace: [], target: "x", error: { message: "m" }, reason: "r", decision: "d", policy: "p", team: "t" }),
    ),
  ).toEqual(["team", "policy", "decision", "reason", "target", "trace", "error"]);
});

describe("classify", () => {
  const fallback = "the fallback advice";
  const res = result({ trace: [] });

  test.each([
    [
      "an input assert",
      { kind: "assertion", phase: "input", asserts: [{ reason: "a", policy: "checkout.alerts", position: "p:1:1" }] },
      422,
      "assertion",
      "the alert fails checkout.alerts's asserts: a",
    ],
    [
      "an outcome assert",
      { kind: "assertion", phase: "outcome", asserts: [{ reason: "b", policy: "platform.x", position: "p:2:1" }] },
      500,
      "assertion",
      "the outcome of checkout.alerts fails its asserts: b",
    ],
    [
      "a runtime error",
      { kind: "runtime", message: "checkout/alerts.sigil:9:6: index 1 out of range" },
      500,
      "runtime",
      "checkout.alerts can't be evaluated against this alert: index 1 out of range at checkout/alerts.sigil:9:6",
    ],
    [
      "a conflict",
      { kind: "conflict", message: "two pages", candidates: [] },
      500,
      "conflict",
      "checkout.alerts produced decisions that can't stand together: two pages",
    ],
    [
      "a timeout",
      { kind: "canceled", message: "stopped" },
      503,
      "timeout",
      "checkout.alerts wasn't decided within alertrouter's evaluation timeout",
    ],
  ] as const)("%s", (_name, over, status, kind, message) => {
    const failure = { message: "", help: "", ...over } as EvalFailure;
    const f = classify("checkout.alerts", failure, res, fallback);
    expect(f.status).toBe(status);
    expect(f.kind).toBe(kind);
    expect(f.error.message).toBe(message);
    expect(f.error.advice[0]).toBe(fallback);
  });

  test("an assert's own policy is kept", () => {
    const f = classify(
      "checkout.alerts",
      {
        kind: "assertion",
        phase: "outcome",
        message: "",
        help: "",
        asserts: [{ reason: "b", policy: "platform.x", position: "p:2:1", cause: "boom" }],
      },
      res,
      fallback,
    );
    expect(f.asserts).toEqual([{ reason: "b", policy: "platform.x", location: "p:2:1", cause: "boom" }]);
  });

  test("without a phase, an assert that ran after a rule fired is an outcome assert", () => {
    const failure = {
      kind: "assertion",
      message: "",
      help: "",
      asserts: [{ reason: "a", policy: "", position: "p:1:1" }],
    } as EvalFailure;
    expect(classify("checkout.alerts", failure, result(), fallback).status).toBe(500);
    expect(classify("checkout.alerts", failure, res, fallback).status).toBe(422);
    expect(classify("checkout.alerts", failure, res, fallback).asserts?.[0]?.policy).toBe("checkout.alerts");
  });
});
