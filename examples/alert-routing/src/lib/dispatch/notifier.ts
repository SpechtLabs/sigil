// Where a routed alert leaves alertrouter: the Notification the router built
// from a decision, and the Notifier that delivers it.
//
// The router makes a decision for every firing alert, the team policy's or a
// fallback, and hands each to a Notifier, so no alert ends without one. A
// drop is dispatched too: it says the alert was looked at and deliberately
// silenced, which is worth a log line when someone later asks why nobody was
// paged. The example ships LogNotifier, which logs instead of calling a pager
// or a chat service; a real deployment puts a PagerDuty or Slack client
// behind the same interface.

import { trace } from "@opentelemetry/api";

import type { Logger } from "../telemetry/types";

/** The destination of a notification that goes nowhere, a drop, in logs and metrics. */
export const NO_DESTINATION = "-";

/** One decision to deliver. target is set for a page, channel for a notification; a drop has neither. */
export interface Notification {
  /** The owning team, or "-" when no team owns the alert. */
  team: string;
  alertname: string;
  /** Alertmanager's fingerprint, empty for an alert posted on its own. */
  fingerprint: string;
  /** The policy that decided, absent when none ran (an unowned or invalid alert). */
  policy?: string;
  decision: string;
  reason: string;
  target?: string;
  channel?: string;
}

/**
 * Delivers notifications. It is called once per firing alert, inside the
 * alert's span. A rejected promise is a delivery that failed: the alert's
 * decision stands, and the caller reports the failure so the sender retries.
 */
export interface Notifier {
  notify(n: Notification): Promise<void>;
}

/** Where the notification goes: the paged target, the channel, or "-" for a drop. */
export function destination(n: Pick<Notification, "target" | "channel">): string {
  if (n.target !== undefined && n.target !== "") return n.target;
  if (n.channel !== undefined && n.channel !== "") return n.channel;
  return NO_DESTINATION;
}

/**
 * The example's notifier: one "notification dispatched" line per
 * notification, carrying the trace and span ids of the alert's span, and the
 * destination recorded on that span, so a trace says where an alert went as
 * well as why. It never fails.
 */
export class LogNotifier implements Notifier {
  constructor(private readonly logger: Logger) {}

  async notify(n: Notification): Promise<void> {
    const dest = destination(n);
    trace.getActiveSpan()?.setAttribute("alertrouter.destination", dest);
    this.logger.info("notification dispatched", {
      team: n.team,
      alertname: n.alertname,
      fingerprint: n.fingerprint,
      ...(n.policy !== undefined && n.policy !== "" ? { policy: n.policy } : {}),
      decision: n.decision,
      reason: n.reason,
      destination: dest,
    });
  }
}
