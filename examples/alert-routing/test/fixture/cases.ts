// The tables both suites run: every route the shipped policies can reach,
// the bodies the service must refuse, a webhook with every result status,
// and the sample requests under requests/ with what cases.json expects.
// The integration and end-to-end suites run the same cases, so the
// in-process server and the container can't disagree.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  ago,
  alertLabels,
  CHECKOUT_CHANNEL,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  CHECKOUT_MUTED,
  CHECKOUT_ONCALL,
  DECISION_DROP,
  DECISION_NOTIFY,
  DECISION_PAGE,
  DEFAULT_CHANNEL,
  ENV_STAGING,
  EXAMPLES_DIR,
  env,
  firing,
  firingAlert,
  firingFor,
  HOUR,
  jsonWithField,
  LABEL_ALERTNAME,
  LABEL_COMPONENT,
  LABEL_ENV,
  label,
  MINUTE,
  newWebhook,
  noLabel,
  PAYMENTS_CHANNEL,
  PAYMENTS_MUTED,
  PAYMENTS_ONCALL,
  REASON_CRITICAL_ALERT,
  REASON_MUTED,
  REASON_NOT_PRODUCTION,
  REASON_ROUTINE,
  REASON_SUSTAINED,
  REASON_UNROUTED,
  resolved,
  SEVERITY_CRITICAL,
  SEVERITY_INFO,
  SEVERITY_WARNING,
  STATUS_INVALID,
  STATUS_RESOLVED,
  STATUS_ROUTED,
  STATUS_UNOWNED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "./requests";
import type { RouteRequest, Webhook } from "./wire";

/**
 * A route as a spec expects it: the decision, the reason, and the target of
 * a page or the channel of a notification, undefined otherwise.
 */
export interface Route {
  decision: string;
  reason: string;
  target?: string;
  channel?: string;
}

/** One alert sent to one team's route endpoint and the route the team's policy must choose. */
export interface RouteCase {
  name: string;
  team: string;
  request: RouteRequest;
  want: Route;
}

/** A body the service must refuse before evaluating anything, and the status it must refuse it with. */
export interface BadRequestCase {
  name: string;
  body: string;
  status: number;
}

/**
 * One alert of a webhook batch and what must become of it. A resolved alert
 * needs no route and names no team.
 */
export interface ResultCase {
  fingerprint: string;
  status: string;
  /** The owning team the result names, undefined for an unowned alert. */
  team?: string;
  want?: Route;
}

/** A webhook and what must become of each of its alerts. */
export interface Batch {
  webhook: Webhook;
  results: ResultCase[];
}

/** The kind's default: nothing matched, or no policy could decide, and the alert still reaches #alerts. */
export const UNROUTED: Route = { decision: DECISION_NOTIFY, reason: REASON_UNROUTED, channel: DEFAULT_CHANNEL };

const checkoutCritical: Route = { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: CHECKOUT_ONCALL };
const checkoutSustained: Route = { decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: CHECKOUT_ONCALL };
const paymentsCritical: Route = { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: PAYMENTS_ONCALL };
const paymentsSustained: Route = { decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: PAYMENTS_ONCALL };
const muted: Route = { decision: DECISION_DROP, reason: REASON_MUTED };
const notProduction: Route = { decision: DECISION_DROP, reason: REASON_NOT_PRODUCTION };

/** When the alerts of the bad requests started. The service refuses the whole payload first, so any time does. */
const badRequestStart = new Date("2026-09-28T12:00:00Z");

/**
 * Every decision and reason the checkout and payments policies can reach,
 * and the combinations whose outcome says something about the kind: a page
 * beats a drop, so muting never silences a page, and the team's own
 * threshold decides when a warning becomes one.
 */
export function routeCases(): RouteCase[] {
  return [...checkoutCases(), ...paymentsCases()];
}

/**
 * Bodies the route endpoint must refuse: 400 for a body that isn't a
 * request, and 422 for a request whose alert the kind can't represent.
 */
