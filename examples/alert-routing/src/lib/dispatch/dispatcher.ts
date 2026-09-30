// Delivery of routed alerts: at least once, with deduplication, never
// exactly once. Alertmanager redelivers a webhook it didn't get a 2xx for,
// and alertrouter answers 503 when a delivery of a batch failed or didn't
// finish in time, so the same alert can arrive twice. The dispatcher
// remembers what it delivered by fingerprint, decision, reason and
// destination for a TTL, so a redelivery within it doesn't page twice.
//
// Every decided alert's delivery starts, and runs to its end: a batch's
// deadline bounds only how long the response waits for it. A delivery that
// finishes after its response was sent is still remembered, so the retry
// that response asked for is deduplicated. What deduplication can't catch
// is a delivery that went out and then was forgotten: the process restarted,
// the TTL passed, or the table was full and dropped it early. Each of those
// can page twice.

import type { Clock } from "../clock";
import type { Logger, Metrics } from "../telemetry/types";
import { destination, NO_DESTINATION, type Notification, type Notifier } from "./notifier";

/** The metric label of a destination that neither the team directory nor the kind names. */
export const OTHER_DESTINATION = "other";

/** How often a full deduplication table is logged at most. */
const EVICTION_LOG_INTERVAL_MS = 60_000;

/**
 * What became of one notification: delivered, skipped as a redelivery,
 * failed, or still under way when its response had to be sent ("late").
 */
export type DeliveryStatus = "sent" | "duplicate" | "failed" | "late";

export interface Delivery {
  status: DeliveryStatus;
  destination: string;
  /** Why it failed, for status "failed". */
  error?: unknown;
}

export interface DispatcherOptions {
  notifier: Notifier;
  metrics: Metrics;
  logger: Logger;
  clock: Clock;
  /** How long a delivered notification is remembered; 0 disables deduplication. */
  dedupTtlMs: number;
  /** How many delivered notifications are remembered at most; the oldest go first. */
  dedupMaxEntries: number;
  /** How many deliveries may be in flight at once. */
  concurrency: number;
  /**
   * Whether a destination is safe as a metric label: a team's on-call target
   * or channel, or the kind's default channel. A payload a team wrote could
   * otherwise mint a series per value.
   */
  isKnownDestination: (dest: string) => boolean;
}

export class Dispatcher {
  readonly #opts: DispatcherOptions;
  /**
   * Delivered notifications by key, with when each is forgotten. The TTL is
   * the same for every entry and a write moves its key to the end, so the
   * map's order is expiry order: pruning stops at the first live entry, and
   * the oldest entry is the first.
   */
  readonly #delivered = new Map<string, number>();
  /** Deliveries in flight by key, so two copies of one alert in flight share a delivery. */
  readonly #inFlight = new Map<string, Promise<Delivery>>();
  readonly #slots: Semaphore;
  #lastEvictionLog = Number.NEGATIVE_INFINITY;

  constructor(opts: DispatcherOptions) {
    this.#opts = opts;
    this.#slots = new Semaphore(opts.concurrency);
  }

  /** How many delivered notifications are remembered right now. */
  get remembered(): number {
    return this.#delivered.size;
  }

  /**
   * Delivers n unless it was delivered within the TTL. The delivery starts
   * now, or as soon as a slot is free, and always runs to its end; the
   * caller decides how long to wait for it. It never rejects: a failure
   * comes back as status "failed".
   */
  async dispatch(n: Notification): Promise<Delivery> {
    const dest = destination(n);
    const dedup = n.fingerprint !== "" && this.#opts.dedupTtlMs > 0;
    const key = dedup ? dedupKey(n, dest) : undefined;
    if (key !== undefined) {
      this.#prune();
      const flying = this.#inFlight.get(key);
      if (this.#delivered.has(key) || flying !== undefined) {
        const earlier = flying === undefined ? undefined : await flying;
        if (earlier === undefined || earlier.status !== "failed") {
          this.#opts.metrics.observeDeduplicated(n.decision);
          return { status: "duplicate", destination: dest };
        }
      }
    }

    const delivery = this.#deliver(n, dest).then((d) => {
      if (d.status === "sent" && key !== undefined) this.#remember(key);
      return d;
    });
    if (key === undefined) return delivery;
    this.#inFlight.set(key, delivery);
    try {
      return await delivery;
    } finally {
      if (this.#inFlight.get(key) === delivery) this.#inFlight.delete(key);
    }
  }

  /** The label alertrouter_notifications_total uses for dest. */
  destinationLabel(dest: string): string {
    if (dest === NO_DESTINATION || this.#opts.isKnownDestination(dest)) return dest;
    return OTHER_DESTINATION;
  }

  async #deliver(n: Notification, dest: string): Promise<Delivery> {
    const { metrics } = this.#opts;
    try {
      await this.#slots.run(() => this.#opts.notifier.notify(n));
      metrics.observeNotification(n.decision, this.destinationLabel(dest));
      return { status: "sent", destination: dest };
    } catch (err) {
      metrics.observeNotificationError(n.decision);
      return { status: "failed", destination: dest, error: err };
    }
  }

  #remember(key: string): void {
    const { clock, dedupTtlMs, dedupMaxEntries, metrics, logger } = this.#opts;
    this.#delivered.delete(key);
    this.#delivered.set(key, clock.now() + dedupTtlMs);
    let evicted = 0;
    while (this.#delivered.size > dedupMaxEntries) {
      const oldest = this.#delivered.keys().next().value as string;
      this.#delivered.delete(oldest);
      evicted++;
    }
    if (evicted === 0) return;
    metrics.observeDedupEvicted(evicted);
    const now = clock.now();
    if (now - this.#lastEvictionLog >= EVICTION_LOG_INTERVAL_MS) {
      this.#lastEvictionLog = now;
      logger.warn("the deduplication table is full; the oldest deliveries are forgotten before their TTL", {
        max_entries: dedupMaxEntries,
        advice:
          "raise ALERTROUTER_DEDUP_MAX_ENTRIES, or lower ALERTROUTER_DEDUP_TTL; a redelivery of a forgotten alert notifies again",
      });
    }
  }

  // Forgets expired deliveries. The map is in expiry order, so this stops at
  // the first entry that is still live: amortized O(1) per dispatch.
  #prune(): void {
    const now = this.#opts.clock.now();
    for (const [key, until] of this.#delivered) {
      if (until > now) return;
      this.#delivered.delete(key);
    }
  }
}

function dedupKey(n: Notification, dest: string): string {
  return [n.fingerprint, n.decision, n.reason, dest].join("\u0000");
}

/** A counting semaphore: at most `size` callbacks run at once, the rest wait their turn in order. */
export class Semaphore {
  #free: number;
  // A queue with a head index: shift() on a long array moves every element.
  #waiting: (() => void)[] = [];
  #head = 0;

  constructor(size: number) {
    this.#free = Math.max(1, size);
  }

  async run<T>(fn: () => Promise<T>): Promise<T> {
    if (this.#free > 0) this.#free--;
    else await new Promise<void>((resolve) => this.#waiting.push(resolve));
    try {
      return await fn();
    } finally {
      const next = this.#waiting[this.#head];
      if (next === undefined) {
        this.#free++;
      } else {
        this.#head++;
        if (this.#head === this.#waiting.length) {
          this.#waiting = [];
          this.#head = 0;
        }
        next();
      }
    }
  }
}
