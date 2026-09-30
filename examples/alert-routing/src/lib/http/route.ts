// Routing one firing alert, the path both endpoints share: decide it, deliver
// the decision, and report it (span, metrics, one "alert routed" log line,
// and the console's history).
//
// No firing alert is lost, and no page the platform owes is either. Every
// team policy invokes platform.paging, so a team's evaluation holds the
// platform's pages among its candidates, bound to the team's page_after and
// tagged platform.paging:
//
//   - When the team's policy decides something else than a page to the
//     target platform.paging paged in that evaluation, the platform's page
//     goes out: a team may add pages, never replace the platform's. That's
//     a guardrail violation, counted, logged and put on the span.
//   - When there is no evaluation to read (it failed, ran past its timeout,
//     or never ran because the batch deadline passed or the alert is past
//     the webhook's limit), platform.paging runs on its own in the platform
//     engine, with the platform's defaults. Its page goes out if it pages,
//     and the kind's default, notify(reason: unrouted), otherwise.
//   - When the platform engine can't evaluate (it's being replaced), the
//     alert goes out with what could be decided, the answer says so
//     (Routed.unsafe), and the webhook answers 503 so Alertmanager delivers
//     the batch again.
//
// An alert no team owns goes to the kind's default. An alert the router
// can't read does too, unless its team and severity are readable enough for
// platform.paging, in which case the platform's page goes out.

import { context, type Span, SpanStatusCode, trace } from "@opentelemetry/api";
import { type EvalResult, SigilTimeoutError } from "@spechtlabs/sigil";

import type { Clock } from "../clock";
import type { Delivery, Dispatcher } from "../dispatch/dispatcher";
import type { AlertSource, History } from "../dispatch/history";
import { destination, type Notification } from "../dispatch/notifier";
import { goDurationString } from "../duration";
import type { PlatformEngine, PlatformVerdict } from "../engine/platform";
import { type EvaluatorPool, NotStartedError } from "../engine/pool";
import { errorResponse, HumaneError, humane, messageOf, wrap } from "../errors";
import { type Alert, type Input, Page, type Team } from "../routing/kind";
import { type Lease, type PolicyStore, REQUIRED_POLICY, ROOT_SUFFIX } from "../store/store";
import type { TeamDirectory } from "../teams/directory";
import { type ErrorKind, type LogFields, type LogLevel, NO_TEAM, type Telemetry } from "../telemetry/types";
import { classify, type Failure, fallbackResponse, routeResponse, STATUS_CLIENT_CLOSED_REQUEST } from "./render";
import type { AlertStatus, RouteResponse } from "./wire";

/** The fallback advice when the kind's default decided. */
const DEFAULT_FALLBACK =
  "the alert was routed with the fallback decision, notify(reason: unrouted), which the decision fields hold";

/** The fallback advice when the platform's page decided. */
const PAGE_FALLBACK = `the alert was routed with ${REQUIRED_POLICY}'s own page, which the decision fields hold; it pages with the platform's default page_after, not the team's`;

/** One firing alert on its way to a decision, and what the router already knows about it. */
export interface AlertJob {
  /** The alert's name and severity as received, for the span and the log even when they don't parse. */
  name: string;
  severity: string;
  /** Alertmanager's fingerprint, empty for an alert posted on its own. */
  fingerprint: string;
  /** The team the alert names, and the directory's entry when it lists that team. */
  teamLabel: string;
  team: Team | undefined;
  /** The kind's alert, when the alert could be read; otherwise why not. */
  alert: Alert | undefined;
  invalid: HumaneError | undefined;
  /**
   * For an alert that can't be read but whose team and severity can: the
   * input platform.paging decides with, from what could be read.
   */
  pagingInput?: Input;
  source: AlertSource;
  /**
   * Set for an alert that must not be evaluated: its webhook's batch
   * deadline passed, or it's past the webhook's alert limit. It goes the
   * way of a failed evaluation.
   */
  skip?: { kind: ErrorKind | undefined; error: HumaneError };
}

