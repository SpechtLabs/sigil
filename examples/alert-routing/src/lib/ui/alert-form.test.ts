import { describe, expect, test } from "bun:test";
import {
  type AlertFormValues,
  alertLabels,
  DEFAULT_VALUES,
  NO_ENV,
  NO_TEAM,
  parseDurationMs,
  randomFingerprint,
  routeRequest,
  validate,
  webhookBody,
} from "./alert-form";

describe("parseDurationMs", () => {
  test.each([
    ["0", 0],
    ["90s", 90_000],
    ["12m", 720_000],
    ["1h30m", 5_400_000],
    ["1.5h", 5_400_000],
    ["250ms", 250],
    ["1m0.5s", 60_500],
    ["2us", 0.002],
    ["2µs", 0.002],
  ])("%s is %d ms", (s, ms) => {
    expect(parseDurationMs(s)).toBeCloseTo(ms, 6);
  });

  test.each(["", "12", "m", "-5m", "5 m", "5d", "1h 30m", "abc"])("%p isn't a duration", (s) => {
    expect(parseDurationMs(s)).toBeUndefined();
  });
});

describe("validate", () => {
  const teams = ["checkout", "payments"];
  const valid = DEFAULT_VALUES;

  test("accepts the defaults", () => {
    expect(validate(valid, teams)).toEqual({});
  });

  test.each<[string, Partial<AlertFormValues>, keyof AlertFormValues]>([
    ["an empty name", { name: "  " }, "name"],
    ["an unknown severity", { severity: "urgent" as AlertFormValues["severity"] }, "severity"],
    ["a firing_for that isn't a duration", { firingFor: "ten minutes" }, "firingFor"],
    ["a team not in the directory", { team: "search" }, "team"],
    ["no team on the route endpoint", { team: NO_TEAM, mode: "route" }, "team"],
  ])("refuses %s", (_, change, field) => {
    const errors = validate({ ...valid, ...change }, teams);
    expect(Object.keys(errors)).toEqual([field]);
  });

  test("accepts no team for a webhook, which the router answers as unowned", () => {
    expect(validate({ ...valid, mode: "webhook", team: NO_TEAM }, teams)).toEqual({});
  });
});

describe("alertLabels", () => {
  test.each<[string, Partial<AlertFormValues>, Record<string, string>]>([
    ["route: env only", {}, { env: "production" }],
    ["route: env and component", { component: "payments" }, { env: "production", component: "payments" }],
    ["route: no env label at all", { env: NO_ENV }, {}],
    [
      "webhook: name, severity and team travel as labels",
      { mode: "webhook" },
      { alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" },
    ],
    [
      "webhook without a team has no team label",
      { mode: "webhook", team: NO_TEAM },
      { alertname: "CheckoutErrorRate", severity: "critical", env: "production" },
    ],
  ])("%s", (_, change, labels) => {
    expect(alertLabels({ ...DEFAULT_VALUES, ...change })).toEqual(labels);
  });
});

test("routeRequest is the route endpoint's body", () => {
  expect(routeRequest({ ...DEFAULT_VALUES, severity: "warning", firingFor: "12m", component: "api" })).toEqual({
    alert: {
      name: "CheckoutErrorRate",
      severity: "warning",
      labels: { env: "production", component: "api" },
      firing_for: "12m",
    },
  });
});

test("webhookBody starts the alert firingFor before now", () => {
  const now = new Date("2026-01-01T01:00:00Z");
  const body = webhookBody({ ...DEFAULT_VALUES, mode: "webhook", firingFor: "12m" }, now, "0123456789abcdef");
  expect(body).toMatchObject({ version: "4", status: "firing", receiver: "alertrouter" });
  const alerts = body.alerts as Record<string, unknown>[];
  expect(alerts).toHaveLength(1);
  expect(alerts[0]).toMatchObject({
    status: "firing",
    startsAt: "2026-01-01T00:48:00.000Z",
    fingerprint: "0123456789abcdef",
    labels: { alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" },
  });
});

test("randomFingerprint has Alertmanager's 16 hex digits", () => {
  let i = 0;
  const steps = [0, 0.5, 0.999];
  expect(randomFingerprint(() => steps[i++ % steps.length] ?? 0)).toMatch(/^[0-9a-f]{16}$/);
  expect(randomFingerprint()).toMatch(/^[0-9a-f]{16}$/);
});