export function routeBadRequestCases(): BadRequestCase[] {
  const valid = firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING);
  const json = (r: RouteRequest) => JSON.stringify(r);
  return [
    { name: "malformed JSON", body: `{"alert": {"name": "CheckoutLatencyHigh"`, status: 400 },
    { name: "an unknown field in the alert", body: jsonWithField(valid, "team", TEAM_PAYMENTS), status: 400 },
    {
      name: "a field outside the alert",
      body: `{"alert": ${JSON.stringify(valid.alert)}, "severity": "critical"}`,
      status: 400,
    },
    {
      name: "a duration that doesn't parse",
      body: json(firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("twelve minutes"))),
      status: 400,
    },
    {
      name: "a number where a duration belongs",
      body: `{"alert": {"name": "CheckoutLatencyHigh", "severity": "warning", "labels": {}, "firing_for": 720000000000}}`,
      status: 400,
    },
    { name: "a severity the kind doesn't declare", body: json(firingAlert(CHECKOUT_LATENCY, "urgent")), status: 422 },
    { name: "a severity in the wrong case", body: json(firingAlert(CHECKOUT_LATENCY, "Critical")), status: 422 },
    { name: "no severity", body: json(firingAlert(CHECKOUT_LATENCY, "")), status: 422 },
    { name: "no name", body: json(firingAlert("", SEVERITY_WARNING)), status: 422 },
    {
      name: "a negative firing time",
      body: json(firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("-5m"))),
      status: 422,
    },
  ];
}

/**
 * Webhook bodies the service must refuse with 400 as a whole, before routing
 * any alert. Alertmanager never retries a 4xx, so the group is dropped for
 * good: only a body that isn't a webhook at all earns one. An alert with a
 * status it doesn't know or a batch over the limit is routed instead (see
 * the lenient webhook specs), unlike the Go service, which refused both.
 */
export function webhookBadRequestCases(): BadRequestCase[] {
  const wrongVersion = newWebhook(
    firing("a1", alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), badRequestStart),
  );
  wrongVersion.version = "3";

  return [
    { name: "malformed JSON", body: `{"version": "4", "alerts": [`, status: 400 },
    { name: "a payload version other than 4", body: JSON.stringify(wrongVersion), status: 400 },
    { name: "a JSON value that isn't an object", body: `["version", "4"]`, status: 400 },
  ];
}

/**
 * A webhook with one alert for every status a result can have, sent at now,
 * and what must become of each. Every firing alert ends in a route, the
 * ones no policy decided included: those take the kind's default, so no
 * alert is ever silently lost.
 *
 * tag, when set, is appended to every fingerprint. alertrouter dispatches
 * idempotently by fingerprint for a while, so a spec that sends the batch to
 * a service that has seen it before (the compose stack) tags it to have
 * every notification go out again.
 */
export function mixedBatch(now: Date, tag = ""): Batch {
  return retag(untaggedMixedBatch(now), tag);
}

/** b with tag appended to every fingerprint, or b itself for no tag. */
export function retag(b: Batch, tag: string): Batch {
  if (tag === "") return b;
  return {
    webhook: { ...b.webhook, alerts: b.webhook.alerts.map((a) => ({ ...a, fingerprint: `${a.fingerprint}-${tag}` })) },
    results: b.results.map((r) => ({ ...r, fingerprint: `${r.fingerprint}-${tag}` })),
  };
}

/** A short random tag for {@link mixedBatch}. */
export function uniqueTag(): string {
  return crypto.randomUUID().slice(0, 8);
}

