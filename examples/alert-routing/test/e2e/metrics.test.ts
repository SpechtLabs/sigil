import { beforeAll, describe, expect, test } from "bun:test";
import { firingCount, mixedBatch, uniqueTag } from "../fixture/cases";
import { decode, expectStatus } from "../fixture/client";
import { expectBatch } from "../fixture/expect";
import {
  type Families,
  METRIC_ALERTS_RECEIVED,
  METRIC_ALERTS_ROUTED,
  METRIC_BATCH_SIZE,
  METRIC_DECISIONS,
  METRIC_EVAL_DURATION,
  METRIC_LAST_RELOAD,
  METRIC_NOTIFICATIONS,
  METRIC_POLICY_INFO,
  METRIC_RELOAD_OK,
  METRIC_RELOADS,
} from "../fixture/metrics";
import {
  CHECKOUT_ERROR_RATE,
  DECISION_NOTIFY,
  DECISION_PAGE,
  DEFAULT_CHANNEL,
  firingAlert,
  REASON_CRITICAL_ALERT,
  SEVERITY_CRITICAL,
  STATUS_INVALID,
  STATUS_ROUTED,
  STATUS_UNOWNED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "../fixture/requests";
import { type PoliciesResponse, ROOT_SUFFIX, routing } from "../fixture/wire";
import { alertrouter, E2E, SPEC_TIMEOUT, scrapeMetrics, waitReady } from "./e2e";

// The specs compare values before and after their own requests instead of
// absolute values, because the other specs, the reload poller, k6 and
// earlier runs against the same stack all move the counters too.
describe.skipIf(!E2E)("e2e", () => {
  beforeAll(waitReady, SPEC_TIMEOUT);

  describe("Metrics", () => {
    test(
      "counts each decision by team, policy, decision and reason",
      async () => {
        const labels = {
          team: TEAM_CHECKOUT,
          policy: "checkout.alerts",
          decision: DECISION_PAGE,
          reason: REASON_CRITICAL_ALERT,
        };
        const before = (await scrapeMetrics()).value(METRIC_DECISIONS, labels);

        expectStatus(await alertrouter.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);

        expect((await scrapeMetrics()).value(METRIC_DECISIONS, labels) - before).toBe(1);
      },
      SPEC_TIMEOUT,
    );

    test(
      "records the evaluation latency per team as a histogram",
      async () => {
        expectStatus(await alertrouter.route(TEAM_PAYMENTS, firingAlert("PaymentsErrorRate", SEVERITY_CRITICAL)), 200);

        const families = await scrapeMetrics();
        expect(families.type(METRIC_EVAL_DURATION)).toBe("histogram");
        const m = families.find(METRIC_EVAL_DURATION, { team: TEAM_PAYMENTS });
        expect(m, `no ${METRIC_EVAL_DURATION} series for team payments`).toBeDefined();
        expect(m?.histogram?.count ?? 0).toBeGreaterThanOrEqual(1);
        expect(m?.histogram?.buckets.length ?? 0).toBeGreaterThan(0);
      },
      SPEC_TIMEOUT,
    );

    test(
      "counts a webhook's alerts by status, outcome and notification, and its size",
      async () => {
        const batch = mixedBatch(new Date(), uniqueTag());
        const before = await scrapeMetrics();

        const a = await alertrouter.webhook(batch.webhook);
        expectBatch(batch, a, a.out);

        const after = await scrapeMetrics();
        const delta = (name: string, labels: Record<string, string>) =>
          after.sum(name, labels) - before.sum(name, labels);
        expect(delta(METRIC_ALERTS_RECEIVED, { status: "firing" })).toBe(firingCount(batch));
        expect(delta(METRIC_ALERTS_RECEIVED, { status: "resolved" })).toBe(1);
        expect(delta(METRIC_ALERTS_ROUTED, { outcome: STATUS_ROUTED })).toBe(4);
        expect(delta(METRIC_ALERTS_ROUTED, { team: "-", outcome: STATUS_UNOWNED })).toBe(2);
        expect(delta(METRIC_ALERTS_ROUTED, { outcome: STATUS_INVALID })).toBe(2);
        // Every firing alert ends in exactly one notification.
        expect(delta(METRIC_NOTIFICATIONS, {})).toBe(firingCount(batch));
        expect(delta(METRIC_NOTIFICATIONS, { decision: DECISION_NOTIFY, destination: DEFAULT_CHANNEL })).toBe(3);

        const size = (f: Families) => f.sampleCount(METRIC_BATCH_SIZE);
        expect(size(after) - size(before)).toBe(1);
      },
      SPEC_TIMEOUT,
    );

    test(
      "counts successful reloads and stamps the time and health",
      async () => {
        const success = { result: "success" };
        const before = (await scrapeMetrics()).value(METRIC_RELOADS, success);

        const a = await alertrouter.reload();
        expectStatus(a, 200);
        const loaded = routing(decode<PoliciesResponse>(a));
        expect(loaded).toBeDefined();

        const families = await scrapeMetrics();
        expect(families.value(METRIC_RELOADS, success) - before).toBeGreaterThanOrEqual(1);
        // The poller may reload again between the POST and the scrape, so the
        // gauge is at least the loaded_at the POST reported.
        expect(families.value(METRIC_LAST_RELOAD)).toBeGreaterThanOrEqual(
          Math.floor(Date.parse(loaded?.loaded_at ?? "") / 1000),
        );
        expect(families.value(METRIC_RELOAD_OK)).toBe(1);
      },
      SPEC_TIMEOUT,
    );

    test(
      "exposes every loaded team policy as an info series with the bundle's fingerprint",
      async () => {
        const { fingerprint } = await alertrouter.served();
        const families = await scrapeMetrics();

        for (const team of [TEAM_CHECKOUT, TEAM_PAYMENTS]) {
          const labels = { team, policy: team + ROOT_SUFFIX, fingerprint };
          expect(families.value(METRIC_POLICY_INFO, labels), `${METRIC_POLICY_INFO}${JSON.stringify(labels)}`).toBe(1);
        }
      },
      SPEC_TIMEOUT,
    );
  });
});
