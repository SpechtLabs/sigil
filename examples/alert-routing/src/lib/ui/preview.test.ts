import { describe, expect, test } from "bun:test";
import type { EvalEntry, EvalResult } from "@spechtlabs/sigil";
import { DEFAULT_VALUES } from "./alert-form";
import type { RouteResponse } from "./api-types";
import {
  candidateFromEntry,
  compareOutcomes,
  destinationOf,
  platformOverride,
  previewBundle,
  routeResponseFromAlertResult,
  routeResponseFromEval,
  routingAlert,
  routingInput,
  unownedResponse,
} from "./preview";

const team = { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" };

describe("candidateFromEntry", () => {
  test.each<[string, EvalEntry, string]>([
    [
      "the rule itself",
      {
        decision: "notify",
        reason: "routine",
        policy: "checkout.alerts",
        position: "teams/checkout/alerts.sigil:13:3",
      },
      "teams/checkout/alerts.sigil:13:3",
    ],
    [
      "a rule reached through an invocation, outermost first",
      {
        decision: "page",
        reason: "critical_alert",
        policy: "platform.paging",
        position: "platform/paging.sigil:8:3",
        chain: ["teams/checkout/alerts.sigil:7:1"],
      },
      "teams/checkout/alerts.sigil:7:1 → platform/paging.sigil:8:3",
    ],
    ["the default, which has no position", { decision: "notify", reason: "unrouted" }, ""],
  ])("joins the location of %s like Go's Candidate.Location", (_, entry, location) => {
    expect(candidateFromEntry(entry).location).toBe(location);
  });

  test("marks the outcome's entries as winners and keeps conditions and payload", () => {
    expect(
      candidateFromEntry({
        decision: "page",
        reason: "critical_alert",
        policy: "platform.paging",
        position: "platform/paging.sigil:8:3",
        conditions: ["in_production and alert.severity == critical"],
        payload: { target: "checkout-primary" },
        outcome: true,
      }),
    ).toEqual({
      decision: "page",
      reason: "critical_alert",
      policy: "platform.paging",
      location: "platform/paging.sigil:8:3",
      conditions: ["in_production and alert.severity == critical"],
      payload: { target: "checkout-primary" },
      winner: true,
    });
  });

  test("leaves empty conditions out and gives an empty payload, as the server does", () => {
    const c = candidateFromEntry({ decision: "drop", reason: "muted", conditions: [] });
    expect(c.conditions).toBeUndefined();
    expect(c.payload).toEqual({});
    expect(c.winner).toBe(false);
  });
});

describe("routeResponseFromEval", () => {
  const trace: EvalEntry[] = [
    { decision: "page", reason: "critical_alert", policy: "platform.paging", payload: { target: "x" }, outcome: true },
  ];

  test.each<[string, Partial<EvalResult>, Partial<RouteResponse>]>([
    [
      "a page carries its target",
      { decision: "page", reason: "critical_alert", payload: { target: "checkout-primary" } },
      { decision: "page", target: "checkout-primary" },
    ],
    [
      "a notify carries its channel",
      { decision: "notify", reason: "routine", payload: { channel: "#checkout-alerts" } },
      { decision: "notify", channel: "#checkout-alerts" },
    ],
    ["a drop carries neither", { decision: "drop", reason: "muted" }, { decision: "drop", reason: "muted" }],
  ])("%s", (_, res, expected) => {
    const out = routeResponseFromEval("checkout", { policy: "checkout.alerts", outcome: [], trace, ...res });
    expect(out).toMatchObject({ team: "checkout", policy: "checkout.alerts", ...expected });
    expect(out.trace).toHaveLength(1);
    if (expected.target === undefined) expect(out.target).toBeUndefined();
    if (expected.channel === undefined) expect(out.channel).toBeUndefined();
  });

  test("a failed evaluation keeps the fallback and explains the failure", () => {
    const out = routeResponseFromEval("checkout", {
      policy: "checkout.alerts",
      decision: "notify",
      reason: "unrouted",
      payload: { channel: "#alerts" },
      outcome: [],
      trace: [],
      error: {
        kind: "assertion",
        message: "an assert failed",
        help: "fix the input",
        asserts: [{ reason: "known_env", policy: "checkout.alerts", position: "a.sigil:3:1", cause: "boom" }],
        candidates: [{ decision: "page", reason: "sustained" }],
      },
    });
    expect(out).toMatchObject({
      decision: "notify",
      reason: "unrouted",
      channel: "#alerts",
      error: { message: "an assert failed", advice: ["fix the input"] },
      asserts: [{ reason: "known_env", policy: "checkout.alerts", location: "a.sigil:3:1", cause: "boom" }],
      conflict: { candidates: [{ decision: "page", reason: "sustained", winner: false }] },
    });
  });
});

test("routingInput is the kind's input: the form's alert and the team", () => {
  expect(routingInput(routingAlert({ ...DEFAULT_VALUES, component: "payments" }), team)).toEqual({
    alert: {
      name: "CheckoutErrorRate",
      severity: "critical",
      labels: { env: "production", component: "payments" },
      firing_for: "2m",
    },
    team,
  });
});

test("unownedResponse is the server's fallback without a team or a policy", () => {
  expect(unownedResponse()).toEqual({
    decision: "notify",
    reason: "unrouted",
    channel: "#alerts",
    trace: [],
  });
});

describe("platformOverride", () => {
  const cand = (decision: string, reason: string, payload: Record<string, unknown> = {}) => ({
    decision,
    reason,
    policy: "checkout.alerts",
    location: "checkout/alerts.sigil:12:3",
    payload,
    winner: true,
  });
  const page: RouteResponse = {
    policy: "checkout.alerts",
    decision: "page",
    reason: "critical_alert",
    target: "checkout-primary",
    trace: [],
  };

  test.each<[string, RouteResponse, string | undefined]>([
    [
      "the team's own page",
      { ...page, trace: [cand("page", "critical_alert", { target: "checkout-primary" })] },
      undefined,
    ],
    ["a notify replaced by the platform's page", { ...page, trace: [cand("notify", "routine")] }, "notify"],
    ["a page to nobody replaced", { ...page, trace: [cand("page", "critical_alert", { target: "nobody" })] }, "page"],
    ["a page without a trace, such as an invalid alert's", page, undefined],
    ["no page at all", { ...page, decision: "drop", reason: "muted", trace: [cand("drop", "muted")] }, undefined],
  ])("%s", (_, r, decision) => {
    expect(platformOverride(r)?.decision).toBe(decision as string);
  });
});

describe("routeResponseFromAlertResult", () => {
  test("a routed alert keeps its route fields", () => {
    expect(
      routeResponseFromAlertResult({
        fingerprint: "f",
        alertname: "A",
        status: "routed",
        team: "checkout",
        policy: "checkout.alerts",
        decision: "page",
        reason: "critical_alert",
        target: "checkout-primary",
        trace: [],
      }),
    ).toEqual({
      team: "checkout",
      policy: "checkout.alerts",
      decision: "page",
      reason: "critical_alert",
      target: "checkout-primary",
      trace: [],
    });
  });

  test("an unowned alert's plain error becomes an ErrorResponse", () => {
    expect(
      routeResponseFromAlertResult({
        fingerprint: "f",
        alertname: "A",
        status: "unowned",
        error: "no team owns it",
        policy: "",
        decision: "notify",
        reason: "unrouted",
        channel: "#alerts",
        trace: [],
      }),
    ).toEqual({
      policy: "",
      decision: "notify",
      reason: "unrouted",
      channel: "#alerts",
      trace: [],
      error: { message: "no team owns it" },
    });
  });
});

describe("destinationOf and compareOutcomes", () => {
  const page: RouteResponse = { policy: "p", decision: "page", reason: "sustained", target: "oncall", trace: [] };

  test.each<[Partial<RouteResponse>, string]>([
    [{ target: "oncall" }, "oncall"],
    [{ channel: "#c" }, "#c"],
    [{}, "-"],
  ])("%p goes to %s", (r, d) => {
    expect(destinationOf(r)).toBe(d);
  });

  test.each<[string, Partial<RouteResponse>, string[]]>([
    ["the same outcome agrees", {}, []],
    ["another reason", { reason: "critical_alert" }, ["reason"]],
    [
      "another decision and destination",
      { decision: "notify", target: undefined, channel: "#c" },
      ["decision", "destination"],
    ],
  ])("%s", (_, change, differences) => {
    const got = compareOutcomes(page, { ...page, ...change });
    expect(got.differences).toEqual(differences as typeof got.differences);
    expect(got.agrees).toBe(differences.length === 0);
  });
});

test("previewBundle compiles the team bundle with the platform's documents as trusted", () => {
  const kind = { path: "alert_routing.sigil", source: "kind" };
  const platform = [{ path: "platform/paging.sigil", source: "p" }];
  const teams = [{ path: "checkout/alerts.sigil", source: "t" }];
  expect(
    previewBundle({
      kind,
      platform,
      teams,
      required: "platform.paging",
      roots: [{ team: "checkout", policy: "checkout.alerts" }],
      fingerprint: "f",
      source: "embedded",
      loaded_at: "2026-01-01T00:00:00Z",
    }),
  ).toEqual({
    fingerprint: "f",
    files: [kind, ...teams],
    trusted: platform,
    required: "platform.paging",
    policies: [{ team: "checkout", policy: "checkout.alerts" }],
  });
});
