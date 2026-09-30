// The browser preview's answer in the server's shape. The preview evaluates
// with @spechtlabs/sigil in a worker and gets the CLI's JSON (EvalResult);
// rendering it as a RouteResponse, the way the server renders the same
// evaluation, lets one component show both and lets the page compare them.

import type { EvalEntry, EvalResult, JsonValue } from "@spechtlabs/sigil";
import { type AlertFormValues, alertLabels } from "./alert-form";
import type { AlertResult, CandidateResult, PolicyFilesResponse, RouteResponse, Team } from "./api-types";

/** The kind's alert input, in its JSON form: firing_for is a duration string. */
export interface RoutingAlert {
  [key: string]: JsonValue;
  name: string;
  severity: string;
  labels: Record<string, string>;
  firing_for: string;
}

/** The input the AlertRouting kind takes, in its JSON form. */
export interface RoutingInput {
  [key: string]: JsonValue;
  alert: RoutingAlert;
  team: { name: string; oncall: string; channel: string };
}

/** The alert of the form, with the labels the policy sees for it. */
export function routingAlert(values: AlertFormValues): RoutingAlert {
  return { name: values.name, severity: values.severity, labels: alertLabels(values), firing_for: values.firingFor };
}

/** The kind's input: the alert and the team that owns it. */
export function routingInput(alert: RoutingAlert, team: Team): RoutingInput {
  return { alert, team: { name: team.name, oncall: team.oncall, channel: team.channel } };
}

/** What the preview compiles: the served files, and each team's root policy. */
export interface PreviewBundle {
  fingerprint: string;
  /** The kind file and the team bundle. */
  files: { path: string; source: string }[];
  /** The platform's documents, which the required policy is read from. */
  trusted: { path: string; source: string }[];
  /** The policy every team root must invoke, platform.paging. */
  required: string;
  policies: { team: string; policy: string }[];
}

/**
 * The files the server compiled, under the paths it compiled them with, so
 * the preview's trace positions read the same as the server's, and the
 * guardrail the server compiles with: the required policy comes from the
 * platform's trusted documents, never from the team bundle.
 */
export function previewBundle(res: PolicyFilesResponse): PreviewBundle {
  return {
    fingerprint: res.fingerprint,
    files: [res.kind, ...res.teams],
    trusted: res.platform,
    required: res.required,
    policies: res.roots,
  };
}

/** Renders an evaluation of team's policy like the server's newRouteResponse. */
export function routeResponseFromEval(team: string, res: EvalResult): RouteResponse {
  const decision = res.decision ?? "";
  const payload = res.payload ?? {};
  const out: RouteResponse = {
    team,
    policy: res.policy,
    decision,
    reason: res.reason ?? "",
    trace: res.trace.map(candidateFromEntry),
  };
  if (decision === "page" && typeof payload.target === "string") out.target = payload.target;
  if (decision === "notify" && typeof payload.channel === "string") out.channel = payload.channel;
  if (res.error !== undefined) {
    out.error = { message: res.error.message, ...(res.error.help ? { advice: [res.error.help] } : {}) };
    if (res.error.asserts !== undefined) {
      out.asserts = res.error.asserts.map((a) => ({
        reason: a.reason,
        policy: a.policy,
        location: a.position,
        ...(a.cause !== undefined ? { cause: a.cause } : {}),
      }));
    }
    if (res.error.candidates !== undefined) {
      out.conflict = { candidates: res.error.candidates.map((c) => ({ ...candidateFromEntry(c), winner: false })) };
    }
  }
  return out;
}

/**
 * One trace entry as the server renders a candidate: the call chain and the
 * position joined into one location, outermost first, the way Go's
 * Candidate.Location prints it.
 */
export function candidateFromEntry(e: EvalEntry): CandidateResult {
  const location = [...(e.chain ?? []), ...(e.position !== undefined ? [e.position] : [])].join(" → ");
  return {
    decision: e.decision,
    reason: e.reason,
    policy: e.policy ?? "",
    location,
    ...(e.conditions !== undefined && e.conditions.length > 0 ? { conditions: e.conditions } : {}),
    payload: e.payload ?? {},
    winner: e.outcome === true,
  };
}

/**
 * The kind's default for an alert no policy decides, an unowned one: what
 * the server's fallbackResponse answers, without an evaluation.
 */
export function unownedResponse(): RouteResponse {
  return { decision: "notify", reason: "unrouted", channel: "#alerts", trace: [] };
}

/**
 * The team's own decision when platform.paging's page replaced it: the
 * result pages, but the trace's winner, the team's candidate, doesn't.
 * Undefined when the outcome is the team's.
 */
export function platformOverride(r: RouteResponse): CandidateResult | undefined {
  if (r.decision !== "page") return undefined;
  const winner = r.trace.find((c) => c.winner);
  if (winner === undefined) return undefined;
  const same = winner.decision === r.decision && winner.reason === r.reason && winner.payload.target === r.target;
  return same ? undefined : winner;
}

/** One firing alert of a webhook's response as a RouteResponse; its plain error becomes a message. */
export function routeResponseFromAlertResult(r: AlertResult): RouteResponse {
  const { fingerprint: _f, alertname: _a, status: _s, error, ...route } = r;
  return {
    ...route,
    decision: route.decision ?? "",
    reason: route.reason ?? "",
    trace: route.trace ?? [],
    ...(error !== undefined ? { error: { message: error } } : {}),
  };
}

/** Where a routed alert goes: the paged target, the channel, or "-" for a drop. */
export function destinationOf(r: Pick<RouteResponse, "target" | "channel">): string {
  return r.target || r.channel || "-";
}

/** The fields on which the preview and the server must agree. */
export interface Agreement {
  agrees: boolean;
  /** The fields that differ, by name. */
  differences: ("decision" | "reason" | "destination")[];
}

/** Compares the preview's answer with the server's, which is authoritative. */
export function compareOutcomes(preview: RouteResponse, server: RouteResponse): Agreement {
  const differences: Agreement["differences"] = [];
  if (preview.decision !== server.decision) differences.push("decision");
  if (preview.reason !== server.reason) differences.push("reason");
  if (destinationOf(preview) !== destinationOf(server)) differences.push("destination");
  return { agrees: differences.length === 0, differences };
}
