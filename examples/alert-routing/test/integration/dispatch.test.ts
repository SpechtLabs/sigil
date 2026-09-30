// Delivery: at least once, deduplicated by fingerprint for a while, and a
// failed delivery the sender hears about. The Go service delivered each
// alert once per request and answered 200 whatever the notifier did; this
// is the lead's addendum, items 2, 3 and 6.
import { afterAll, afterEach, describe, expect, test } from "bun:test";
import type { Notification, Notifier } from "@/lib/dispatch/notifier";
import { expectStatus } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { METRIC_NOTIFICATIONS } from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  CHECKOUT_ONCALL,
  DECISION_NOTIFY,
  DECISION_PAGE,
  firing,
  firingAlert,
  LABEL_COMPONENT,
  label,
  MINUTE,
  newWebhook,
  SEVERITY_CRITICAL,
  SEVERITY_INFO,
  SEVERITY_WARNING,
  STATUS_ROUTED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "../fixture/requests";
import { closeEnvs, type Env, newEnv, releaseTelemetry, SPAN_ROUTE } from "./env";

const METRIC_NOTIFICATION_ERRORS = "alertrouter_notification_errors_total";
const METRIC_DEDUPLICATED = "alertrouter_notifications_deduplicated_total";
const METRIC_DEDUP_EVICTED = "alertrouter_notifications_dedup_evicted_total";
const STATUS_DISPATCH_FAILED = "dispatch_failed";

/** SpanStatusCode.ERROR. */
const SPAN_ERROR = 2;

afterEach(closeEnvs);
afterAll(releaseTelemetry);

/** A webhook with one critical checkout alert and one checkout warning, fired a minute ago. */
function pageAndNotify(prefix = "") {
  const minuteAgo = new Date(Date.now() - MINUTE);
  return newWebhook(
    firing(`${prefix}critical`, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
    firing(`${prefix}warning`, alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), minuteAgo),
  );
}

/** A notifier that takes delayMs per delivery, and records what it was asked and what it delivered. */
class SlowNotifier implements Notifier {
  readonly asked: Notification[] = [];
  readonly delivered: Notification[] = [];

  constructor(readonly delayMs: number) {}

  async notify(n: Notification): Promise<void> {
    this.asked.push({ ...n });
    await Bun.sleep(this.delayMs);
    this.delivered.push({ ...n });
  }
}

/** A notifier that fails every delivery until told otherwise, and records what it was asked. */
class FlakyNotifier implements Notifier {
  failing = true;
  readonly asked: Notification[] = [];

  async notify(n: Notification): Promise<void> {
    this.asked.push({ ...n });
    if (this.failing) throw new Error("the pager is down");
  }
}

