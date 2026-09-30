// Renders an evaluation as the API answers it, and classifies a failed one:
// whose fault it is (the HTTP status), the kind of failure (the metrics),
// and what to do about it (the error's advice). The Go service's
// render.go and the classify half of handlers.go.

import type { EvalEntry, EvalFailure, EvalResult, FailedAssert } from "@spechtlabs/sigil";

import { HumaneError } from "../errors";
import { DEFAULT_CHANNEL, Notify, Page, Unrouted } from "../routing/kind";
import type { ErrorKind } from "../telemetry/types";
import type { AssertResult, CandidateResult, ConflictResult, RouteResponse } from "./wire";

/**
 * The status of a request whose client went away before the answer: 499,
 * nginx's "client closed request". No client reads it; it keeps a client
 * that gave up apart from a client error and a server failure in the access
 * log and the request metrics.
 */
export const STATUS_CLIENT_CLOSED_REQUEST = 499;

/** How call-chain steps join in a location. */
const CHAIN_SEPARATOR = " → ";

/**
 * A failed evaluation, classified. kind is undefined for a failure that
 * isn't an evaluation error to count.
 */
export interface Failure {
  error: HumaneError;
  kind: ErrorKind | undefined;
  status: number;
  asserts?: AssertResult[];
  conflict?: ConflictResult;
}

/** Renders an evaluation of team's policy. */
export function routeResponse(team: string, policy: string, res: EvalResult): RouteResponse {
  const resp: RouteResponse = {
    team,
    policy,
    decision: res.decision ?? "",
    reason: res.reason ?? "",
    trace: traceCandidates(res),
  };
  const page = Page.match(res);
  if (page !== undefined && page.target !== "") resp.target = page.target;
  const note = Notify.match(res);
  if (note !== undefined) resp.channel = note.channel === "" ? DEFAULT_CHANNEL : note.channel;
  return resp;
}

/**
 * The kind's default, and no trace: the answer for an alert no policy
 * decided. team is empty when no team owns the alert; policy is given only
 * when the team's policy ran and failed without a result.
 */
export function fallbackResponse(team: string, policy?: string): RouteResponse {
  return ordered({
    ...(team === "" ? {} : { team }),
    ...(policy === undefined || policy === "" ? {} : { policy }),
    decision: Unrouted.decision,
    reason: Unrouted.reason,
    channel: DEFAULT_CHANNEL,
    trace: [],
  });
}

/**
 * The trace with the candidates that made the outcome marked. A constructor
 * can fire once per call chain, so each outcome entry claims the first
 * candidate that matches it and no more.
 */
export function traceCandidates(res: EvalResult): CandidateResult[] {
  const claimed = res.outcome.map(() => false);
  return res.trace.map((c) => {
    let winner = false;
    for (const [i, e] of res.outcome.entries()) {
      if (
        !claimed[i] &&
        e.decision === c.decision &&
        e.reason === c.reason &&
        e.policy === c.policy &&
        e.position === c.position
      ) {
        claimed[i] = true;
        winner = true;
        break;
      }
    }
    return candidateResult(c, winner);
  });
}

/** Renders one candidate. */
export function candidateResult(c: EvalEntry, winner: boolean): CandidateResult {
  const out: CandidateResult = {
    decision: c.decision,
    reason: c.reason,
    policy: c.policy ?? "",
    location: location(c.chain, c.position),
    payload: { ...(c.payload ?? {}) },
    winner,
  };
  if (c.conditions !== undefined && c.conditions.length > 0) {
    // Field order as Go's CandidateResult: conditions before payload.
    return {
      decision: out.decision,
      reason: out.reason,
      policy: out.policy,
      location: out.location,
      conditions: [...c.conditions],
      payload: out.payload,
      winner,
    };
  }
  return out;
}

/**
 * Classifies a failed evaluation of policy. Both endpoints answer through
 * it, so a failure has the same meaning wherever it happens:
 *
 * - A failed input assert is the caller's: the policy declared the input
 *   invalid before any rule ran. 422.
 * - A failed outcome assert, a conflict and a runtime error are the
 *   policy's: the input was acceptable, and what the policy made of it
 *   wasn't. 500.
 * - An evaluation past the evaluation timeout is alertrouter's: it didn't
 *   decide in time. 503, since alertrouter evaluates in process and has no
 *   upstream that could have timed out.
 *
 * fallback is the advice that says what the decision fields hold instead.
 */
