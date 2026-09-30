// What Prometheus Alertmanager sends to a webhook receiver, version 4 of its
// payload, and how each alert becomes the AlertRouting kind's alert.
//
// Alertmanager describes an alert by its labels only, so the kind's fields
// come from well-known labels: the name from alertname, the severity from
// severity, and the owning team, which the router looks up itself, from
// team. Everything else stays a label, which is how a policy reads env or
// component. firing_for is the only field that isn't a label: it's the time
// the batch arrived minus startsAt.
//
// Alertmanager retries only a 5xx, so a 4xx drops the whole group for good.
// Only a body that isn't a webhook at all (not JSON, not an object, another
// version) is refused; anything wrong with one alert makes that alert
// invalid, and it's routed to the fallback like every other alert.

import { ms } from "@spechtlabs/sigil";

import { type HumaneError, humane } from "../errors";
import { type Alert, parseSeverity, SEVERITIES } from "../routing/kind";

/** The payload version this module reads. */
export const VERSION = "4";

/**
 * The most alerts of one webhook the router evaluates. Alertmanager's
 * max_alerts truncates a group to fewer; the alerts past this go to the
 * fallback unevaluated, so a runaway batch can't hold the request.
 */
export const MAX_ALERTS = 1000;

/** The longest duration Sigil writes, in whole milliseconds (Go's time.Duration). */
const MAX_DURATION_MS = 9_223_372_036_854;

/** The labels the router reads. Every other label reaches the policy untouched. */
export const LABEL_ALERTNAME = "alertname";
export const LABEL_SEVERITY = "severity";
export const LABEL_TEAM = "team";

/** A webhook body: a group of alerts sent to one receiver. Unknown fields are ignored. */
export interface Webhook {
  version: string;
  groupKey: string;
  status: string;
  receiver: string;
  /** How many alerts Alertmanager left out because of the receiver's max_alerts. */
  truncatedAlerts: number;
  alerts: WebhookAlert[];
}

/**
 * One alert of a webhook, as far as it could be read. problem says why the
 * router can't route it by a policy, and is set for an alert whose fields
 * don't have the types Alertmanager sends or whose status is neither firing
 * nor resolved.
 */
export interface WebhookAlert {
  status: string;
  labels: Record<string, string>;
  /** undefined when absent or Go's zero time, which Alertmanager never sends for a firing alert. */
  startsAt: Date | undefined;
  fingerprint: string;
  problem: HumaneError | undefined;
}

/**
 * Reads a decoded JSON body as a webhook. Throws a HumaneError, a 400, only
 * for a body that isn't a webhook at all.
 */
export function parseWebhook(body: unknown): Webhook {
  if (!isObject(body)) {
    throw humane(
      "the request body isn't a valid request: expected a JSON object",
      "send Alertmanager's webhook payload, version 4, with a webhook_configs receiver pointing at /api/v1/alerts",
    );
  }
  const version = typeof body.version === "string" ? body.version : "";
  if (version !== VERSION) {
    throw humane(
      `the webhook has version ${JSON.stringify(version)}, not ${JSON.stringify(VERSION)}`,
      "send Alertmanager's webhook payload version 4, which every Alertmanager since 0.9 sends",
    );
  }
  const rawAlerts = body.alerts ?? [];
  if (!Array.isArray(rawAlerts)) {
    throw humane(
      "the request body isn't a valid request: alerts isn't a list",
      "send Alertmanager's webhook payload, version 4, with a webhook_configs receiver pointing at /api/v1/alerts",
    );
  }
  return {
    version,
    groupKey: typeof body.groupKey === "string" ? body.groupKey : "",
    status: typeof body.status === "string" ? body.status : "",
    receiver: typeof body.receiver === "string" ? body.receiver : "",
    truncatedAlerts: typeof body.truncatedAlerts === "number" ? body.truncatedAlerts : 0,
    alerts: rawAlerts.map((a, i) => readAlert(a, i)),
  };
}

/** Whether the alert fires. A resolved alert needs no route; any other status is invalid. */
export function isResolved(a: WebhookAlert): boolean {
  return a.status === "resolved";
}

/**
 * Turns a firing alert into the kind's alert as of now, the time the router
 * received the batch, or throws a HumaneError saying why it can't. The name
 * and the severity are required: an alert without a name can't be muted by
 * name, and one without a known severity would fail the evaluation the
 * moment a rule compared it. firing_for is zero for an alert that starts in
 * the future because the two clocks disagree.
 */