describe("Delivering a notification", () => {
  describe("when Alertmanager delivers the same webhook again", () => {
    test("notifies once, and says the second delivery was a duplicate", async () => {
      const e = await newEnv();
      const webhook = pageAndNotify();

      const first = await e.client.webhook(webhook);
      const again = await e.client.webhook(webhook);
      expectStatus(first, 200);
      expectStatus(again, 200);

      // Both answers carry the same routes: a duplicate is still routed.
      expect(again.out.results.map((r) => r.status)).toEqual([STATUS_ROUTED, STATUS_ROUTED]);
      expect(again.out.results.map((r) => r.decision)).toEqual([DECISION_PAGE, DECISION_NOTIFY]);

      expect(
        e
          .notifications()
          .map((n) => n.fingerprint)
          .sort(),
      ).toEqual(["critical", "warning"]);
      const families = await e.families();
      expect(families.sum(METRIC_NOTIFICATIONS)).toBe(2);
      expect(families.value(METRIC_DEDUPLICATED, { decision: DECISION_PAGE })).toBe(1);
      expect(families.value(METRIC_DEDUPLICATED, { decision: DECISION_NOTIFY })).toBe(1);

      // Every alert still gets its "alert routed" line, marked as a duplicate the second time.
      const routed = e.logs().filter((l) => l.msg === "alert routed" && l.fields.fingerprint === "critical");
      expect(routed.map((l) => l.fields.deduplicated ?? false)).toEqual([false, true]);
    });

    test("notifies again once the dedup window passed", async () => {
      const e = await newEnv({ dedupTtlMs: 50 });
      const webhook = pageAndNotify();

      expectStatus(await e.client.webhook(webhook), 200);
      await Bun.sleep(100);
      expectStatus(await e.client.webhook(webhook), 200);

      expect(e.notifications()).toHaveLength(4);
    });

    test("notifies again when the decision changed, such as a warning that became sustained", async () => {
      const e = await newEnv();
      const fresh = firing(
        "latency",
        alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING),
        new Date(Date.now() - MINUTE),
      );
      const sustained = { ...fresh, startsAt: new Date(Date.now() - 20 * MINUTE).toISOString() };

      expectStatus(await e.client.webhook(newWebhook(fresh)), 200);
      expectStatus(await e.client.webhook(newWebhook(sustained)), 200);

      expect(e.notifications().map((n) => n.decision)).toEqual([DECISION_NOTIFY, DECISION_PAGE]);
    });
  });

  test("never deduplicates an alert posted on its own, which has no fingerprint", async () => {
    const e = await newEnv();
    for (let i = 0; i < 2; i++) {
      expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);
    }
    expect(e.notifications()).toHaveLength(2);
  });

  describe("when the notifier fails", () => {
    test("answers the webhook with 503, so Alertmanager delivers it again, and counts the failure", async () => {
      const notifier = new FlakyNotifier();
      const e = await newEnv({ notifier });
      e.resetSpans();

      const a = await e.client.webhook(pageAndNotify());
      expectStatus(a, 503);
      // The body still says what was decided, and that it didn't go out.
      expect(a.out.results.map((r) => r.status)).toEqual([STATUS_DISPATCH_FAILED, STATUS_DISPATCH_FAILED]);
      expect(a.out.results.map((r) => r.decision)).toEqual([DECISION_PAGE, DECISION_NOTIFY]);
      expect(a.out.results.map((r) => r.error)).toEqual([
        "dispatching the page to checkout-primary failed: the pager is down",
        "dispatching the notify to #checkout-alerts failed: the pager is down",
      ]);
      expect(a.out.routed).toBe(0);

      const families = await e.families();
      expect(families.value(METRIC_NOTIFICATION_ERRORS, { decision: DECISION_PAGE })).toBe(1);
      expect(families.value(METRIC_NOTIFICATION_ERRORS, { decision: DECISION_NOTIFY })).toBe(1);
      expect(families.sum(METRIC_NOTIFICATIONS)).toBe(0);

      const spans = e.spansNamed(SPAN_ROUTE);
      expect(spans.map((s) => s.status.code)).toEqual([SPAN_ERROR, SPAN_ERROR]);
      const lines = e.logs().filter((l) => l.msg === "alert routed");
      expect(lines.map((l) => [l.level, l.fields.status])).toEqual([
        ["error", STATUS_DISPATCH_FAILED],
        ["error", STATUS_DISPATCH_FAILED],
      ]);
      // The notifier's own reason is in the log line, for whoever fixes it.
      for (const l of lines) expect(String(l.fields.dispatch_error)).toContain("the pager is down");
    });

    test("delivers the retry, since a failed delivery isn't remembered as sent", async () => {
      const notifier = new FlakyNotifier();
      const e = await newEnv({ notifier });
      const webhook = pageAndNotify();

      expectStatus(await e.client.webhook(webhook), 503);
      notifier.failing = false;
      const retry = await e.client.webhook(webhook);
      expectStatus(retry, 200);
      expect(retry.out.routed).toBe(2);

      expect(notifier.asked.map((n) => n.fingerprint).sort()).toEqual(["critical", "critical", "warning", "warning"]);
      expect(
        (await e.families()).value(METRIC_NOTIFICATIONS, { decision: DECISION_PAGE, destination: CHECKOUT_ONCALL }),
      ).toBe(1);
    });

    // The route endpoint answers the decision, which is right, as the Go
    // service did; its error says the delivery failed. Only Alertmanager
    // needs a 5xx to retry.
    test("answers a single alert with the decision it couldn't deliver and why", async () => {
      const e = await newEnv({ notifier: new FlakyNotifier() });

      const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL));
      expectStatus(a, 200);
      expect([a.out.decision, a.out.target]).toEqual([DECISION_PAGE, CHECKOUT_ONCALL]);
      expect(a.out.error?.message).toContain("dispatching the page to checkout-primary failed");
    });
  });

  describe("the destination label of the notifications counter", () => {
    test("names a team's on-call target and channel and the default channel, and folds any other into other", async () => {
      const e = await newEnv({ logNotifier: true });
      // checkout posts payments info to #checkout-payments, a channel its
      // policy wrote, not one the team directory lists.
      expectStatus(
        await e.client.route(
          TEAM_CHECKOUT,
          firingAlert("PaymentsRetryRate", SEVERITY_INFO, label(LABEL_COMPONENT, "payments")),
        ),
        200,
      );
      expectStatus(await e.client.route(TEAM_PAYMENTS, firingAlert("PaymentsErrorRate", SEVERITY_CRITICAL)), 200);

      const families = await e.families();
      expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_NOTIFY, destination: "other" })).toBe(1);
      expect(families.count(METRIC_NOTIFICATIONS, { destination: "#checkout-payments" })).toBe(0);
      expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_PAGE, destination: "payments-primary" })).toBe(
        1,
      );

      // The log line keeps the real destination: it isn't a label.
      const line = e.logs().find((l) => l.msg === "alert routed" && l.fields.alertname === "PaymentsRetryRate");
      expect(line?.fields.destination).toBe("#checkout-payments");
    });
  });

  // A slow pager must not hold the webhook past Alertmanager's timeout,
  // and must not lose the page either: the answer stops waiting, the
  // delivery goes on, and the retry the 503 brings is deduplicated.
  describe("when a delivery takes longer than the batch may", () => {
    test("answers 503 in time, still delivers, and absorbs the retry without a second page", async () => {
      const notifier = new SlowNotifier(600);
      const e = await newEnv({ notifier, batchTimeoutMs: 200 });
      const webhook = pageAndNotify();

      const start = performance.now();
      const first = await e.client.webhook(webhook);
      expect(performance.now() - start).toBeLessThan(500);
      expectStatus(first, 503);
      expect(first.out.results.map((r) => r.status)).toEqual([STATUS_DISPATCH_FAILED, STATUS_DISPATCH_FAILED]);
      expect(first.out.results[0]?.error).toContain("hadn't finished when the batch's time ran out");

      // The deliveries went on after the answer.
      await eventually(
        () => expect(notifier.delivered.map((n) => n.fingerprint).sort()).toEqual(["critical", "warning"]),
        {
          timeoutMs: 3_000,
        },
      );

      const retry = await e.client.webhook(webhook);
      expectStatus(retry, 200);
      expect(retry.out.routed).toBe(2);
      expect(notifier.asked).toHaveLength(2);
      expect((await e.families()).value(METRIC_DEDUPLICATED, { decision: DECISION_PAGE })).toBe(1);
    }, 10_000);
  });

  // The dedup table remembers every delivery for the TTL. It must stay
  // cheap to consult however many alerts went out before, and bounded, so a
  // flood of distinct alerts can't grow it without limit.
  describe("the dedup table", () => {
    test("stays fast to consult after a hundred thousand earlier deliveries", async () => {
      const e = await newEnv();
      const baseline = await timeWebhook(e, "baseline");

      const start = performance.now();
      for (let i = 0; i < 100_000; i++) {
        await e.service.dispatcher.dispatch({
          team: TEAM_CHECKOUT,
          alertname: CHECKOUT_ERROR_RATE,
          fingerprint: `earlier-${i}`,
          decision: DECISION_PAGE,
          reason: "critical_alert",
          target: CHECKOUT_ONCALL,
        });
      }
      // Well under a second when each delivery costs O(1); minutes when each
      // one walks the table.
      expect(performance.now() - start).toBeLessThan(5_000);

      const after = await timeWebhook(e, "after");
      expect(after).toBeLessThan(Math.max(1_000, 5 * baseline));
    }, 30_000);

    test("is capped: the oldest entries go first, counted, and their alerts notify again", async () => {
      const e = await newEnv({ dedupMaxEntries: 10 });
      const minuteAgo = new Date(Date.now() - MINUTE);
      const alerts = Array.from({ length: 25 }, (_, i) =>
        firing(`flood-${i}`, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
      );
      expectStatus(await e.client.webhook(newWebhook(...alerts)), 200);

      const families = await e.families();
      expect(families.value(METRIC_DEDUP_EVICTED)).toBe(15);
      // One warning for the flood, not one per eviction.
      expect(e.logs().filter((l) => l.level === "warn" && /dedup/i.test(l.msg))).toHaveLength(1);

      // The newest ten are still remembered; an evicted one notifies again.
      const newest = alerts[24];
      const oldest = alerts[0];
      if (newest === undefined || oldest === undefined) throw new Error("the flood has 25 alerts");
      expectStatus(await e.client.webhook(newWebhook(newest, oldest)), 200);
      const sent = e.notifications().map((n) => n.fingerprint);
      expect(sent.filter((f) => f === "flood-24")).toHaveLength(1);
      expect(sent.filter((f) => f === "flood-0")).toHaveLength(2);
    });
  });
});

/** How long a webhook of a thousand critical alerts, fingerprinted with prefix, takes to answer. */
async function timeWebhook(e: Env, prefix: string): Promise<number> {
  const minuteAgo = new Date(Date.now() - MINUTE);
  const alerts = Array.from({ length: 1000 }, (_, i) =>
    firing(`${prefix}-${i}`, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
  );
  const start = performance.now();
  expectStatus(await e.client.webhook(newWebhook(...alerts)), 200);
  return performance.now() - start;
}
