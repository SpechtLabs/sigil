// The send-an-alert form's values and what the page builds from them: the
// route endpoint's request, an Alertmanager webhook carrying the same alert,
// and the labels the policy sees, which differ between the two because a
// webhook's alert carries its name, severity and team as labels too.

import type { RouteRequest } from "./api-types";

export const SEVERITIES = ["critical", "warning", "info"] as const;
export type Severity = (typeof SEVERITIES)[number];

/** The env label's values: the platform treats staging and dev as pre-production. */
export const ENVIRONMENTS = ["production", "staging", "dev"] as const;

/** The env select's value for an alert without an env label, which still pages. */
export const NO_ENV = "__none__";

/** Where the form sends the alert. */
export type SendMode = "route" | "webhook";

/** The team select's value for a webhook alert without a team label, which the router can't assign. */
export const NO_TEAM = "__none__";

export interface AlertFormValues {
  mode: SendMode;
  /** A team name from the directory, or {@link NO_TEAM} in webhook mode. */
  team: string;
  name: string;
  severity: Severity;
  env: string;
  /** Empty for none. */
  component: string;
  /** A Go duration string, such as "12m". */
  firingFor: string;
}

export const DEFAULT_VALUES: AlertFormValues = {
  mode: "route",
  team: "checkout",
  name: "CheckoutErrorRate",
  severity: "critical",
  env: "production",
  component: "",
  firingFor: "2m",
};

/** A field's problem, by field name, for the form to show next to it. */
export type FormErrors = Partial<Record<keyof AlertFormValues, string>>;

const UNIT_NS: Record<string, number> = {
  ns: 1,
  us: 1e3,
  µs: 1e3,
  μs: 1e3,
  ms: 1e6,
  s: 1e9,
  m: 60e9,
  h: 3600e9,
};

const DURATION = /^(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|µs|μs|ms|s|m|h)/;

/**
 * Parses a Go duration string ("1h30m", "90s", "0") into milliseconds, or
 * returns undefined when it isn't one. Negative durations aren't accepted:
 * the server refuses a negative firing_for too.
 */
export function parseDurationMs(s: string): number | undefined {
  if (s === "0") return 0;
  if (s === "") return undefined;
  let rest = s;
  let ns = 0;
  while (rest !== "") {
    const m = DURATION.exec(rest);
    if (m === null) return undefined;
    const token = m[0];
    const unit = /(ns|us|µs|μs|ms|s|m|h)$/.exec(token)?.[0] ?? "";
    ns += Number(token.slice(0, token.length - unit.length)) * (UNIT_NS[unit] ?? Number.NaN);
    rest = rest.slice(token.length);
  }
  return Number.isFinite(ns) ? ns / 1e6 : undefined;
}

/** Checks the values the way the server will, so a mistake shows before sending. */
export function validate(values: AlertFormValues, teams: readonly string[]): FormErrors {
  const errors: FormErrors = {};
  if (values.name.trim() === "") errors.name = "Give the alert a name, such as CheckoutErrorRate.";
  if (!(SEVERITIES as readonly string[]).includes(values.severity)) {
    errors.severity = `Pick one of ${SEVERITIES.join(", ")}.`;
  }
  if (parseDurationMs(values.firingFor) === undefined) {
    errors.firingFor = 'Use a Go duration such as "90s", "12m" or "1h30m".';
  }
  if (values.team === NO_TEAM ? values.mode !== "webhook" : !teams.includes(values.team)) {
    errors.team = "Pick a team from the directory.";
  }
  return errors;
}

/** The labels the policy sees for the alert. */
export function alertLabels(values: AlertFormValues): Record<string, string> {
  const labels: Record<string, string> = values.env === NO_ENV ? {} : { env: values.env };
  if (values.component !== "") labels.component = values.component;
  if (values.mode === "route") return labels;
  // A webhook alert's labels are all of Alertmanager's, the name, severity
  // and team included; the router passes every one of them to the policy.
  return {
    alertname: values.name,
    severity: values.severity,
    ...(values.team === NO_TEAM ? {} : { team: values.team }),
    ...labels,
  };
}

/** The body of POST /api/v1/teams/:team/route. */
export function routeRequest(values: AlertFormValues): RouteRequest {
  return {
    alert: {
      name: values.name,
      severity: values.severity,
      labels: alertLabels(values),
      firing_for: values.firingFor,
    },
  };
}

/**
 * An Alertmanager webhook (payload version 4) carrying the alert, firing
 * since firingFor before now; the router computes firing_for back from
 * startsAt. The fingerprint is random, as a new alert's would be.
 */
export function webhookBody(values: AlertFormValues, now: Date, fingerprint: string): Record<string, unknown> {
  const startsAt = new Date(now.getTime() - (parseDurationMs(values.firingFor) ?? 0));
  return {
    version: "4",
    groupKey: `{}:{alertname="${values.name}"}`,
    truncatedAlerts: 0,
    status: "firing",
    receiver: "alertrouter",
    groupLabels: { alertname: values.name },
    commonLabels: {},
    commonAnnotations: {},
    externalURL: "",
    alerts: [
      {
        status: "firing",
        labels: alertLabels(values),
        annotations: { summary: "sent from the alertrouter console" },
        startsAt: startsAt.toISOString(),
        endsAt: "0001-01-01T00:00:00Z",
        generatorURL: "",
        fingerprint,
      },
    ],
  };
}

/** A random 16-hex-digit fingerprint, the length Alertmanager's have. */
export function randomFingerprint(random: () => number = Math.random): string {
  let out = "";
  for (let i = 0; i < 16; i++) out += Math.floor(random() * 16).toString(16);
  return out;
}
