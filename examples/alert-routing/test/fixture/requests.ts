// The alerts both suites send, and the names they send them under.
import { join } from "node:path";
import type { RouteRequest, Webhook, WebhookAlert } from "./wire";

/** The example's root, examples/alert-routing/, wherever bun test runs from. */
export const EXAMPLES_DIR = join(import.meta.dirname, "..", "..");

/** The teams the example serves, as the default team directory lists them. */
export const TEAM_CHECKOUT = "checkout";
export const TEAM_PAYMENTS = "payments";
export const CHECKOUT_ONCALL = "checkout-primary";
export const CHECKOUT_CHANNEL = "#checkout-alerts";
export const PAYMENTS_ONCALL = "payments-primary";
export const PAYMENTS_CHANNEL = "#payments-alerts";
/** Where the kind's default decision posts. */
export const DEFAULT_CHANNEL = "#alerts";

/** The severities the AlertRouting kind declares. */
export const SEVERITY_CRITICAL = "critical";
export const SEVERITY_WARNING = "warning";
export const SEVERITY_INFO = "info";

/** The decisions and reasons of the AlertRouting kind. */
export const DECISION_PAGE = "page";
export const DECISION_DROP = "drop";
export const DECISION_NOTIFY = "notify";
export const REASON_CRITICAL_ALERT = "critical_alert";
export const REASON_SUSTAINED = "sustained";
export const REASON_MUTED = "muted";
export const REASON_NOT_PRODUCTION = "not_production";
export const REASON_ROUTINE = "routine";
export const REASON_UNROUTED = "unrouted";

/** What became of one alert of a webhook batch. */
export const STATUS_ROUTED = "routed";
export const STATUS_RESOLVED = "resolved";
export const STATUS_UNOWNED = "unowned";
export const STATUS_INVALID = "invalid";
export const STATUS_FAILED = "failed";

/** The labels the router reads from an Alertmanager alert, and the ones the policies read. */
export const LABEL_ALERTNAME = "alertname";
export const LABEL_SEVERITY = "severity";
export const LABEL_TEAM = "team";
export const LABEL_ENV = "env";
export const LABEL_COMPONENT = "component";
export const ENV_PRODUCTION = "production";
export const ENV_STAGING = "staging";

/** The alerts the specs send most often. CHECKOUT_MUTED and PAYMENTS_MUTED are the ones each team mutes. */
export const CHECKOUT_ERROR_RATE = "CheckoutErrorRate";
export const CHECKOUT_LATENCY = "CheckoutLatencyHigh";
export const CHECKOUT_MUTED = "CheckoutCanaryLatency";
export const PAYMENTS_MUTED = "PaymentsSettlementBatchSlow";

/** The statuses of a webhook and of each alert in it, as Alertmanager sends them. */
export const ALERT_FIRING = "firing";
export const ALERT_RESOLVED = "resolved";

/** How Go renders a zero time.Time, which Alertmanager sends as a firing alert's endsAt. */
const ZERO_TIME = "0001-01-01T00:00:00Z";

/** Changes one aspect of a request built by {@link firingAlert}. */
export type Mutator = (r: RouteRequest) => void;

/**
 * A production alert called name at severity that has fired for a minute,
 * with each mutator applied in turn. Every call builds a fresh label map, so
 * a mutator never leaks into the next spec.
 */
export function firingAlert(name: string, severity: string, ...mutators: Mutator[]): RouteRequest {
  const r: RouteRequest = {
    alert: { name, severity, labels: { [LABEL_ENV]: ENV_PRODUCTION }, firing_for: "1m" },
  };
  for (const mutate of mutators) mutate(r);
  return r;
}

/** Sets how long the alert has fired, a duration string. */
export function firingFor(d: string): Mutator {
  return (r) => {
    r.alert.firing_for = d;
  };
}

/** Sets the alert's env label, which decides whether it is a production alert. */
export function env(value: string): Mutator {
  return label(LABEL_ENV, value);
}

/** Sets one label of the alert. */
export function label(key: string, value: string): Mutator {
  return (r) => {
    r.alert.labels[key] = value;
  };
}