/** How one alert was routed. */
export interface Routed {
  resp: RouteResponse;
  status: Exclude<AlertStatus, "resolved">;
  /** Why the policy didn't route the alert; undefined when it did. */
  error: HumaneError | undefined;
  /** Set when the evaluation failed, or its decision was overridden. */
  failure: Failure | undefined;
  /** The client left before the evaluation; nothing was dispatched or counted. */
  canceled: boolean;
  delivery: Delivery | undefined;
  /**
   * Set when platform.paging couldn't be evaluated for an alert a team
   * owns, so the answer can't vouch for the platform's page. The endpoints
   * answer 503 so the alert is sent again.
   */
  unsafe: HumaneError | undefined;
}

/** What bounds routing one alert. */
export interface RouteOptions {
  /** The request's; a client that already left gets nothing routed. */
  signal?: AbortSignal;
  /** When the evaluation must have started by, in the clock's milliseconds. */
  evaluateBy?: number;
  /** How long the answer waits for the delivery; the delivery itself goes on. */
  deliverBy?: number;
}

export interface RouterOptions {
  store: PolicyStore;
  teams: TeamDirectory;
  pool: EvaluatorPool;
  platform: PlatformEngine;
  dispatcher: Dispatcher;
  history: History;
  telemetry: Telemetry;
  clock: Clock;
  evaluationTimeoutMs: number;
}

export class AlertRouter {
  constructor(private readonly opts: RouterOptions) {}

  /**
   * Decides one firing alert and delivers the decision, in the alert's
   * alertrouter.route span.
   */
  async route(lease: Lease, job: AlertJob, opts: RouteOptions = {}): Promise<Routed> {
    const { telemetry } = this.opts;
    const span = telemetry.tracer.startSpan("alertrouter.route", {
      attributes: {
        "alert.name": job.name,
        "alert.severity": job.severity,
        "alert.fingerprint": job.fingerprint,
        "alertrouter.team": job.team === undefined ? NO_TEAM : job.team.name,
      },
    });
    if (job.team === undefined) span.setAttribute("alertrouter.team_label", job.teamLabel);
    const ctx = trace.setSpan(context.active(), span);
    try {
      return await context.with(ctx, () => this.#routeIn(span, lease, job, opts));
    } finally {
      span.end();
    }
  }

  /**
   * Routes an alert past the webhook's limit as cheaply as correctness
   * allows: no evaluation, span, log line or history entry, but the
   * platform's page when it pages, the delivery, and the routed counter. The
   * webhook logs the overflow once for the batch.
   */
  async routeQuietly(job: AlertJob & { skip: NonNullable<AlertJob["skip"]> }, deliverBy?: number): Promise<Routed> {
    const r = this.#decideWithout(job);
    const delivery = await this.#deliver(notification(job, r.resp), deliverBy);
    this.opts.telemetry.metrics.observeRouted(job.team === undefined ? NO_TEAM : job.team.name, r.status);
    return finish(r, delivery);
  }

  async #routeIn(span: Span, lease: Lease, job: AlertJob, opts: RouteOptions): Promise<Routed> {
    const { telemetry, history, clock } = this.opts;
    const fields = alertFields(job);

    if (aborted(opts.signal)) return this.#clientClosed(span, job, fields);

    const started = performance.now();
    const r = await this.#decide(lease, job, opts);
    const tookNs = r.evaluated ? BigInt(Math.round((performance.now() - started) * 1e6)) : 0n;
    // A client that left while its alert was evaluated gets nothing routed
    // for it, and nothing counts as a failure: no one failed, and no one is
    // left to tell.
    if (r.evaluated && aborted(opts.signal)) return this.#clientClosed(span, job, fields);
    r.count?.();
    recordRoute(span, r);
    if (r.violation !== undefined) this.#reportViolation(span, job, r.violation);

    const n = notification(job, r.resp);
    const delivery = await this.#deliver(n, opts.deliverBy);
    telemetry.metrics.observeRouted(job.team === undefined ? NO_TEAM : job.team.name, r.status);
    const routed = finish(r, delivery);

    const logFields: LogFields = {
      ...fields,
      status: routed.status as string,
      policy: r.resp.policy ?? "",
      decision: r.resp.decision,
      reason: r.resp.reason,
      destination: delivery.destination,
    };
    if (tookNs > 0n) logFields.took = goDurationString(tookNs);
    let level = logLevel(r);
    if (r.failure !== undefined) {
      Object.assign(logFields, {
        error_kind: r.failure.kind ?? "",
        http_status: r.failure.status,
        error: r.failure.error.message,
      });
    } else if (r.error !== undefined) {
      logFields.error = r.error.message;
    }
    if (r.unsafe !== undefined) {
      logFields.platform_error = r.unsafe.message;
      level = "error";
      span.addEvent("alertrouter.platform_unavailable", { error: r.unsafe.message });
      span.setStatus({ code: SpanStatusCode.ERROR, message: r.unsafe.message });
    }
    if (delivery.status === "duplicate") logFields.deduplicated = true;
    if (routed.status === "dispatch_failed") {
      const herr = deliveryError(n, delivery);
      span.recordException(herr);
      span.setStatus({ code: SpanStatusCode.ERROR, message: "dispatching the notification failed" });
      logFields.dispatch_error = herr.message;
      level = "error";
    }
    telemetry.logger.log(level, "alert routed", logFields);

    const spanContext = span.spanContext();
    history.add({
      at: new Date(clock.now()).toISOString(),
      source: job.source,
      ...(job.alert === undefined
        ? {}
        : {
            alert: {
              name: job.alert.name,
              severity: job.alert.severity,
              labels: definedLabels(job.alert.labels),
              firing_for: job.alert.firing_for,
            },
          }),
      severity: job.severity,
      result: {
        fingerprint: job.fingerprint,
        alertname: job.name,
        status: routed.status,
        ...(routed.error === undefined ? {} : { error: routed.error.message }),
        ...withoutError(routed.resp),
      },
      notification: {
        destination: delivery.destination,
        status: delivery.status,
        ...(delivery.status === "failed" ? { error: messageOf(delivery.error) } : {}),
      },
      ...(trace.isSpanContextValid(spanContext) ? { trace_id: spanContext.traceId } : {}),
    });

    return routed;
  }

