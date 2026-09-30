// The JSON the console reads. The API's own shapes come from the modules
// that define them (type-only, so nothing of the server reaches the
// browser); what's here is the console's view of them.

import type { Explanation } from "@spechtlabs/sigil";
import type { AlertSource, HistoryEntry, ReloadEvent } from "../dispatch/history";
import type { ErrorResponse } from "../errors";
import type { AlertStatus, RouteResponse } from "../http/wire";

export type { ExplainEntry, Explanation } from "@spechtlabs/sigil";
export type { HistoryEntry, HistoryEvent, ReloadEvent } from "../dispatch/history";
export type { ErrorResponse } from "../errors";
export type {
  AlertResult,
  AlertStatus,
  AssertResult,
  CandidateResult,
  ConflictResult,
  ErrorEnvelope,
  KindPolicies,
  PoliciesResponse,
  PolicyFilesResponse,
  ReloadError,
  RouteRequest,
  RouteResponse,
  TeamsResponse,
  WebhookResponse,
} from "../http/wire";

export type Team = { name: string; oncall: string; channel: string };

/** The body of GET /api/v1/history. */
export interface HistoryResponse {
  entries: HistoryEntry[];
}

/**
 * One history entry as the console's pages read it: flat, with the result
 * as a RouteResponse whatever endpoint the alert came through.
 */
export interface FeedItem {
  /** The entry's id as text, for keys and links. */
  id: string;
  at: string;
  source: AlertSource;
  status: Exclude<AlertStatus, "resolved">;
  /** Empty for an alert posted to the route endpoint. */
  fingerprint: string;
  alertname: string;
  severity: string;
  labels: Record<string, string>;
  firing_for?: string;
  /** The team label the alert carried, which names the team an unowned alert asked for. */
  team_label?: string;
  response: RouteResponse;
  /** The paged target, the channel, or "-" for a drop. */
  destination: string;
  /** How the notification went: sent, a duplicate, failed, late (still going when the webhook answered) or skipped. */
  delivery: HistoryEntry["notification"]["status"];
  /** Why the notifier couldn't deliver it, when it couldn't. */
  notify_error?: string;
  trace_id?: string;
}

/** How the policy store stands: what serves, and the reload that failed since, if one did. */
export interface PolicyStatus {
  fingerprint?: string;
  loaded_at?: string;
  source?: string;
  last_error?: { at: string; trigger: string; error: ErrorResponse };
  /** The latest load attempt the event stream reported. */
  last_attempt?: ReloadEvent;
}

/** One team's policy explained, as the policies page shows it. */
export interface TeamExplanation {
  team: string;
  policy: string;
  explanation: Explanation;
}