export function classify(policy: string, failure: EvalFailure, res: EvalResult, fallback: string): Failure {
  const cause = new Error(failure.message);
  switch (failure.kind) {
    case "assertion": {
      const asserts = assertResults(policy, failure.asserts ?? []);
      const list = asserts.map((a) => a.reason).join(", ");
      if (assertPhase(failure, res) === "input") {
        return {
          kind: "assertion",
          status: 422,
          error: new HumaneError(
            `the alert fails ${policy}'s asserts: ${list}`,
            [fallback, "fix the alert so the asserts listed in asserts hold"],
            { cause },
          ),
          asserts,
        };
      }
      return {
        kind: "assertion",
        status: 500,
        error: new HumaneError(
          `the outcome of ${policy} fails its asserts: ${list}`,
          [
            fallback,
            "an outcome assert checks what the policy decided, which the alert can't change; tell the policy's owners",
          ],
          { cause },
        ),
        asserts,
      };
    }
    case "runtime":
      return {
        kind: "runtime",
        status: 500,
        error: new HumaneError(
          `${policy} can't be evaluated against this alert: ${runtimeText(failure.message)}`,
          [
            fallback,
            "a runtime error usually means the policy doesn't guard against this alert; tell the policy's owners",
          ],
          { cause },
        ),
      };
    case "conflict":
      return {
        kind: "conflict",
        status: 500,
        error: new HumaneError(
          `${policy} produced decisions that can't stand together: ${failure.message}`,
          [
            fallback,
            "a conflict is a defect in the policy, not in the alert, such as a team rule that pages someone else than the platform's; tell the policy's owners",
          ],
          { cause },
        ),
        conflict: { candidates: (failure.candidates ?? []).map((c) => candidateResult(c, false)) },
      };
    case "canceled":
      return {
        kind: "timeout",
        status: 503,
        error: new HumaneError(
          `${policy} wasn't decided within alertrouter's evaluation timeout`,
          [fallback, "if this alert keeps timing out, the policy is slow for its input: tell alertrouter's operators"],
          { cause },
        ),
      };
  }
  return {
    kind: undefined,
    status: 500,
    error: new HumaneError(
      `evaluating ${policy} failed`,
      [fallback, "if it keeps failing, check the alertrouter logs"],
      {
        cause,
      },
    ),
  };
}

/** The asserts of an assertion failure. */
export function assertResults(policy: string, asserts: readonly FailedAssert[]): AssertResult[] {
  return asserts.map((a) => {
    const out: AssertResult = {
      reason: a.reason,
      policy: a.policy === "" ? policy : a.policy,
      location: a.position,
    };
    if (a.cause !== undefined) out.cause = a.cause;
    return out;
  });
}

/**
 * Whether a failed assert was an input or an outcome assert. The module
 * reports the phase; for a module that doesn't, an outcome assert is the
 * one that read an outcome, or ran after a rule fired.
 */
function assertPhase(failure: EvalFailure, res: EvalResult): "input" | "outcome" {
  if (failure.phase !== undefined) return failure.phase;
  const readOutcome = (failure.asserts ?? []).some((a) => a.outcome !== undefined && a.outcome.length > 0);
  return readOutcome || res.trace.length > 0 ? "outcome" : "input";
}

/** A position with the call chain that reached it, outermost first. */
function location(chain: readonly string[] | undefined, position: string | undefined): string {
  return [...(chain ?? []), ...(position === undefined ? [] : [position])].join(CHAIN_SEPARATOR);
}

/**
 * The module writes a runtime error as "file:line:col: message"; the API,
 * like the Go service, as "message at file:line:col".
 */
function runtimeText(message: string): string {
  const m = /^(\S+:\d+:\d+): ([\s\S]*)$/.exec(message);
  return m === null ? message : `${m[2]} at ${m[1]}`;
}

/**
 * resp with its fields in the Go service's order (team, policy, decision,
 * reason, target, channel, trace, error, asserts, conflict), empty optional
 * fields left out, so the JSON reads the same as the Go service's.
 */
export function ordered<T extends Partial<RouteResponse>>(resp: T): T {
  const out: Record<string, unknown> = {};
  for (const key of ROUTE_FIELDS) if (resp[key] !== undefined) out[key] = resp[key];
  for (const [key, value] of Object.entries(resp)) if (!(key in out) && value !== undefined) out[key] = value;
  return out as T;
}

const ROUTE_FIELDS = [
  "team",
  "policy",
  "decision",
  "reason",
  "target",
  "channel",
  "trace",
  "error",
  "asserts",
  "conflict",
] as const satisfies readonly (keyof RouteResponse)[];
