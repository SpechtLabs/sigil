// The HTTP API's JSON shapes, field for field the Go service's
// internal/server/api.go, including which fields are left out when empty.
// The console imports these types rather than copying them.

import type { ErrorResponse } from "../errors";

/**
 * The status of one alert in a WebhookResponse. Every firing alert ends
 * routed, unowned, invalid, failed or dispatch_failed, and each of those
 * ends in a notification attempt; a resolved alert is only acknowledged.
 */
export type AlertStatus =
  /** The owning team's policy decided it. */
  | "routed"
  /** It stopped firing. It isn't evaluated. */
  | "resolved"
  /** No team label, or a team the directory doesn't list: the kind's default, unevaluated. */
  | "unowned"
  /** The router can't read it (no name, an unknown severity or status): the kind's default. */
  | "invalid"
  /**
   * Its evaluation failed, ran out of time, or never ran before the batch
   * deadline: the platform's page if platform.paging pages for it, the
   * kind's default otherwise.
   */
  | "failed"
  /**
   * Decided, but the notifier failed to deliver it. Not in the Go service:
   * the webhook answers 503 so Alertmanager retries.
   */
  | "dispatch_failed";

/** The body of POST /api/v1/teams/{team}/route: one firing alert of the team in the path. */
export interface RouteRequest {
  alert: AlertRequest;
}

/** One alert as the route endpoint receives it. */
export interface AlertRequest {
  name: string;
  severity: string;
  labels: Record<string, string>;
  /** How long the alert has been firing, a duration string such as "12m". */
  firing_for: string;
}

/**
 * How one alert was routed: the decision the dispatcher acts on, where it
 * goes, and the trace that explains it. When the evaluation failed, the
 * decision fields hold the fallback, and error says what went wrong.
 */
export interface RouteResponse {
  /** The owning team, left out when no team in the directory owns the alert. */
  team?: string;
  /**
   * The team's root policy, <team>.alerts, left out when no policy ran: an
   * unowned or invalid alert, or one skipped by the batch deadline. (The Go
   * service wrote "" for an unowned alert and the policy for an invalid one.)
   */
  policy?: string;
  decision: string;
  reason: string;
  /** Whom a page goes to, for a page only. */
  target?: string;
  /** Where a notification is posted, for notify only. */
  channel?: string;
  /** Every candidate the policy produced, the winner marked; empty when no rule fired or no policy ran. */
  trace: CandidateResult[];
  error?: ErrorResponse;
  asserts?: AssertResult[];
  conflict?: ConflictResult;
}

/** The body of POST /api/v1/alerts. */
export interface WebhookResponse {
  received: number;
  /** How many alerts a team's policy routed: status "routed" only. */
  routed: number;
  results: AlertResult[];
}

/**
 * One alert of a webhook: which alert, its status, and for a firing alert the
 * RouteResponse fields, inline (none for a resolved one).
 */
export interface AlertResult extends Partial<Omit<RouteResponse, "error">> {
  fingerprint: string;
  alertname: string;
  status: AlertStatus;
  /**
   * Why the policy didn't route the alert, as a plain message so a batch
   * stays readable; it replaces RouteResponse's structured error, and
   * asserts and conflict still explain a failure in full.
   */
  error?: string;
}

/** The candidates that can't fire together. */
export interface ConflictResult {
  candidates: CandidateResult[];
}

/** A decision's payload by field name, durations as strings. */
export type Payload = Record<string, unknown>;

/** One decision constructor that fired, as the trace reports it. */
export interface CandidateResult {
  decision: string;
  reason: string;
  /** The policy the constructor is written in. */
  policy: string;
  /** The constructor's position with the call chain that reached it, like `checkout/alerts.sigil:8:1 → platform/routing.sigil:10:5`. */
  location: string;
  /** The conditions that held on the way, outermost first. */
  conditions?: string[];
  payload: Payload;
  winner: boolean;
}

/** One assert that didn't hold. */
export interface AssertResult {
  reason: string;
  policy: string;
  location: string;
  /** Set when the assert couldn't be checked because its condition raised a runtime error. */
  cause?: string;
}

/** The body of GET /api/v1/policies and of a successful reload. */
export interface PoliciesResponse {
  kinds: KindPolicies[];
}

/** One kind's loaded bundle. */
export interface KindPolicies {
  kind: string;
  version: number;
  /** RFC 3339 with nanoseconds, UTC. */
  loaded_at: string;
  /** The directory the bundle was read from, or "embedded". */
  source: string;
  fingerprint: string;
  policies: PolicyResponse[];
}

export interface PolicyResponse {
  team: string;
  policy: string;
}

/** The body of GET /api/v1/teams, sorted by name. */
export interface TeamsResponse {
  teams: { name: string; oncall: string; channel: string }[];
}

/** The body of the health endpoints. */
export interface StatusResponse {
  /** ok for /healthz; ready or not ready for /readyz. */
  status: string;
  loaded_at?: string;
}

/** The body of every error that comes without a decision. */
export interface ErrorEnvelope {
  error: ErrorResponse;
}

/** One source file as GET /api/v1/policies/files serves it. */
export interface FileResponse {
  path: string;
  source: string;
}

/**
 * The body of GET /api/v1/policies/files, for the console's in-browser
 * preview: exactly what the server compiles. Not in the Go service.
 */
export interface PolicyFilesResponse {
  /** The kind file, as the server's kind exports it. */
  kind: FileResponse;
  /** The platform's trusted documents. */
  platform: FileResponse[];
  /** The team bundle that serves. */
  teams: FileResponse[];
  /** The policy every team root must invoke, from the platform documents. */
  required: string;
  /** Each team's root policy. */
  roots: PolicyResponse[];
  fingerprint: string;
  source: string;
  loaded_at: string;
  /** The last reload that failed after the bundle that serves loaded; absent when none did. */
  last_error?: ReloadError;
}

/** A reload that was rejected. */
export interface ReloadError {
  at: string;
  trigger: string;
  error: ErrorResponse;
}
