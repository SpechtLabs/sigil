// Alertmanager retries only a 5xx, so a 4xx drops the whole group for good.
// alertrouter refuses only a body that isn't a webhook at all; an alert it
// can't read is invalid, and a batch past the alert limit is routed as far
// as the limit and sends the rest to the fallback. The Go service refused
// all three with 400; this is the lead's addendum, item 4.
import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { UNROUTED } from "../fixture/cases";
import { decode, expectStatus, PATH_ALERTS, routePath } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { expectBatch } from "../fixture/expect";
import { METRIC_ALERTS_ROUTED } from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  CHECKOUT_ONCALL,
  DECISION_PAGE,
  firing,
  firingAlert,
  label,
  MINUTE,
  newWebhook,
  REASON_CRITICAL_ALERT,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  STATUS_FAILED,
  STATUS_INVALID,
  STATUS_ROUTED,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import type { ErrorResponse } from "../fixture/wire";
import { closeEnvs, newEnv, releaseTelemetry, SPAN_ROUTE } from "./env";

/** The most alerts of one webhook alertrouter evaluates. */
const MAX_ALERTS = 1000;
/** The largest body alertrouter reads. */
const BODY_LIMIT = 4 << 20;

const METRIC_TRUNCATED = "alertrouter_alerts_truncated_total";

afterEach(closeEnvs);
afterAll(releaseTelemetry);

describe("A webhook alertrouter can't route as a whole", () => {
  test("routes an alert whose status it doesn't know as invalid, and the rest as usual", async () => {
    const e = await newEnv();
    const minuteAgo = new Date(Date.now() - MINUTE);
    const pending = {
      ...firing("pending", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), minuteAgo),
      status: "pending",
    };
    const critical = firing("critical", alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo);

    const a = await e.client.webhook(newWebhook(pending, critical));
    expectBatch(
      {
        webhook: newWebhook(pending, critical),
        results: [
          { fingerprint: "pending", status: STATUS_INVALID, team: TEAM_CHECKOUT, want: UNROUTED },
          {
            fingerprint: "critical",
            status: STATUS_ROUTED,
            team: TEAM_CHECKOUT,
            want: { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: CHECKOUT_ONCALL },
          },
        ],
      },
      a,
      a.out,
    );
    expect(a.out.results[0]?.error).toContain("pending");
    expect(e.notifications()).toHaveLength(2);
  });

  test("routes the first alerts up to the limit and sends the rest to the fallback, still paging a critical one", async () => {
    const e = await newEnv();
    const minuteAgo = new Date(Date.now() - MINUTE);
    const alerts = Array.from({ length: MAX_ALERTS + 2 }, (_, i) =>
      firing(`a${i}`, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
    );

    const a = await e.client.webhook(newWebhook(...alerts));
    expectStatus(a, 200);
    expect(a.out.received).toBe(MAX_ALERTS + 2);
    expect(a.out.results).toHaveLength(MAX_ALERTS + 2);
    expect(a.out.routed).toBe(MAX_ALERTS);
    expect(a.out.results.slice(0, MAX_ALERTS).every((r) => r.status === STATUS_ROUTED)).toBe(true);

    // Past the limit nothing was evaluated, but platform.paging still pages
    // a critical alert, so no page is lost to a large batch.
    for (const r of a.out.results.slice(MAX_ALERTS)) {
      expect(r).toMatchObject({ status: STATUS_FAILED, decision: DECISION_PAGE, target: CHECKOUT_ONCALL });
      expect(r.error).toBeDefined();
    }
    expect(e.notifications()).toHaveLength(MAX_ALERTS + 2);

    const families = await e.families();
    expect(families.value(METRIC_TRUNCATED, { by: "alertrouter" })).toBe(2);
    expect(families.value(METRIC_TRUNCATED, { by: "alertmanager" })).toBe(0);
    expect(families.value(METRIC_ALERTS_ROUTED, { team: TEAM_CHECKOUT, outcome: STATUS_FAILED })).toBe(2);

    // The alerts past the limit are delivered, but a flood of them mustn't
    // flood the telemetry too: no span or "alert routed" line of their own,
    // one warning for the batch.
    await eventually(() => expect(e.spansNamed(SPAN_ROUTE)).toHaveLength(MAX_ALERTS));
    expect(e.logs().filter((l) => l.msg === "alert routed")).toHaveLength(MAX_ALERTS);
    expect(
      e.logs().filter((l) => l.msg === "the webhook carries more alerts than alertrouter evaluates at once"),
    ).toHaveLength(1);
  });

  // A runaway rule can put thousands of alerts in one group. Past the limit
  // nothing is evaluated by the team, but every critical still pages, and
  // cheaply: the batch answers well inside Alertmanager's timeout.
  test("pages every critical of a batch far over the limit, and answers quickly", async () => {
    const e = await newEnv();
    const minuteAgo = new Date(Date.now() - MINUTE);
    const n = 5 * MAX_ALERTS;
    const alerts = Array.from({ length: n }, (_, i) =>
      firing(`c${i}`, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
    );

    const start = performance.now();
    const a = await e.client.webhook(newWebhook(...alerts));
    const took = performance.now() - start;

    expectStatus(a, 200);
    expect(a.out.results).toHaveLength(n);
    expect(a.out.results.every((r) => r.decision === DECISION_PAGE && r.target === CHECKOUT_ONCALL)).toBe(true);
    expect(a.out.routed).toBe(MAX_ALERTS);
    expect(e.notifications()).toHaveLength(n);
    // About half a second on a laptop; the bound leaves room for a busy CI runner.
    expect(took).toBeLessThan(5_000);
  }, 30_000);

  test("counts the alerts Alertmanager itself left out of a truncated group", async () => {
    const e = await newEnv();
    const webhook = newWebhook(
      firing("only", alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), new Date(Date.now() - MINUTE)),
    );
    webhook.truncatedAlerts = 7;

    expectStatus(await e.client.webhook(webhook), 200);
    const families = await e.families();
    expect(families.value(METRIC_TRUNCATED, { by: "alertmanager" })).toBe(7);
    expect(families.value(METRIC_TRUNCATED, { by: "alertrouter" })).toBe(0);
    expect(e.logs().find((l) => l.msg === "Alertmanager truncated the webhook")).toMatchObject({
      level: "warn",
      fields: { truncated_alerts: 7 },
    });
  });
});

describe("The body limit", () => {
  test("reads a webhook of a few MiB, which real alert groups reach", async () => {
    const e = await newEnv();
    const alert = firing(
      "big",
      alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING),
      new Date(Date.now() - MINUTE),
    );
    alert.annotations.description = "x".repeat(2 << 20);

    const a = await e.client.webhook(newWebhook(alert));
    expectStatus(a, 200);
    expect(a.out.routed).toBe(1);
  });

  test("answers 413 for a webhook over the limit", async () => {
    const e = await newEnv();
    const alert = firing("big", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), new Date());
    alert.annotations.description = "x".repeat(BODY_LIMIT);

    const a = await e.client.postJSON(PATH_ALERTS, newWebhook(alert));
    expectStatus(a, 413);
    expect(decode<ErrorResponse>(a).error).toBeDefined();
    expect(e.notifications()).toEqual([]);
  });

  test("answers 413 for a single alert over the limit", async () => {
    const e = await newEnv();
    const big = firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, label("padding", "x".repeat(BODY_LIMIT)));

    const a = await e.client.postJSON(routePath(TEAM_CHECKOUT), big);
    expectStatus(a, 413);
    expect(decode<ErrorResponse>(a).error).toBeDefined();
  });
});
