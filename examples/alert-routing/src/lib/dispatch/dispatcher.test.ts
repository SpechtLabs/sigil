import { describe, expect, test } from "bun:test";

import { messageOf } from "../errors";
import { PromMetrics } from "../telemetry/metrics";
import { fakeTelemetry } from "../testing";
import { Dispatcher, type DispatcherOptions, OTHER_DESTINATION, Semaphore } from "./dispatcher";
import type { Notification, Notifier } from "./notifier";

class FakeClock {
  constructor(public t = 1_000_000) {}
  now = () => this.t;
}

class RecordingNotifier implements Notifier {
  sent: Notification[] = [];
  fail: Error | undefined;
  hold: Promise<void> | undefined;
  async notify(n: Notification): Promise<void> {
    if (this.hold !== undefined) await this.hold;
    if (this.fail !== undefined) throw this.fail;
    this.sent.push(n);
  }
}

const PAGE: Notification = {
  team: "checkout",
  alertname: "CheckoutErrorRate",
  fingerprint: "f1",
  policy: "checkout.alerts",
  decision: "page",
  reason: "critical_alert",
  target: "checkout-primary",
};

function setup(over: Partial<DispatcherOptions> = {}) {
  const notifier = new RecordingNotifier();
  const clock = new FakeClock();
  const metrics = new PromMetrics();
  const { telemetry, logs } = fakeTelemetry();
  const dispatcher = new Dispatcher({
    notifier,
    metrics,
    logger: telemetry.logger,
    clock,
    dedupTtlMs: 60_000,
    dedupMaxEntries: 1_000,
    concurrency: 4,
    isKnownDestination: (d) => d === "checkout-primary" || d === "#alerts",
    ...over,
  });
  const text = async () => (await metrics.render()).body;
  return { notifier, clock, metrics, dispatcher, text, logs };
}