  /** Starts the delivery and waits for it until deliverBy, when given. */
  async #deliver(n: Notification, deliverBy: number | undefined): Promise<Delivery> {
    const { dispatcher, clock } = this.opts;
    const pending = dispatcher.dispatch(n);
    if (deliverBy === undefined) return pending;
    const waitMs = deliverBy - clock.now();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const late = new Promise<Delivery>((resolve) => {
      timer = setTimeout(() => resolve({ status: "late", destination: destination(n) }), Math.max(0, waitMs));
    });
    try {
      return await Promise.race([pending, late]);
    } finally {
      clearTimeout(timer);
    }
  }

  /**
   * The answer for a client that left before its alert was decided: 499, no
   * body, nothing dispatched or counted. The span records why it stopped
   * without being marked failed.
   */
  #clientClosed(span: Span, job: AlertJob, fields: LogFields): Routed {
    const err = humane(
      `the client closed the request while ${policyOf(job) || "the alert"} was evaluated`,
      "nothing to fix on either side; the client left before the answer",
    );
    span.recordException(err);
    this.opts.telemetry.logger.info("the client closed the request during the evaluation", {
      ...fields,
      error_kind: "",
      http_status: STATUS_CLIENT_CLOSED_REQUEST,
      error: err.message,
    });
    return {
      resp: fallbackResponse(job.team?.name ?? ""),
      status: "failed",
      error: err,
      failure: { error: err, kind: undefined, status: STATUS_CLIENT_CLOSED_REQUEST },
      canceled: true,
      delivery: undefined,
      unsafe: undefined,
    };
  }

  /**
   * The alert's decision, before delivery. What it counts in the metrics is
   * left to count(), which the caller runs once it knows the client is still
   * there.
   */
  async #decide(lease: Lease, job: AlertJob, opts: RouteOptions): Promise<Decided> {
    const team = job.team;
    if (team === undefined || job.alert === undefined || job.invalid !== undefined || job.skip !== undefined) {
      return this.#decideWithout(job);
    }
    const { telemetry, pool } = this.opts;
    const input: Input = { alert: job.alert, team };
    // The standalone platform engine is asked only when the team's
    // evaluation produced no trace to read platform.paging's pages from.
    const verdict = () => this.opts.platform.page(input);
    const policyName = team.name + ROOT_SUFFIX;

    const policy = lease.snapshot.policy(team.name);
    if (policy === undefined) {
      // The store compiles a policy for every team in the directory, so this
      // is a store and a directory that disagree.
      return failedWithout(
        team,
        undefined,
        verdict(),
        humane(
          `team ${team.name} is in the team directory, but the loaded bundle has no policy for it`,
          "build the store with the team directory's names, so both list the same teams",
        ),
        500,
      );
    }

    const done = telemetry.metrics.evaluationTimer(team.name);
    let res: EvalResult;
    try {
      res = await pool.evaluate(team.name, policy, input, {
        timeoutMs: this.opts.evaluationTimeoutMs,
        ...(opts.evaluateBy === undefined ? {} : { startBy: opts.evaluateBy }),
      });
    } catch (err) {
      if (err instanceof NotStartedError) {
        const r = failedWithout(team, undefined, verdict(), errDeadline(policyName), 503);
        return { ...r, count: () => telemetry.metrics.observeEvaluationError(team.name, "timeout") };
      }
      done();
      if (err instanceof SigilTimeoutError) {
        // The worker didn't answer shortly after the deadline and was
        // replaced; to the caller that's a timeout like any other.
        return this.#failed(team, policy.name, verdict(), {
          policy: policy.name,
          outcome: [],
          trace: [],
          error: { kind: "canceled", message: err.message, help: "" },
        });
      }
      // An input the kind rejects, or an engine that failed: alertrouter's
      // failure, not the policy's.
      return {
        ...failedWithout(
          team,
          policyName,
          verdict(),
          wrap(
            err,
            `evaluating ${policyName} failed: ${messageOf(err)}`,
            DEFAULT_FALLBACK,
            "if it keeps failing, check the alertrouter logs",
          ),
          500,
        ),
        evaluated: true,
      };
    }
    done();
    if (res.error !== undefined) return this.#failed(team, policy.name, verdict(), res);

    const resp = routeResponse(team.name, policy.name, res);
    const owed = platformPageIn(res);
    if (owed !== undefined && !(resp.decision === Page.name && resp.target === owed.target)) {
      // The team's policy decided around the page platform.paging made in
      // its own evaluation, with the team's page_after: say with a
      // higher-ranked page to someone else. The platform's page goes out.
      const verdict = owed;
      const violation = {
        policy: policy.name,
        decision: resp.decision,
        reason: resp.reason,
        target: resp.target,
        verdict,
      };
      const error = humane(
        `${policy.name} decided ${describe(resp)} for an alert ${REQUIRED_POLICY} pages ${verdict.target} for (reason: ${verdict.reason})`,
        `the alert was routed with ${REQUIRED_POLICY}'s page, which the decision fields hold`,
        `a team policy may add pages, never replace the platform's; tell the owners of ${policy.name}`,
      );
      applyPage(resp, verdict);
      resp.error = errorResponse(error);
      return {
        resp,
        status: "failed",
        error,
        failure: { error, kind: undefined, status: 500 },
        evaluated: true,
        unsafe: undefined,
        violation,
        count: () => telemetry.metrics.observeGuardrailViolation(team.name),
      };
    }
    return {
      resp,
      status: "routed",
      error: undefined,
      evaluated: true,
      unsafe: undefined,
      count: () => telemetry.metrics.observeDecision(team.name, policy.name, resp.decision, resp.reason),
    };
  }

  /** The answer for an evaluation whose result holds a failure. */
  #failed(team: Team, policyName: string, verdict: PlatformVerdict, res: EvalResult): Decided {
    const failure = res.error;
    if (failure === undefined) throw new Error("#failed on a result without a failure");
    const page = verdict.kind === "page" ? verdict : undefined;
    const resp = routeResponse(team.name, policyName, res);
    const f = classify(policyName, failure, res, page === undefined ? DEFAULT_FALLBACK : PAGE_FALLBACK);
    applyPage(resp, page);
    resp.error = errorResponse(f.error);
    if (f.asserts !== undefined) resp.asserts = f.asserts;
    if (f.conflict !== undefined) resp.conflict = f.conflict;
    const kind = f.kind;
    return {
      resp,
      status: "failed",
      error: f.error,
      failure: f,
      evaluated: true,
      unsafe: verdict.kind === "unavailable" ? verdict.error : undefined,
      ...(kind === undefined
        ? {}
        : { count: () => this.opts.telemetry.metrics.observeEvaluationError(team.name, kind) }),
    };
  }

  /**
   * The decision for an alert no team policy runs for: no team owns it, it
   * can't be read, or it was skipped. The platform's page when platform.paging
   * pages for what could be read, the kind's default otherwise.
   */
  #decideWithout(job: AlertJob): Decided {
    const team = job.team;
    if (team === undefined) {
      return {
        resp: fallbackResponse(""),
        status: "unowned",
        error: this.#errUnowned(job.teamLabel),
        evaluated: false,
        unsafe: undefined,
      };
    }
    const input = job.alert !== undefined && job.invalid === undefined ? { alert: job.alert, team } : job.pagingInput;
    const verdict: PlatformVerdict = input === undefined ? { kind: "none" } : this.opts.platform.page(input);
    if (job.invalid !== undefined || job.alert === undefined) {
      const resp = fallbackResponse(team.name);
      applyPage(resp, verdict.kind === "page" ? verdict : undefined);
      return {
        resp,
        status: "invalid",
        error: job.invalid ?? humane("the alert can't be read", "check the alertrouter logs for the alert"),
        evaluated: false,
        unsafe: verdict.kind === "unavailable" ? verdict.error : undefined,
      };
    }
    const skip = job.skip ?? {
      kind: undefined,
      error: humane("the alert wasn't evaluated", "this is a bug in alertrouter; please report it"),
    };
    const r = failedWithout(team, undefined, verdict, skip.error, 503);
    const kind = skip.kind;
    return kind === undefined
      ? r
      : { ...r, count: () => this.opts.telemetry.metrics.observeEvaluationError(team.name, kind) };
  }

  #reportViolation(span: Span, job: AlertJob, v: Violation): void {
    const attributes = {
      policy: v.policy,
      decision: v.decision,
      reason: v.reason,
      target: v.target ?? "",
      platform_reason: v.verdict.reason,
      platform_target: v.verdict.target,
    };
    span.addEvent("alertrouter.guardrail_violation", attributes);
    this.opts.telemetry.logger.error("guardrail violated: the platform's page replaced the team's decision", {
      ...alertFields(job),
      ...attributes,
    });
  }

  #errUnowned(label: string): HumaneError {
    const advice = "the alert was routed with the kind's default, notify(reason: unrouted), to #alerts";
    const names = this.opts.teams.names().join(", ");
    if (label === "") {
      return humane("the alert has no team label", advice, `add a team label to the alerting rule, one of: ${names}`);
    }
    return humane(
      `team ${JSON.stringify(label)} isn't in the team directory`,
      advice,
      `set the alerting rule's team label to one of: ${names}`,
      `or add the team to the team directory, ALERTROUTER_TEAMS_FILE, with a policy ${label}${ROOT_SUFFIX}`,
    );
  }
}