function untaggedMixedBatch(now: Date): Batch {
  const minuteAgo = ago(now, MINUTE);
  return {
    webhook: newWebhook(
      firing("checkout-critical", alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
      firing("payments-warning", alertLabels(TEAM_PAYMENTS, "PaymentsLatencyHigh", SEVERITY_WARNING), minuteAgo),
      firing(
        "checkout-staging",
        alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL, LABEL_ENV, ENV_STAGING),
        minuteAgo,
      ),
      firing("payments-sustained", alertLabels(TEAM_PAYMENTS, "PaymentsLatencyHigh", SEVERITY_WARNING), ago(now, HOUR)),
      resolved("checkout-resolved", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), now),
      firing("no-team", alertLabels("", "NodeDiskFull", SEVERITY_CRITICAL), minuteAgo),
      firing("unknown-team", alertLabels("marketing", "CampaignBounceRate", SEVERITY_WARNING), minuteAgo),
      firing("bad-severity", alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, "urgent"), minuteAgo),
      firing("no-name", alertLabels(TEAM_PAYMENTS, "", SEVERITY_CRITICAL, LABEL_ALERTNAME, ""), minuteAgo),
    ),
    results: [
      { fingerprint: "checkout-critical", status: STATUS_ROUTED, team: TEAM_CHECKOUT, want: checkoutCritical },
      {
        fingerprint: "payments-warning",
        status: STATUS_ROUTED,
        team: TEAM_PAYMENTS,
        want: { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: PAYMENTS_CHANNEL },
      },
      { fingerprint: "checkout-staging", status: STATUS_ROUTED, team: TEAM_CHECKOUT, want: notProduction },
      { fingerprint: "payments-sustained", status: STATUS_ROUTED, team: TEAM_PAYMENTS, want: paymentsSustained },
      { fingerprint: "checkout-resolved", status: STATUS_RESOLVED },
      { fingerprint: "no-team", status: STATUS_UNOWNED, want: UNROUTED },
      { fingerprint: "unknown-team", status: STATUS_UNOWNED, want: UNROUTED },
      { fingerprint: "bad-severity", status: STATUS_INVALID, team: TEAM_CHECKOUT, want: UNROUTED },
      // Its team and severity are readable, so platform.paging still pages a
      // critical one: an alert rule without a name can't silence a page.
      { fingerprint: "no-name", status: STATUS_INVALID, team: TEAM_PAYMENTS, want: paymentsCritical },
    ],
  };
}

/** How many results of the batch are for firing alerts, each of which must end in exactly one notification. */
export function firingCount(b: Batch): number {
  return b.results.filter((r) => r.status !== STATUS_RESOLVED).length;
}

// requests/cases.json, typed after the Go manifest it was written for.

/** A routing decision as the wire carries it. */
export interface Outcome {
  decision?: string;
  reason?: string;
  target?: string;
  channel?: string;
}

/** One sample request and what alertrouter must answer to it. */
export interface ManifestCase {
  name: string;
  file: string;
  kind: "route" | "webhook";
  /** The team a route request is sent for; empty for a webhook. */
  team?: string;
  description: string;
  /** The status alertrouter answers with; 200 when left out. */
  status?: number;
  expect: Outcome & {
    received?: number;
    routed?: number;
    results?: (Outcome & { fingerprint: string; alertname: string; status: string; team?: string })[];
  };
}

/** requests/, which demo scripts, the README's curl examples and k6 send. */
export const REQUESTS_DIR = join(EXAMPLES_DIR, "requests");

/** Every case of requests/cases.json, in the manifest's order. */
export function manifestCases(): ManifestCase[] {
  return JSON.parse(readFileSync(join(REQUESTS_DIR, "cases.json"), "utf8")) as ManifestCase[];
}

/** The case's request body, byte for byte. */
export function manifestBody(c: ManifestCase): string {
  return readFileSync(join(REQUESTS_DIR, c.file), "utf8");
}

/** The spec's description of a manifest case: its name and what it shows. */
export function manifestDescription(c: ManifestCase): string {
  return c.description === "" ? c.name : `${c.name}: ${c.description}`;
}

/** The route an outcome of the manifest names. */
export function outcomeRoute(o: Outcome): Route {
  return route(o.decision ?? "", o.reason ?? "", o.target, o.channel);
}

/** A route with its empty fields left out, the shape the specs compare. */
export function route(decision: string, reason: string, target?: string, channel?: string): Route {
  const r: Route = { decision, reason };
  if (target) r.target = target;
  if (channel) r.channel = channel;
  return r;
}