describe("Dispatcher", () => {
  test("delivers and counts a notification by its destination", async () => {
    const { dispatcher, notifier, text } = setup();
    expect(await dispatcher.dispatch(PAGE)).toEqual({ status: "sent", destination: "checkout-primary" });
    expect(notifier.sent).toEqual([PAGE]);
    expect(await text()).toContain('alertrouter_notifications_total{decision="page",destination="checkout-primary"} 1');
  });

  test("a redelivery within the TTL is a duplicate, not a second page", async () => {
    const { dispatcher, notifier, text } = setup();
    await dispatcher.dispatch(PAGE);
    expect(await dispatcher.dispatch({ ...PAGE })).toEqual({ status: "duplicate", destination: "checkout-primary" });
    expect(notifier.sent).toHaveLength(1);
    expect(await text()).toContain('alertrouter_notifications_deduplicated_total{decision="page"} 1');
  });

  test("two copies in flight at once share one delivery", async () => {
    const { dispatcher, notifier } = setup();
    let release = () => {};
    notifier.hold = new Promise((r) => {
      release = r;
    });
    const a = dispatcher.dispatch(PAGE);
    const b = dispatcher.dispatch(PAGE);
    release();
    expect((await Promise.all([a, b])).map((d) => d.status).sort()).toEqual(["duplicate", "sent"]);
    expect(notifier.sent).toHaveLength(1);
  });

  test("after the TTL the same alert goes out again", async () => {
    const { dispatcher, notifier, clock } = setup();
    await dispatcher.dispatch(PAGE);
    clock.t += 60_000;
    expect((await dispatcher.dispatch(PAGE)).status).toBe("sent");
    expect(notifier.sent).toHaveLength(2);
  });

  test.each([
    ["another decision", { decision: "notify", reason: "routine", target: undefined, channel: "#checkout-alerts" }],
    ["another reason", { reason: "sustained" }],
    ["another target", { target: "someone-else" }],
    ["another fingerprint", { fingerprint: "f2" }],
  ])("%s isn't a duplicate", async (_name, over) => {
    const { dispatcher, notifier } = setup();
    await dispatcher.dispatch(PAGE);
    expect((await dispatcher.dispatch({ ...PAGE, ...over } as Notification)).status).toBe("sent");
    expect(notifier.sent).toHaveLength(2);
  });

  test("an alert without a fingerprint is never deduplicated", async () => {
    const { dispatcher, notifier } = setup();
    await dispatcher.dispatch({ ...PAGE, fingerprint: "" });
    await dispatcher.dispatch({ ...PAGE, fingerprint: "" });
    expect(notifier.sent).toHaveLength(2);
  });

  test("a TTL of 0 turns deduplication off", async () => {
    const { dispatcher, notifier } = setup({ dedupTtlMs: 0 });
    await dispatcher.dispatch(PAGE);
    await dispatcher.dispatch(PAGE);
    expect(notifier.sent).toHaveLength(2);
  });

  test("a failed delivery is reported, counted, and not remembered, so the retry goes out", async () => {
    const { dispatcher, notifier, text } = setup();
    notifier.fail = new Error("pager unreachable");
    const failed = await dispatcher.dispatch(PAGE);
    expect(failed.status).toBe("failed");
    expect(messageOf(failed.error)).toBe("pager unreachable");
    expect(await text()).toContain('alertrouter_notification_errors_total{decision="page"} 1');
    expect(await text()).not.toContain('alertrouter_notifications_total{decision="page"');

    notifier.fail = undefined;
    expect((await dispatcher.dispatch(PAGE)).status).toBe("sent");
  });

  test("a delivery the caller stopped waiting for still runs to its end, and is remembered", async () => {
    const { dispatcher, notifier } = setup();
    let release = () => {};
    notifier.hold = new Promise((r) => {
      release = r;
    });
    const first = dispatcher.dispatch(PAGE);
    // The caller answers without waiting; the delivery finishes later.
    release();
    await first;
    expect(notifier.sent).toHaveLength(1);
    expect((await dispatcher.dispatch(PAGE)).status).toBe("duplicate");
  });

  test("forgets expired deliveries from the front of the table, without scanning the live ones", async () => {
    const { dispatcher, clock } = setup({ dedupTtlMs: 1_000 });
    for (let i = 0; i < 5; i++) await dispatcher.dispatch({ ...PAGE, fingerprint: `old-${i}` });
    clock.t += 500;
    for (let i = 0; i < 5; i++) await dispatcher.dispatch({ ...PAGE, fingerprint: `new-${i}` });
    expect(dispatcher.remembered).toBe(10);
    clock.t += 600; // the first five expired, the last five haven't
    await dispatcher.dispatch({ ...PAGE, fingerprint: "probe" });
    expect(dispatcher.remembered).toBe(6);
  });

  test("a redelivery moves its entry to the back, so its TTL starts again", async () => {
    const { dispatcher, clock, notifier } = setup({ dedupTtlMs: 1_000 });
    await dispatcher.dispatch({ ...PAGE, fingerprint: "a" });
    await dispatcher.dispatch({ ...PAGE, fingerprint: "b" });
    clock.t += 1_000;
    await dispatcher.dispatch({ ...PAGE, fingerprint: "a" }); // expired: sent again, remembered anew
    clock.t += 500;
    expect((await dispatcher.dispatch({ ...PAGE, fingerprint: "a" })).status).toBe("duplicate");
    expect(notifier.sent.map((n) => n.fingerprint)).toEqual(["a", "b", "a"]);
  });

  test("a full table forgets the oldest delivery first, counts it and logs it once", async () => {
    const { dispatcher, text, logs, notifier } = setup({ dedupMaxEntries: 3 });
    for (const fp of ["a", "b", "c", "d", "e"]) await dispatcher.dispatch({ ...PAGE, fingerprint: fp });
    expect(dispatcher.remembered).toBe(3);
    expect(await text()).toContain("alertrouter_notifications_dedup_evicted_total 2");
    expect(logs.filter((l) => l.msg.startsWith("the deduplication table is full"))).toHaveLength(1);
    // "a" was forgotten, so its redelivery notifies again; "e" wasn't.
    await dispatcher.dispatch({ ...PAGE, fingerprint: "a" });
    expect((await dispatcher.dispatch({ ...PAGE, fingerprint: "e" })).status).toBe("duplicate");
    expect(notifier.sent.filter((n) => n.fingerprint === "a")).toHaveLength(2);
  });

  test("12,000 distinct alerts are deduplicated in linear time", async () => {
    const { dispatcher } = setup({ dedupMaxEntries: 100_000 });
    const started = performance.now();
    for (let i = 0; i < 12_000; i++) await dispatcher.dispatch({ ...PAGE, fingerprint: `fp-${i}` });
    // The quadratic prune took seconds for this; linear takes milliseconds.
    expect(performance.now() - started).toBeLessThan(1_500);
    expect(dispatcher.remembered).toBe(12_000);
  });

  test.each([
    ["an on-call target from the directory", "checkout-primary", "checkout-primary"],
    ["the kind's default channel", "#alerts", "#alerts"],
    ["a drop", "-", "-"],
    ["a channel a team's payload made up", "#whatever-the-team-wrote", OTHER_DESTINATION],
  ])("labels %s as %s", (_name, dest, label) => {
    expect(setup().dispatcher.destinationLabel(dest)).toBe(label);
  });

  test("a drop is dispatched too, to no destination", async () => {
    const { dispatcher, text } = setup();
    const d = await dispatcher.dispatch({ ...PAGE, decision: "drop", reason: "muted", target: undefined });
    expect(d).toEqual({ status: "sent", destination: "-" });
    expect(await text()).toContain('alertrouter_notifications_total{decision="drop",destination="-"} 1');
  });
});

describe("Semaphore", () => {
  test("runs at most size callbacks at once, in order", async () => {
    const sem = new Semaphore(2);
    let running = 0;
    let peak = 0;
    const order: number[] = [];
    await Promise.all(
      [1, 2, 3, 4, 5].map((i) =>
        sem.run(async () => {
          running++;
          peak = Math.max(peak, running);
          await new Promise((r) => setTimeout(r, 5));
          order.push(i);
          running--;
        }),
      ),
    );
    expect(peak).toBe(2);
    expect(order).toEqual([1, 2, 3, 4, 5]);
  });

  test("frees the slot when the callback throws", async () => {
    const sem = new Semaphore(1);
    await expect(sem.run(async () => Promise.reject(new Error("x")))).rejects.toThrow("x");
    expect(await sem.run(async () => 7)).toBe(7);
  });
});