interface Violation {
  policy: string;
  decision: string;
  reason: string;
  target: string | undefined;
  verdict: { reason: string; target: string };
}

interface Decided {
  resp: RouteResponse;
  status: "routed" | "unowned" | "invalid" | "failed";
  error: HumaneError | undefined;
  failure?: Failure;
  /** Whether the team's policy ran. */
  evaluated: boolean;
  unsafe: HumaneError | undefined;
  violation?: Violation;
  /** Counts the decision or the failure, once the alert is known to be routed. */
  count?: () => void;
}

/**
 * The answer for an owned, readable alert the router didn't get a decision
 * for from its policy, for a reason of its own: the platform's page when it
 * pages, the kind's default otherwise.
 */
function failedWithout(
  team: Team,
  policyName: string | undefined,
  verdict: PlatformVerdict,
  err: HumaneError,
  status: number,
): Decided {
  const page = verdict.kind === "page" ? verdict : undefined;
  const error =
    page === undefined
      ? err
      : new HumaneError(err.message, [...err.advice.filter((a) => a !== DEFAULT_FALLBACK), PAGE_FALLBACK], {
          cause: err.cause,
        });
  const resp = fallbackResponse(team.name, policyName);
  applyPage(resp, page);
  resp.error = errorResponse(error);
  return {
    resp,
    status: "failed",
    error,
    failure: { error, kind: undefined, status },
    evaluated: false,
    unsafe: verdict.kind === "unavailable" ? verdict.error : undefined,
  };
}