// checkout's routes: the platform's paging and routing with a ten-minute
// threshold and one muted alert, and the team's own channel for payments
// info alerts.
function checkoutCases(): RouteCase[] {
  const team = TEAM_CHECKOUT;
  return [
    {
      name: "a critical production alert pages checkout's on-call",
      team,
      request: firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL),
      want: checkoutCritical,
    },
    {
      name: "a fresh warning goes to checkout's channel",
      team,
      request: firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("4m")),
      want: { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: CHECKOUT_CHANNEL },
    },
    {
      name: "a warning that has fired for checkout's ten minutes pages",
      team,
      request: firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("10m")),
      want: checkoutSustained,
    },
    {
      name: "a muted warning is dropped",
      team,
      request: firingAlert(CHECKOUT_MUTED, SEVERITY_WARNING, firingFor("4m")),
      want: muted,
    },
    {
      // A page beats a drop, so the mute only silences the notification.
      name: "muting never silences a sustained page",
      team,
      request: firingAlert(CHECKOUT_MUTED, SEVERITY_WARNING, firingFor("12m")),
      want: checkoutSustained,
    },
    {
      name: "muting never silences a critical page",
      team,
      request: firingAlert(CHECKOUT_MUTED, SEVERITY_CRITICAL),
      want: checkoutCritical,
    },
    {
      name: "a critical staging alert is dropped",
      team,
      request: firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL, env(ENV_STAGING)),
      want: notProduction,
    },
    {
      // Both drops fire, and the kind ranks muted first.
      name: "a muted staging alert is dropped as muted",
      team,
      request: firingAlert(CHECKOUT_MUTED, SEVERITY_WARNING, env(ENV_STAGING)),
      want: muted,
    },
    {
      // Only a known pre-production env drops: a rule that forgot the
      // label must fail loud, not silently.
      name: "a critical alert without an env label pages",
      team,
      request: firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL, noLabel(LABEL_ENV)),
      want: checkoutCritical,
    },
    {
      name: "a critical alert with an env the platform doesn't know pages",
      team,
      request: firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL, env("prod")),
      want: checkoutCritical,
    },
    {
      name: "a dev alert is dropped like a staging one",
      team,
      request: firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL, env("dev")),
      want: notProduction,
    },
    {
      name: "a fresh warning without an env label goes to checkout's channel",
      team,
      request: firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, noLabel(LABEL_ENV)),
      want: { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: CHECKOUT_CHANNEL },
    },
    {
      name: "payments info goes to checkout's payments channel",
      team,
      request: firingAlert("PaymentsRetryRate", SEVERITY_INFO, label(LABEL_COMPONENT, "payments")),
      want: { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: "#checkout-payments" },
    },
    {
      name: "any other info alert falls back to #alerts",
      team,
      request: firingAlert("CheckoutPodRestarted", SEVERITY_INFO),
      want: UNROUTED,
    },
  ];
}

// payments' routes: the same platform policies with a five-minute
// threshold, and the team's own channel for ledger info alerts.
function paymentsCases(): RouteCase[] {
  const team = TEAM_PAYMENTS;
  return [
    {
      name: "a critical production alert pages payments' on-call",
      team,
      request: firingAlert("PaymentsErrorRate", SEVERITY_CRITICAL),
      want: paymentsCritical,
    },
    {
      name: "a fresh warning goes to payments' channel",
      team,
      request: firingAlert("PaymentsLatencyHigh", SEVERITY_WARNING, firingFor("4m")),
      want: { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: PAYMENTS_CHANNEL },
    },
    {
      // Six minutes is sustained for payments and fresh for checkout.
      name: "a warning pages after payments' shorter five minutes",
      team,
      request: firingAlert("PaymentsLatencyHigh", SEVERITY_WARNING, firingFor("6m")),
      want: paymentsSustained,
    },
    {
      name: "payments' muted warning is dropped",
      team,
      request: firingAlert(PAYMENTS_MUTED, SEVERITY_WARNING, firingFor("4m")),
      want: muted,
    },
    {
      name: "a staging warning is dropped",
      team,
      request: firingAlert("PaymentsLatencyHigh", SEVERITY_WARNING, env(ENV_STAGING)),
      want: notProduction,
    },
    {
      name: "ledger info goes to payments' ledger channel",
      team,
      request: firingAlert("LedgerReconciliationLag", SEVERITY_INFO, label(LABEL_COMPONENT, "ledger")),
      want: { decision: DECISION_NOTIFY, reason: REASON_ROUTINE, channel: "#payments-ledger" },
    },
    {
      // The payments channel rule is checkout's, not payments'.
      name: "payments component info falls back to #alerts for payments",
      team,
      request: firingAlert("PaymentsRetryRate", SEVERITY_INFO, label(LABEL_COMPONENT, "payments")),
      want: UNROUTED,
    },
  ];
}