/** Removes one label of the alert. */
export function noLabel(key: string): Mutator {
  return (r) => {
    delete r.alert.labels[key];
  };
}

/**
 * Renders the request with one more field in the alert, which the route
 * endpoint must reject rather than ignore: a misspelled field would
 * otherwise evaluate a policy against a zero value.
 */
export function jsonWithField(r: RouteRequest, key: string, value: unknown): string {
  return JSON.stringify({ alert: { ...r.alert, [key]: value } });
}

/**
 * An Alertmanager webhook, version 4, carrying alerts. The batch is firing
 * when any alert in it fires.
 */
export function newWebhook(...alerts: WebhookAlert[]): Webhook {
  return {
    version: "4",
    groupKey: `{}:{alertname=~".+"}`,
    truncatedAlerts: 0,
    status: alerts.some((a) => a.status === ALERT_FIRING) ? ALERT_FIRING : ALERT_RESOLVED,
    receiver: "alertrouter",
    groupLabels: {},
    commonLabels: {},
    commonAnnotations: {},
    externalURL: "http://alertmanager:9093",
    alerts: [...alerts],
  };
}

/**
 * A firing alert that started at startsAt, identified by fingerprint. The
 * router derives firing_for from startsAt and its own clock, so a spec
 * against the wall clock picks a start well clear of any threshold it tests.
 */
export function firing(fingerprint: string, labels: Record<string, string>, startsAt: Date): WebhookAlert {
  return {
    status: ALERT_FIRING,
    labels,
    annotations: { summary: "sent by the test suite" },
    startsAt: startsAt.toISOString(),
    endsAt: ZERO_TIME,
    generatorURL: "http://prometheus:9090/graph",
    fingerprint,
  };
}

/** An alert that fired for an hour and resolved at endsAt, which the router acknowledges without a policy. */
export function resolved(fingerprint: string, labels: Record<string, string>, endsAt: Date): WebhookAlert {
  return {
    ...firing(fingerprint, labels, new Date(endsAt.getTime() - HOUR)),
    status: ALERT_RESOLVED,
    endsAt: endsAt.toISOString(),
  };
}

/**
 * The labels of a production alert owned by team, with more labels as key,
 * value pairs. An empty team leaves the team label out, and a pair with an
 * empty value removes that label, so a spec can build an alert without an
 * env or a name.
 */
export function alertLabels(team: string, name: string, severity: string, ...pairs: string[]): Record<string, string> {
  const labels: Record<string, string> = {
    [LABEL_ALERTNAME]: name,
    [LABEL_SEVERITY]: severity,
    [LABEL_ENV]: ENV_PRODUCTION,
  };
  if (team !== "") labels[LABEL_TEAM] = team;
  for (let i = 0; i + 1 < pairs.length; i += 2) {
    const key = pairs[i] as string;
    const value = pairs[i + 1] as string;
    if (value === "") delete labels[key];
    else labels[key] = value;
  }
  return labels;
}

/**
 * A checkout.alerts document that pages like every team policy must, and
 * then runs far past any evaluation timeout before it decides anything else:
 * a quantifier nested in another walks n² pairs of a list, and the condition
 * never holds, so nothing cuts the walk short. 10,000 names make a hundred
 * million steps, well over a second unbounded, from about 100 KB.
 */
export function slowPolicy(n: number): string {
  const names = Array.from({ length: n }, (_, i) => `"n${i}"`);
  return (
    "policy checkout.alerts: AlertRouting@1\n\n" +
    "use platform.paging\n\n" +
    "paging()\n\n" +
    `let names = [${names.join(", ")}]\n\n` +
    "when any a in names: any b in names: a == alert.name and b == alert.name {\n" +
    '  notify(reason: routine, channel: "#never")\n' +
    "}\n"
  );
}

export const SECOND = 1_000;
export const MINUTE = 60 * SECOND;
export const HOUR = 60 * MINUTE;

/** now shifted by ms, which may be negative. */
export function ago(now: Date, ms: number): Date {
  return new Date(now.getTime() - ms);
}