export function convert(a: WebhookAlert, now: Date): Alert {
  if (a.problem !== undefined) throw a.problem;
  const name = a.labels[LABEL_ALERTNAME] ?? "";
  if (name === "") {
    throw humane(
      `the alert has no ${LABEL_ALERTNAME} label`,
      "send alerts from a Prometheus alerting rule, which sets alertname to the rule's name",
    );
  }
  const raw = a.labels[LABEL_SEVERITY];
  if (raw === undefined) {
    throw humane(
      `alert ${name} has no ${LABEL_SEVERITY} label`,
      `add a severity label to the alerting rule: ${severityList()}`,
    );
  }
  const severity = parseSeverity(raw);
  if (severity === undefined) {
    throw humane(
      `alert ${name} has the severity ${JSON.stringify(raw)}, which the AlertRouting kind doesn't declare`,
      `set the alerting rule's severity label to ${severityList()}, in lower case`,
    );
  }
  if (a.startsAt === undefined) {
    throw humane(
      `alert ${name} has no startsAt`,
      "send the alert as Alertmanager does, with the time it started firing; without it the router can't tell a fresh alert from a sustained one",
    );
  }
  return {
    name,
    severity,
    labels: { ...a.labels },
    firing_for: firingFor(a.startsAt, now),
  };
}

/**
 * The alert as far as platform.paging needs it, for an alert convert
 * refuses: its severity is required, and everything else is taken as it
 * could be read. undefined when the severity can't be read, since without it
 * no rule could page.
 */
export function pagingAlert(a: WebhookAlert, now: Date): Alert | undefined {
  const severity = parseSeverity(a.labels[LABEL_SEVERITY] ?? "");
  if (severity === undefined) return undefined;
  return {
    name: a.labels[LABEL_ALERTNAME] ?? "",
    severity,
    labels: { ...a.labels },
    firing_for: a.startsAt === undefined ? ms(0) : firingFor(a.startsAt, now),
  };
}

/**
 * How long an alert has fired: now minus startsAt, zero for an alert that
 * starts in the future because two clocks disagree, and at most the longest
 * duration Sigil can write, about 292 years, for a startsAt far in the past.
 */
function firingFor(startsAt: Date, now: Date): string {
  return ms(Math.min(Math.max(now.getTime() - startsAt.getTime(), 0), MAX_DURATION_MS));
}

/** The kind's severities for advice: "critical, warning or info". */
export function severityList(): string {
  return `${SEVERITIES.slice(0, -1).join(", ")} or ${SEVERITIES[SEVERITIES.length - 1]}`;
}

function readAlert(raw: unknown, index: number): WebhookAlert {
  const alert: WebhookAlert = { status: "", labels: {}, startsAt: undefined, fingerprint: "", problem: undefined };
  if (!isObject(raw)) {
    alert.problem = humane(`alert ${index} isn't a JSON object`, "send each alert as Alertmanager does");
    return alert;
  }
  alert.fingerprint = typeof raw.fingerprint === "string" ? raw.fingerprint : "";
  alert.status = typeof raw.status === "string" ? raw.status : "";

  const labels = raw.labels ?? {};
  if (isObject(labels) && Object.values(labels).every((v) => typeof v === "string")) {
    alert.labels = { ...(labels as Record<string, string>) };
  } else {
    alert.problem = humane(
      `alert ${index}'s labels aren't a map of strings`,
      "send each alert's labels as Alertmanager does",
    );
    return alert;
  }

  if (alert.status !== "firing" && alert.status !== "resolved") {
    alert.problem = humane(
      `alert ${index} has the status ${JSON.stringify(alert.status)}`,
      `set each alert's status to "firing" or "resolved", as Alertmanager does`,
    );
    return alert;
  }

  if (raw.startsAt !== undefined && raw.startsAt !== null) {
    const startsAt = typeof raw.startsAt === "string" ? parseTime(raw.startsAt) : "invalid";
    if (startsAt === "invalid") {
      alert.problem = humane(
        `alert ${index}'s startsAt ${JSON.stringify(raw.startsAt)} isn't an RFC 3339 time`,
        "send the time the alert started firing as Alertmanager does, like 2026-01-01T00:00:00Z",
      );
      return alert;
    }
    alert.startsAt = startsAt ?? undefined;
  }
  return alert;
}

// RFC 3339, as Go's time.Time reads it; null for Go's zero time, which is
// how a missing time comes across.
function parseTime(text: string): Date | null | "invalid" {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(text)) return "invalid";
  if (text.startsWith("0001-01-01T00:00:00")) return null;
  const d = new Date(text);
  return Number.isNaN(d.getTime()) ? "invalid" : d;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}