/** Folds the delivery into the decision: a delivery that failed or didn't finish makes the alert dispatch_failed. */
function finish(r: Decided, delivery: Delivery): Routed {
  let status: Routed["status"] = r.status;
  let error = r.error;
  if (delivery.status === "failed" || delivery.status === "late") {
    const herr = deliveryError(notificationOf(r.resp), delivery);
    status = "dispatch_failed";
    error ??= herr;
    r.resp.error ??= errorResponse(herr);
  }
  if (r.unsafe !== undefined) {
    error ??= r.unsafe;
    r.resp.error ??= errorResponse(r.unsafe);
  }
  return { resp: r.resp, status, error, failure: r.failure, canceled: false, delivery, unsafe: r.unsafe };
}

/** Why a delivery didn't go out, or not in time. */
function deliveryError(n: Pick<Notification, "decision">, d: Delivery): HumaneError {
  if (d.status === "late") {
    return humane(
      `dispatching the ${n.decision} to ${d.destination} hadn't finished when the batch's time ran out`,
      "the delivery goes on; the retry this answer asks for is deduplicated once it finishes",
    );
  }
  return wrap(
    d.error,
    `dispatching the ${n.decision} to ${d.destination} failed: ${messageOf(d.error)}`,
    "the decision in this answer is right; check the notifier's logs for why it didn't go out",
  );
}

