import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { type Batch, UNROUTED } from "../fixture/cases";
import { routePath } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { expectBatch, expectFallback } from "../fixture/expect";
import { METRIC_ALERTS_ROUTED, METRIC_DECISIONS, METRIC_EVAL_ERRORS, METRIC_REQUESTS } from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_LATENCY,
  DECISION_NOTIFY,
  DECISION_PAGE,
  DEFAULT_CHANNEL,
  firing,
  firingAlert,
  MINUTE,
  newWebhook,
  PAYMENTS_ONCALL,
  REASON_CRITICAL_ALERT,
  REASON_UNROUTED,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  STATUS_FAILED,
  STATUS_ROUTED,
  slowPolicy,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "../fixture/requests";
import { CHECKOUT_POLICY, closeEnvs, type Env, newEnv, ROUTE_TEMPLATE, releaseTelemetry, SPAN_ROUTE } from "./env";

/** The size of the list the slow policy walks: a hundred million steps, well over a second unbounded. */
const SLOW_NAMES = 10_000;

/** SpanStatusCode.UNSET and ERROR. */
const SPAN_UNSET = 0;
const SPAN_ERROR = 2;

afterEach(closeEnvs);
afterAll(releaseTelemetry);

// The shipped policies decide in microseconds whatever the alert, so every
// spec here swaps checkout's policy for one that doesn't.
async function slowEnv(evaluationTimeoutMs?: number): Promise<Env> {
  const e = await newEnv({ evaluationTimeoutMs });
  e.writeTeamFile(CHECKOUT_POLICY, slowPolicy(SLOW_NAMES));
  await e.reloadOK();
  e.resetSpans();
  return e;
}

describe("An evaluation that doesn't finish", () => {
  describe("when it runs past the evaluation timeout", () => {
    test("answers 503 with the fallback, dispatches it, and counts a timeout, not a decision", async () => {
      const e = await slowEnv(100);

      const start = performance.now();
      const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING));
      // Unbounded, the policy takes well over a second.
      expect(performance.now() - start).toBeLessThan(1_000);
      expectFallback(TEAM_CHECKOUT, 503, a, a.out, "evaluation timeout");

      // The fallback still goes out, so the alert isn't lost.
      expect(e.notifications()).toEqual([
        expect.objectContaining({ decision: DECISION_NOTIFY, reason: REASON_UNROUTED, channel: DEFAULT_CHANNEL }),
      ]);

      const families = await e.families();
      expect(families.value(METRIC_EVAL_ERRORS, { team: TEAM_CHECKOUT, kind: "timeout" })).toBe(1);
      expect(families.count(METRIC_EVAL_ERRORS)).toBe(1);
      expect(families.count(METRIC_DECISIONS)).toBe(0);
      expect(families.value(METRIC_ALERTS_ROUTED, { team: TEAM_CHECKOUT, outcome: STATUS_FAILED })).toBe(1);

      // A 503 is alertrouter's failure, so both the route span and the
      // request span say so.
      expect((await e.waitForSpan(SPAN_ROUTE)).status.code).toBe(SPAN_ERROR);
      expect((await e.serverSpan()).status.code).toBe(SPAN_ERROR);
      await eventually(async () => {
        const f = await e.families();
        expect(f.value(METRIC_REQUESTS, { code: "503", method: "POST", route: ROUTE_TEMPLATE })).toBe(1);
      });
    });

    test("fails the webhook's alert alone and still answers the batch with 200", async () => {
      const e = await slowEnv(100);
      const minuteAgo = new Date(Date.now() - MINUTE);
      const batch: Batch = {
        webhook: newWebhook(
          firing("slow", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), minuteAgo),
          firing("fast", alertLabels(TEAM_PAYMENTS, "PaymentsErrorRate", SEVERITY_CRITICAL), minuteAgo),
        ),
        results: [
          { fingerprint: "slow", status: STATUS_FAILED, team: TEAM_CHECKOUT, want: UNROUTED },
          {
            fingerprint: "fast",
            status: STATUS_ROUTED,
            team: TEAM_PAYMENTS,
            want: { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: PAYMENTS_ONCALL },
          },
        ],
      };

      const a = await e.client.webhook(batch.webhook);
      expectBatch(batch, a, a.out);
      expect(a.out.results[0]?.error).toContain("evaluation timeout");
      expect(e.notifications()).toHaveLength(2);
    });
  });

  test("answers 499 and counts no error when the client leaves during the evaluation", async () => {
    // The server's own timeout is a second; the client gives up long before,
    // so it's the client's cancellation that stops the evaluation.
    const e = await slowEnv(1_000);

    await e.abandon(routePath(TEAM_CHECKOUT), firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING), 100);

    await eventually(
      async () => {
        const f = await e.families();
        expect(f.value(METRIC_REQUESTS, { code: "499", method: "POST", route: ROUTE_TEMPLATE })).toBe(1);
      },
      { timeoutMs: 5_000 },
    );

    // Nothing failed, in the policy or in alertrouter, and no one is left to
    // tell: no evaluation error, no decision, no notification, and no span
    // marked as an error, though the route span records why it stopped.
    const families = await e.families();
    expect(families.count(METRIC_EVAL_ERRORS)).toBe(0);
    expect(families.count(METRIC_DECISIONS)).toBe(0);
    expect(e.notifications()).toEqual([]);

    const route = await e.waitForSpan(SPAN_ROUTE);
    expect(route.status.code).toBe(SPAN_UNSET);
    expect(route.events.map((ev) => ev.name)).toContain("exception");
    expect((await e.serverSpan()).status.code).toBe(SPAN_UNSET);
  }, 20_000);
});

// A webhook must be answered well inside the HTTP server's timeout, or
// Alertmanager gives up and retries the whole group. The batch gets a
// deadline; alerts it doesn't reach in time go the way of a failed
// evaluation. The Go service had none; this is the lead's addendum, item 2.
describe("A webhook that runs past its batch deadline", () => {
  test("answers in time, and routes the alerts it didn't reach through the fallback, still paging", async () => {
    const e = await newEnv({ evaluationTimeoutMs: 100, batchTimeoutMs: 250 });
    e.writeTeamFile(CHECKOUT_POLICY, slowPolicy(SLOW_NAMES));
    await e.reloadOK();

    const minuteAgo = new Date(Date.now() - MINUTE);
    const alerts = Array.from({ length: 6 }, (_, i) =>
      firing(`slow-${i}`, alertLabels(TEAM_CHECKOUT, "CheckoutErrorRate", SEVERITY_CRITICAL), minuteAgo),
    );

    const start = performance.now();
    const a = await e.client.webhook(newWebhook(...alerts));
    // Six evaluations of 100 ms each would take 600 ms; the deadline cuts
    // them off at 250.
    expect(performance.now() - start).toBeLessThan(500);
    expect(a.status).toBe(200);
    expect(a.out.results).toHaveLength(6);
    for (const r of a.out.results) {
      expect(r).toMatchObject({ status: STATUS_FAILED, decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT });
    }
    expect(a.out.results.some((r) => r.error?.includes("batch deadline"))).toBe(true);
    expect(e.notifications()).toHaveLength(6);

    // Every alert it didn't decide counts as a timeout, evaluated or not.
    expect((await e.families()).value(METRIC_EVAL_ERRORS, { team: TEAM_CHECKOUT, kind: "timeout" })).toBe(6);
  });
});
