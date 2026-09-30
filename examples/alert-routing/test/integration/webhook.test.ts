import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { type Batch, firingCount, mixedBatch, type Route, UNROUTED, webhookBadRequestCases } from "../fixture/cases";
import { decode, expectStatus, PATH_ALERTS } from "../fixture/client";
import { expectBatch } from "../fixture/expect";
import { METRIC_ALERTS_RECEIVED } from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_CHANNEL,
  CHECKOUT_LATENCY,
  CHECKOUT_ONCALL,
  DECISION_NOTIFY,
  DECISION_PAGE,
  DEFAULT_CHANNEL,
  firing,
  MINUTE,
  newWebhook,
  PAYMENTS_ONCALL,
  REASON_CRITICAL_ALERT,
  REASON_ROUTINE,
  REASON_SUSTAINED,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  STATUS_FAILED,
  STATUS_RESOLVED,
  STATUS_ROUTED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "../fixture/requests";
import { type ErrorResponse, result } from "../fixture/wire";
import { CHECKOUT_RULES, closeEnvs, conflictingRule, FixedClock, failingRule, newEnv, releaseTelemetry } from "./env";

afterEach(closeEnvs);
afterAll(releaseTelemetry);

describe("Receiving an Alertmanager webhook", () => {
  test("routes every firing alert, acknowledges the resolved one and loses none", async () => {
    const e = await newEnv();
    const batch = mixedBatch(new Date());

    const a = await e.client.webhook(batch.webhook);
    expectBatch(batch, a, a.out);
    expect(result(a.out, "unknown-team")?.error).toContain(`"marketing"`);

    // Exactly one notification per firing alert. Evaluations finish in a
    // worker pool, so they go out in completion order, not the webhook's.
    const got = new Map(e.notifications().map((n) => [n.fingerprint, n]));
    const firingResults = batch.results.filter((r) => r.status !== STATUS_RESOLVED);
    expect(e.notifications()).toHaveLength(firingCount(batch));
    expect([...got.keys()].sort()).toEqual(firingResults.map((r) => r.fingerprint).sort());
    firingResults.forEach((want) => {
      const n = got.get(want.fingerprint);
      expect({ decision: n?.decision, reason: n?.reason, target: n?.target ?? "", channel: n?.channel ?? "" }).toEqual({
        decision: want.want?.decision,
        reason: want.want?.reason,
        target: want.want?.target ?? "",
        channel: want.want?.channel ?? "",
      });
    });
  });

  // Alertmanager adds fields to its payload over time, and the router must
  // keep routing through such an upgrade, so the webhook ignores fields it
  // doesn't read, unlike the route endpoint.
  test("routes a payload with fields it doesn't know", async () => {
    const e = await newEnv();
    const body = `{"version": "4", "status": "firing", "newField": {"x": 1}, "alerts": [{"status": "firing", "fingerprint": "f1", "extra": true,
      "labels": {"alertname": "CheckoutErrorRate", "severity": "critical", "team": "checkout", "env": "production"},
      "startsAt": "2026-01-01T00:00:00Z"}]}`;
    const a = await e.client.webhookRaw(body);

    expectStatus(a, 200);
    expect(a.out.results).toHaveLength(1);
    expect(a.out.results[0]).toMatchObject({ status: STATUS_ROUTED, decision: DECISION_PAGE, target: CHECKOUT_ONCALL });
  });

  test("answers an empty batch with no results", async () => {
    const e = await newEnv();
    const a = await e.client.webhook(newWebhook());

    expectStatus(a, 200);
    expect(a.out).toEqual({ received: 0, routed: 0, results: [] });
  });

  // An alert that has fired for longer than a duration can hold, or whose
  // startsAt is garbage from a broken exporter, is still an alert: it must
  // page, not fail on an overflowing firing_for.
  test.each([
    ["the epoch", "1970-01-01T00:00:00Z"],
    ["year one", "0001-01-02T00:00:00Z"],
    ["three hundred years ago", "1726-09-28T15:00:00Z"],
  ])("pages a critical alert and a warning that started at %s", async (_, startsAt) => {
    const e = await newEnv();
    const critical = {
      ...firing("critical", alertLabels(TEAM_CHECKOUT, "CheckoutErrorRate", SEVERITY_CRITICAL), new Date()),
      startsAt,
    };
    const warning = {
      ...firing("warning", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), new Date()),
      startsAt,
    };

    const a = await e.client.webhook(newWebhook(critical, warning));
    expectBatch(
      {
        webhook: newWebhook(critical, warning),
        results: [
          {
            fingerprint: "critical",
            status: STATUS_ROUTED,
            team: TEAM_CHECKOUT,
            want: { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: CHECKOUT_ONCALL },
          },
          {
            fingerprint: "warning",
            status: STATUS_ROUTED,
            team: TEAM_CHECKOUT,
            want: { decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: CHECKOUT_ONCALL },
          },
        ],
      },
      a,
      a.out,
    );
    expect(e.notifications().map((n) => n.target)).toEqual([CHECKOUT_ONCALL, CHECKOUT_ONCALL]);
  });

  describe("when the server's clock says how long an alert has fired", () => {
    const now = new Date("2026-09-28T15:00:00Z");

    test.each([
      [
        "nine minutes is a fresh warning",
        9 * MINUTE,
        { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: CHECKOUT_CHANNEL },
      ],
      [
        "ten minutes is sustained",
        10 * MINUTE,
        { decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: CHECKOUT_ONCALL },
      ],
      // Alertmanager's clock may run ahead of the router's; that alert has
      // fired for no time at all, not a negative one.
      [
        "a start in the future is a fresh warning",
        -MINUTE,
        { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: CHECKOUT_CHANNEL },
      ],
    ] as [string, number, Route][])(
      "derives firing_for from startsAt, so checkout's ten minutes decide the page: %s",
      async (_, startedAgo, want) => {
        const e = await newEnv({ clock: new FixedClock(now) });
        const alert = firing(
          "latency",
          alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING),
          new Date(now.getTime() - startedAgo),
        );
        const a = await e.client.webhook(newWebhook(alert));
        expectBatch(
          {
            webhook: newWebhook(alert),
            results: [{ fingerprint: "latency", status: STATUS_ROUTED, team: TEAM_CHECKOUT, want }],
          },
          a,
          a.out,
        );
      },
    );
  });

  describe("when an alert's evaluation fails", () => {
    test.each([
      [
        "a conflict names both sides",
        conflictingRule,
        (r: NonNullable<ReturnType<typeof result>>) => {
          expect(r.conflict, "the result has no conflict block").toBeDefined();
          const channels = r.conflict?.candidates.map((c) => c.payload.channel).sort();
          expect(channels).toEqual(["#checkout-alerts", "#checkout-oncall"]);
          expect(r.error).toContain("checkout.alerts");
        },
      ],
      [
        "a runtime error says where",
        failingRule,
        (r: NonNullable<ReturnType<typeof result>>) => {
          expect(r.error).toContain("out of range");
        },
      ],
    ] as const)(
      "fails that alert alone, routes it with the fallback and routes the rest: %s",
      async (_, rules, check) => {
        const e = await newEnv();
        e.editCheckout(CHECKOUT_RULES, rules);
        await e.reloadOK();

        const minuteAgo = new Date(Date.now() - MINUTE);
        const batch: Batch = {
          webhook: newWebhook(
            firing("checkout-warning", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), minuteAgo),
            firing("payments-critical", alertLabels(TEAM_PAYMENTS, "PaymentsErrorRate", SEVERITY_CRITICAL), minuteAgo),
          ),
          results: [
            { fingerprint: "checkout-warning", status: STATUS_FAILED, team: TEAM_CHECKOUT, want: UNROUTED },
            {
              fingerprint: "payments-critical",
              status: STATUS_ROUTED,
              team: TEAM_PAYMENTS,
              want: { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: PAYMENTS_ONCALL },
            },
          ],
        };
        const a = await e.client.webhook(batch.webhook);
        expectBatch(batch, a, a.out);
        check(a.out.results[0] as NonNullable<ReturnType<typeof result>>);

        // Still dispatching the fallback, so the failed alert reaches #alerts.
        expect(
          e
            .notifications()
            .map((n) => n.channel || n.target)
            .sort(),
        ).toEqual([DEFAULT_CHANNEL, PAYMENTS_ONCALL]);
      },
    );
  });

  test.each(webhookBadRequestCases().map((c) => [c.name, c] as const))(
    "answers 400 for a payload it can't read, and routes none of it: %s",
    async (_, c) => {
      const e = await newEnv();
      const a = await e.client.postRaw(PATH_ALERTS, c.body);

      expectStatus(a, c.status);
      const err = decode<ErrorResponse>(a).error;
      expect(err).toBeDefined();
      expect(err?.advice?.length ?? 0, "a client error should say how to fix the request").toBeGreaterThan(0);

      expect(e.notifications()).toEqual([]);
      expect((await e.families()).count(METRIC_ALERTS_RECEIVED)).toBe(0);
    },
  );
});