function notification(job: AlertJob, resp: RouteResponse): Notification {
  return {
    team: job.team?.name ?? NO_TEAM,
    alertname: job.name,
    fingerprint: job.fingerprint,
    ...notificationOf(resp),
  };
}

function notificationOf(
  resp: RouteResponse,
): Pick<Notification, "decision" | "reason" | "policy" | "target" | "channel"> {
  return {
    decision: resp.decision,
    reason: resp.reason,
    ...(resp.policy === undefined ? {} : { policy: resp.policy }),
    ...(resp.target === undefined ? {} : { target: resp.target }),
    ...(resp.channel === undefined ? {} : { channel: resp.channel }),
  };
}

/**
 * The page platform.paging made in a team's evaluation, bound to the team's
 * params, when it made one: its highest-ranked page candidate. A candidate
 * tagged platform.paging can only come from the platform's trusted files,
 * which the store requires the policy from, so the tag can be trusted.
 */
function platformPageIn(res: EvalResult): { reason: string; target: string } | undefined {
  let best: { reason: string; target: string; rank: number } | undefined;
  for (const c of res.trace) {
    if (c.policy !== REQUIRED_POLICY || c.decision !== Page.name) continue;
    const target = c.payload?.target;
    if (typeof target !== "string") continue;
    const rank = (Page.reasons as readonly string[]).indexOf(c.reason);
    if (best === undefined || rank < best.rank) best = { reason: c.reason, target, rank };
  }
  return best === undefined ? undefined : { reason: best.reason, target: best.target };
}

