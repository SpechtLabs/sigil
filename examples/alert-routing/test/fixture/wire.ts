// The API's wire types as a client sees them. The suites keep their own
// types instead of importing the service's: durations travel as strings
// ("12m"), and a black-box test should break when the wire format changes,
// not follow it silently.

/** The body of POST /api/v1/teams/:team/route. */
export interface RouteRequest {
  alert: Alert;
}

/** One alert as the single-alert endpoint takes it. firing_for is a duration string. */
export interface Alert {
  name: string;
  severity: string;
  labels: Record<string, string>;
  firing_for: string;
}

/** The body of POST /api/v1/alerts: Alertmanager's webhook payload, version 4. */
export interface Webhook {
  version: string;
  groupKey: string;
  truncatedAlerts: number;
  status: string;
  receiver: string;
  groupLabels: Record<string, string>;
  commonLabels: Record<string, string>;
  commonAnnotations: Record<string, string>;
  externalURL: string;
  alerts: WebhookAlert[];
}

/** One alert of a webhook batch. The router reads its labels and how long ago startsAt was. */
export interface WebhookAlert {
  status: string;
  labels: Record<string, string>;
  annotations: Record<string, string>;
  startsAt: string;
  endsAt: string;
  generatorURL: string;
  fingerprint: string;
}

/**
 * The body of a single-alert route, including the 422/500/503 of an
 * evaluation that failed or ran out of time, which carries the fallback.
 */
export interface RouteResponse {
  team?: string;
  /** The team's root policy; left out when no policy ran (an unowned or invalid alert, or one skipped). */
  policy?: string;
  decision: string;
  reason: string;
  target?: string;
  channel?: string;
  trace: TraceEntry[] | null;
  error?: ErrorBody;
  asserts?: AssertEntry[];
  conflict?: Conflict;
}

/** The body of a processed webhook: how many alerts arrived, how many a policy routed, and each result. */
export interface WebhookResponse {
  received: number;
  routed: number;
  results: AlertResult[];
}

/**
 * What became of one alert of a batch. error is a plain message here, set
 * for an unowned, invalid or failed alert; a resolved alert carries none of
 * the route fields.
 */
export interface AlertResult {
  fingerprint: string;
  alertname: string;
  status: string;
  error?: string;
  team?: string;
  policy?: string;
  decision?: string;
  reason?: string;
  target?: string;
  channel?: string;
  trace?: TraceEntry[] | null;
  asserts?: AssertEntry[];
  conflict?: Conflict;
}

/** The candidates the kind says can't stand together. */
export interface Conflict {
  candidates: TraceEntry[];
}

/** One candidate of the trace. */
export interface TraceEntry {
  decision: string;
  reason: string;
  policy: string;
  location: string;
  conditions?: string[];
  payload: Record<string, unknown>;
  winner: boolean;
}

/** One assert that didn't hold. */
export interface AssertEntry {
  reason: string;
  policy: string;
  location: string;
  cause?: string;
}

/** A humane error as the API renders it. */
export interface ErrorBody {
  message: string;
  advice?: string[];
  cause?: ErrorBody;
}

/** The body of every error status that has no decision to report. */
export interface ErrorResponse {
  error?: ErrorBody;
}

/** The body of GET /api/v1/policies and of a successful reload. */
export interface PoliciesResponse {
  kinds: KindPolicies[];
}

/** What one kind's bundle holds right now. */
export interface KindPolicies {
  kind: string;
  version: number;
  loaded_at: string;
  source: string;
  fingerprint: string;
  policies: PolicyRef[];
}

/** One served root policy and the team it serves. */
export interface PolicyRef {
  team: string;
  policy: string;
}

/** The body of GET /api/v1/teams. */
export interface TeamsResponse {
  teams: Team[];
}

/** One entry of the team directory. */
export interface Team {
  name: string;
  oncall: string;
  channel: string;
}

/** The trace entries marked as the outcome. */
export function winners(trace: TraceEntry[] | null | undefined): TraceEntry[] {
  return (trace ?? []).filter((c) => c.winner);
}

/** The AlertRouting entry of a listing, or undefined when it has none. */
export function routing(list: PoliciesResponse): KindPolicies | undefined {
  return list.kinds.find((k) => k.kind === KIND_ROUTING);
}

/** The result for the alert with fingerprint, or undefined. */
export function result(out: WebhookResponse, fingerprint: string): AlertResult | undefined {
  return out.results.find((r) => r.fingerprint === fingerprint);
}

/**
 * The message of the error and of every cause below it, so a spec can look
 * for a diagnostic wherever in the chain the server put it.
 */
export function messages(err: ErrorBody | undefined): string[] {
  const out: string[] = [];
  for (let cur = err; cur !== undefined; cur = cur.cause) out.push(cur.message);
  return out;
}

/** The part of a webhook result a single-alert route answers with, so one expectation checks both. */
export function asRoute(r: AlertResult): RouteResponse {
  return {
    team: r.team,
    policy: r.policy,
    decision: r.decision ?? "",
    reason: r.reason ?? "",
    target: r.target,
    channel: r.channel,
    trace: r.trace ?? null,
    conflict: r.conflict,
  };
}

/** The name of the kind every policy implements. */
export const KIND_ROUTING = "AlertRouting";

/** Makes a team's root policy name: team checkout is routed by checkout.alerts. */
export const ROOT_SUFFIX = ".alerts";