function errDeadline(policyName: string): HumaneError {
  return humane(
    `the webhook's batch deadline for evaluations passed before ${policyName} could be evaluated`,
    "if batches keep running out of time, lower max_alerts on the Alertmanager receiver, raise ALERTROUTER_WORKERS or ALERTROUTER_BATCH_TIMEOUT",
  );
}

/** A decision as a policy writes it: page(reason: sustained, target: x). */
function describe(resp: RouteResponse): string {
  const payload =
    resp.target !== undefined
      ? `, target: ${resp.target}`
      : resp.channel !== undefined
        ? `, channel: ${resp.channel}`
        : "";
  return `${resp.decision}(reason: ${resp.reason}${payload})`;
}

/** Puts the platform's page in place of the decision. */
function applyPage(resp: RouteResponse, page: { reason: string; target: string } | undefined): void {
  if (page === undefined) return;
  resp.decision = Page.name;
  resp.reason = page.reason;
  resp.target = page.target;
  delete resp.channel;
}

/**
 * Puts the alert's routing on its span, with one event per trace candidate,
 * so a trace explains the decision on its own. A failure marks the span
 * failed; an unowned or invalid alert doesn't, since it was routed as
 * designed.
 */
function recordRoute(span: Span, r: Decided): void {
  // sigil.policy is left out when no policy ran, as the response's policy is.
  if (r.resp.policy !== undefined) span.setAttribute("sigil.policy", r.resp.policy);
  span.setAttributes({
    "alertrouter.outcome": r.status,
    "sigil.decision": r.resp.decision,
    "sigil.reason": r.resp.reason,
    "sigil.candidates": r.resp.trace.length,
  });
  for (const c of r.resp.trace) {
    span.addEvent("sigil.candidate", {
      decision: c.decision,
      reason: c.reason,
      policy: c.policy,
      location: c.location,
      winner: c.winner,
    });
  }
  if (r.failure !== undefined) {
    span.recordException(r.failure.error);
    span.setStatus({ code: SpanStatusCode.ERROR, message: r.failure.error.message });
  }
}

/** The level of an alert's "alert routed" line. */
function logLevel(r: Decided): LogLevel {
  if (r.failure !== undefined) return r.failure.status < 500 ? "warn" : "error";
  if (r.status === "invalid") return "warn";
  return "info";
}

/**
 * The fields that identify an alert in its log lines. team is the owning
 * team or "-", since an alert rule can write any team label; an unowned
 * alert's own label goes in team_label instead.
 */
function alertFields(job: AlertJob): LogFields {
  const fields: LogFields = {
    team: job.team === undefined ? NO_TEAM : job.team.name,
    alertname: job.name,
    fingerprint: job.fingerprint,
  };
  if (job.team === undefined) fields.team_label = job.teamLabel;
  return fields;
}

// A function rather than a property read, so the compiler doesn't narrow
// the signal's state across the awaits that change it.
function aborted(signal: AbortSignal | undefined): boolean {
  return signal?.aborted === true;
}

function policyOf(job: AlertJob): string {
  return job.team === undefined ? "" : job.team.name + ROOT_SUFFIX;
}

function withoutError(resp: RouteResponse): Omit<RouteResponse, "error"> {
  const { error: _error, ...rest } = resp;
  return rest;
}

// The kind's input type allows a map value to be left out; the alert's labels
// never are.
function definedLabels(labels: Readonly<Record<string, string | undefined>>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(labels)) if (v !== undefined) out[k] = v;
  return out;
}
