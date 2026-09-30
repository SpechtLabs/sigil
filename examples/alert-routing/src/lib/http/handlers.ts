// The API's endpoints, framework-free: each takes a web Request and returns
// a Response, so the integration tests drive exactly what production runs,
// and Next's route files only adapt. Status codes, bodies and messages are
// the Go service's (internal/server/handlers.go, route.go, alerts.go) except
// where the lead's review changed them on purpose; ARCHITECTURE.md lists
// those.

import { ms, SigilError } from "@spechtlabs/sigil";

import {
  convert,
  isResolved,
  LABEL_ALERTNAME,
  LABEL_SEVERITY,
  LABEL_TEAM,
  MAX_ALERTS,
  pagingAlert,
  parseWebhook,
  type WebhookAlert,
} from "../alertmanager/webhook";
import type { Clock } from "../clock";
import type { History } from "../dispatch/history";
import { formatDuration, nsToMs, parseDuration } from "../duration";
import type { PlatformEngine } from "../engine/platform";
import { adviceOf, errorResponse, HumaneError, humane, messageOf, wrap } from "../errors";
import { type Alert, AlertRouting, parseSeverity, SEVERITIES, type Team } from "../routing/kind";
import { type PolicyStore, REQUIRED_POLICY } from "../store/store";
import type { TeamDirectory } from "../teams/directory";
import type { Telemetry } from "../telemetry/types";
import { BodyError, badDuration, isObject, readJSON, strictKeys, wrongType } from "./body";
import { ordered, STATUS_CLIENT_CLOSED_REQUEST } from "./render";
import { errorJSON, json } from "./respond";
import type { AlertJob, AlertRouter, Routed } from "./route";
import type { EventHub } from "./sse";
import { formatTime } from "./time";
import type {
  AlertResult,
  KindPolicies,
  PoliciesResponse,
  PolicyFilesResponse,
  StatusResponse,
  TeamsResponse,
  WebhookResponse,
} from "./wire";

const ROUTE_ADVICE = `send a JSON object like {"alert": {"name": "CheckoutLatencyHigh", "severity": "warning", "labels": {"env": "production"}, "firing_for": "12m"}}`;
const WEBHOOK_ADVICE =
  "send Alertmanager's webhook payload, version 4, with a webhook_configs receiver pointing at /api/v1/alerts";

/** How many alerts of a batch are read between two yields to the event loop. */
const YIELD_EVERY = 32;

export interface HandlerOptions {
  store: PolicyStore;
  teams: TeamDirectory;
  router: AlertRouter;
  history: History;
  telemetry: Telemetry;
  clock: Clock;
  /** How long a webhook batch may take, evaluation and dispatch together. */
  batchTimeoutMs: number;
  /** The platform engine; /readyz reports it. */
  platform: PlatformEngine;
  /** The console's event streams. */
  events: EventHub;
  /** Aborts when the service shuts down. */
  shutdown: AbortSignal;
}

/** The endpoints. */
export class Handlers {
  constructor(private readonly opts: HandlerOptions) {}

  /**
   * POST /api/v1/alerts, Alertmanager's webhook: routes every firing alert
   * of the batch and acknowledges every resolved one, in the webhook's
   * order, and answers with each alert's result.
   *
   * Alertmanager retries only a 5xx, and then the whole group. So the
   * answer is 200 once every decision went out, whatever the decisions
   * were, and 503 when a page or notification failed to go out or didn't
   * finish within the batch timeout, or when the platform's page couldn't
   * be vouched for (a retry is safe: the dispatcher deduplicates by
   * fingerprint). Only a body that isn't a webhook at all is a 4xx. A router
   * that hasn't loaded its policies answers 503, so Alertmanager retries
   * once it has.
   */
  async webhook(req: Request): Promise<Response> {
    const { store, telemetry, clock } = this.opts;
    // The batch's clock starts when the request arrives: reading a large
    // body counts against its timeout too.
    const arrival = clock.now();
    using lease = store.acquire() ?? notLoaded();
    let body: unknown;
    try {
      body = await readJSON(req, WEBHOOK_ADVICE);
    } catch (err) {
      return bodyError(err);
    }
    let hook: ReturnType<typeof parseWebhook>;
    try {
      hook = parseWebhook(body);
    } catch (err) {
      return errorJSON(400, err);
    }

    telemetry.metrics.observeBatch(hook.alerts.length);
    if (hook.truncatedAlerts > 0) {
      telemetry.metrics.observeTruncated("alertmanager", hook.truncatedAlerts);
      telemetry.logger.warn("Alertmanager truncated the webhook", {
        group_key: hook.groupKey,
        receiver: hook.receiver,
        truncated_alerts: hook.truncatedAlerts,
        advice: "raise max_alerts on the Alertmanager receiver, up to 1000, so every alert of the group is routed",
      });
    }
    const overflow = hook.alerts.length - MAX_ALERTS;
    if (overflow > 0) {
      telemetry.metrics.observeTruncated("alertrouter", overflow);
      telemetry.logger.warn("the webhook carries more alerts than alertrouter evaluates at once", {
        group_key: hook.groupKey,
        alerts: hook.alerts.length,
        evaluated: MAX_ALERTS,
        skipped: overflow,
        advice: `set max_alerts on the Alertmanager receiver to ${MAX_ALERTS} or fewer; the rest went to the fallback`,
      });
    }

    // One clock reading for the batch, so every alert's firing time is
    // measured from the moment the webhook arrived. Evaluations must start in
    // the first half of the batch timeout, so an alert that missed its
    // evaluation still has time to go out with the fallback; the answer
    // waits for deliveries until the end of it.
    const evaluateBy = arrival + this.opts.batchTimeoutMs / 2;
    const deliverBy = arrival + this.opts.batchTimeoutMs;
    const results: (AlertResult | Promise<Routed>)[] = [];
    for (const [i, a] of hook.alerts.entries()) {
      const alertname = a.labels[LABEL_ALERTNAME] ?? "";
      if (isResolved(a) && a.problem === undefined) {
        telemetry.metrics.observeReceived("resolved");
        results.push({ fingerprint: a.fingerprint, alertname, status: "resolved" });
        continue;
      }
      telemetry.metrics.observeReceived("firing");
      if (req.signal.aborted) return clientClosed();

      const job = this.#webhookJob(a, new Date(arrival));
      if (i >= MAX_ALERTS) {
        // Past the limit: the platform's page or the fallback, delivered,
        // with nothing per alert beyond that; the overflow is logged once.
        results.push(
          this.opts.router.routeQuietly(
            {
              ...job,
              skip: {
                kind: undefined,
                error: humane(
                  `the alert is past the first ${MAX_ALERTS} of its webhook, which alertrouter evaluates`,
                  `set max_alerts on the Alertmanager receiver to ${MAX_ALERTS} or fewer`,
                ),
              },
            },
            deliverBy,
          ),
        );
      } else {
        if (clock.now() >= evaluateBy) {
          job.skip = {
            kind: "timeout",
            error: humane(
              `the webhook's batch deadline for evaluations, half of ${this.opts.batchTimeoutMs} ms, passed before ${job.team?.name ?? ""}.alerts could be evaluated`,
              "if batches keep running out of time, lower max_alerts on the Alertmanager receiver or raise ALERTROUTER_BATCH_TIMEOUT",
            ),
          };
        }
        // Evaluations run in the pool's workers and deliveries overlap up to
        // the dispatcher's limit; the results are collected in order below.
        results.push(this.opts.router.route(lease, job, { signal: req.signal, evaluateBy, deliverBy }));
      }
      // Converting alerts and asking platform.paging about them happens on
      // this thread; let other requests in between.
      if (i % YIELD_EVERY === YIELD_EVERY - 1) await new Promise((resolve) => setImmediate(resolve));
    }

    const resp: WebhookResponse = { received: hook.alerts.length, routed: 0, results: [] };
    let retry = false;
    for (const [i, r] of results.entries()) {
      if (!(r instanceof Promise)) {
        resp.results.push(r);
        continue;
      }
      const routed = await r;
      if (routed.canceled) return clientClosed();
      const a = hook.alerts[i] as WebhookAlert;
      const { error: _error, ...fields } = routed.resp;
      resp.results.push({
        fingerprint: a.fingerprint,
        alertname: a.labels[LABEL_ALERTNAME] ?? "",
        status: routed.status,
        ...(routed.error === undefined ? {} : { error: routed.error.message }),
        ...ordered(fields),
      });
      if (routed.status === "routed") resp.routed++;
      if (routed.status === "dispatch_failed" && routed.resp.decision !== "drop") retry = true;
      if (routed.unsafe !== undefined) retry = true;
    }
    return json(retry ? 503 : 200, resp);
  }

  /**
   * POST /api/v1/teams/{team}/route: routes one alert of the team in the
   * path, dispatches the decision and answers with it: 200 when the policy
   * decided, and 422, 500 or 503 with the fallback decision when its
   * evaluation failed. A client that left gets 499 and no body.
   */
  async routeOne(req: Request, teamName: string): Promise<Response> {
    const { store, teams, telemetry } = this.opts;
    using lease = store.acquire() ?? notLoaded();
    const team = teams.lookup(teamName);
    if (team === undefined) {
      return errorJSON(
        404,
        humane(
          `team ${JSON.stringify(teamName)} isn't in the team directory`,
          `teams: ${teams.names().join(", ")}`,
          "GET /api/v1/teams lists the teams with their on-call targets and channels",
        ),
      );
    }

    let alert: Alert;
    try {
      alert = checkAlert(decodeRouteRequest(await readJSON(req, ROUTE_ADVICE)));
    } catch (err) {
      return err instanceof BodyError ? bodyError(err) : errorJSON(422, err);
    }

    telemetry.metrics.observeReceived("firing");
    const r = await this.opts.router.route(
      lease,
      {
        name: alert.name,
        severity: alert.severity,
        fingerprint: "",
        teamLabel: team.name,
        team,
        alert,
        invalid: undefined,
        source: "route",
      },
      { signal: req.signal },
    );
    if (r.canceled) return clientClosed();
    if (r.unsafe !== undefined) return json(503, ordered(r.resp));
    return json(r.failure?.status ?? 200, ordered(r.resp));
  }

  /** GET /api/v1/policies: the loaded bundle, one kind. */
  policies(): Response {
    const snap = this.opts.store.snapshot();
    if (snap === undefined) {
      logFailure(this.opts.telemetry, 503, "/api/v1/policies", errNotLoaded());
      return errorJSON(503, errNotLoaded());
    }
    const kind: KindPolicies = {
      kind: snap.kind,
      version: snap.kindVersion,
      loaded_at: formatTime(snap.loadedAt),
      source: snap.source,
      fingerprint: snap.fingerprint,
      policies: snap.roots.map((r) => ({ team: r.team, policy: r.policy })),
    };
    return json(200, { kinds: [kind] } satisfies PoliciesResponse);
  }

  /**
   * POST /api/v1/policies/reload: loads the bundle as it is now. A bundle
   * that doesn't load is a 500 with the compiler's diagnostics, and the
   * previous bundle keeps serving.
   */
  async reload(): Promise<Response> {
    try {
      await this.opts.store.load("manual");
    } catch (err) {
      logFailure(this.opts.telemetry, 500, "/api/v1/policies/reload", err);
      return errorJSON(500, err);
    }
    return this.policies();
  }

  /** GET /api/v1/teams: the team directory, sorted by name. */
  teams(): Response {
    return json(200, { teams: this.opts.teams.teams() } satisfies TeamsResponse);
  }

  /** GET /healthz: the process is up and serving HTTP. */
  healthz(): Response {
    return json(200, { status: "ok" } satisfies StatusResponse);
  }

  /** GET /readyz: ready once the bundle is loaded, and until shutdown begins. */
  readyz(): Response {
    const snap = this.opts.store.snapshot();
    // Not ready while the platform engine is being replaced either: an alert
    // routed then couldn't be vouched for.
    if (snap === undefined || this.opts.shutdown.aborted || !this.opts.platform.probe())
      return json(503, { status: "not ready" } satisfies StatusResponse);
    return json(200, { status: "ready", loaded_at: formatTime(snap.loadedAt) } satisfies StatusResponse);
  }

  /** GET /metrics: the service's registry in the Prometheus text format. */
  async metrics(): Promise<Response> {
    const { contentType, body } = await this.opts.telemetry.metrics.render();
    return new Response(body, { status: 200, headers: { "content-type": contentType } });
  }

  /**
   * GET /api/v1/policies/files: exactly what the server compiles, for the
   * console's in-browser preview. Not in the Go service.
   */
  policyFiles(platform: readonly { path: string; source: string }[]): Response {
    const { store } = this.opts;
    const snap = store.snapshot();
    if (snap === undefined) return errorJSON(503, errNotLoaded());
    const failure = store.lastFailure();
    const body: PolicyFilesResponse = {
      kind: AlertRouting.file(),
      platform: platform.map((f) => ({ path: f.path, source: f.source })),
      teams: snap.files.map((f) => ({ path: f.path, source: f.source })),
      required: REQUIRED_POLICY,
      roots: snap.roots.map((r) => ({ team: r.team, policy: r.policy })),
      fingerprint: snap.fingerprint,
      source: snap.source,
      loaded_at: formatTime(snap.loadedAt),
      ...(failure === undefined
        ? {}
        : {
            last_error: {
              at: formatTime(failure.at),
              trigger: failure.trigger,
              error: errorResponse(failure.error) ?? { message: failure.error.message },
            },
          }),
    };
    return json(200, body);
  }

  /**
   * GET /api/v1/policies/{team}/explain: the team's root policy flattened
   * into its rules, as `sigil explain` prints it. Not in the Go service.
   */
  async explain(teamName: string): Promise<Response> {
    using lease = this.opts.store.acquire() ?? notLoaded();
    const policy = lease.snapshot.policy(teamName);
    if (policy === undefined) {
      return errorJSON(
        404,
        humane(
          `team ${JSON.stringify(teamName)} has no policy in the loaded bundle`,
          `teams: ${this.opts.teams.names().join(", ")}`,
        ),
      );
    }
    try {
      return json(200, await policy.explain());
    } catch (err) {
      return errorJSON(
        500,
        err instanceof SigilError ? humane(err.message, err.help ?? "check the alertrouter logs") : err,
      );
    }
  }

  /** GET /api/v1/history: the routed alerts the console remembers, oldest first. Not in the Go service. */
  history(req: Request): Response {
    const after = Number(new URL(req.url).searchParams.get("after") ?? "0");
    return json(200, { entries: this.opts.history.entries(Number.isFinite(after) ? after : 0) });
  }

  /** GET /api/v1/events: new history entries and policy loads as Server-Sent Events. Not in the Go service. */
  events(req: Request): Response {
    return this.opts.events.stream(req);
  }

  /** The answer for a path no route serves. */
  notFound(req: Request): Response {
    const path = new URL(req.url).pathname;
    return errorJSON(
      404,
      humane(`no route for ${req.method} ${path}`, "the API lives under /api/v1; see the README for the routes"),
    );
  }

  #webhookJob(a: WebhookAlert, now: Date): AlertJob {
    const label = a.labels[LABEL_TEAM] ?? "";
    const team: Team | undefined = this.opts.teams.lookup(label);
    const job: AlertJob = {
      name: a.labels[LABEL_ALERTNAME] ?? "",
      severity: a.labels[LABEL_SEVERITY] ?? "",
      fingerprint: a.fingerprint,
      teamLabel: label,
      team,
      alert: undefined,
      invalid: undefined,
      source: "webhook",
    };
    try {
      job.alert = convert(a, now);
    } catch (err) {
      job.invalid =
        err instanceof HumaneError
          ? err
          : wrap(
              err,
              `alert ${job.name || a.fingerprint} can't be read: ${messageOf(err)}`,
              "send the alert as Alertmanager does",
            );
      // An alert whose team and severity can be read still gets the
      // platform's page, from what could be read.
      const readable = team === undefined ? undefined : pagingAlert(a, now);
      if (team !== undefined && readable !== undefined) job.pagingInput = { alert: readable, team };
    }
    return job;
  }
}

/** The error for a request that came before the first bundle loaded. */
export function errNotLoaded(): HumaneError {
  return humane(
    "no policy bundle is loaded yet",
    "wait until GET /readyz reports ready",
    "if it never does, the alertrouter logs name the policy that fails to compile",
  );
}

/** A thrown 503, caught by the router; a handler without a bundle can't do anything else. */
export class NotLoadedError extends Error {}

function notLoaded(): never {
  throw new NotLoadedError();
}

/**
 * Logs a failure on the server's side, which the client can't fix, as the Go
 * service's writeError does for every 5xx it renders.
 */
export function logFailure(telemetry: Telemetry, status: number, path: string, err: unknown): void {
  if (status < 500) return;
  telemetry.logger.error("request failed", { status, path, error: messageOf(err), advice: adviceOf(err) });
}

export { errorJSON, json };

function bodyError(err: unknown): Response {
  if (err instanceof BodyError) return errorJSON(err.status, err);
  return errorJSON(400, err);
}

function clientClosed(): Response {
  return new Response(null, { status: STATUS_CLIENT_CLOSED_REQUEST });
}

/** Decodes the route endpoint's body strictly: every field typed, no unknown fields. */
function decodeRouteRequest(body: unknown): {
  name: string;
  severity: string;
  labels: Record<string, string>;
  firingForNs: bigint;
} {
  if (!isObject(body)) throw wrongType("the body", "a JSON object", ROUTE_ADVICE);
  strictKeys(body, ["alert"], ROUTE_ADVICE);
  const alert = body.alert ?? {};
  if (!isObject(alert)) throw wrongType("alert", "a JSON object", ROUTE_ADVICE);
  strictKeys(alert, ["name", "severity", "labels", "firing_for"], ROUTE_ADVICE);

  const str = (field: string): string => {
    const v = alert[field];
    if (v === undefined || v === null) return "";
    if (typeof v !== "string") throw wrongType(`alert.${field}`, "a string", ROUTE_ADVICE);
    return v;
  };
  const labels = alert.labels ?? {};
  if (!isObject(labels) || !Object.values(labels).every((v) => typeof v === "string")) {
    throw wrongType("alert.labels", "a map of strings", ROUTE_ADVICE);
  }
  let firingForNs = 0n;
  const ff = alert.firing_for;
  if (ff !== undefined && ff !== null) {
    if (typeof ff !== "string") {
      throw badDuration(
        new HumaneError(`durations are strings, found ${JSON.stringify(ff)}`, [
          `write durations as strings like "6h" or "1h30m"`,
        ]),
        ROUTE_ADVICE,
      );
    }
    try {
      firingForNs = parseDuration(ff);
    } catch (err) {
      throw badDuration(err, ROUTE_ADVICE);
    }
  }
  return {
    name: str("name"),
    severity: str("severity"),
    labels: { ...(labels as Record<string, string>) },
    firingForNs,
  };
}

/** The kind's alert, refusing what a policy couldn't evaluate. Throws a HumaneError, a 422. */
function checkAlert(a: { name: string; severity: string; labels: Record<string, string>; firingForNs: bigint }): Alert {
  if (a.name === "") {
    throw humane(
      "alert.name is empty",
      `send the alert's name, its alertname, such as "CheckoutLatencyHigh"; a policy mutes alerts by name`,
    );
  }
  const severity = parseSeverity(a.severity);
  if (severity === undefined) {
    throw humane(
      `alert.severity ${JSON.stringify(a.severity)} isn't a severity`,
      `send one of the severities the AlertRouting kind declares: ${SEVERITIES.join(", ")}`,
    );
  }
  if (a.firingForNs < 0n) {
    throw humane(
      `alert.firing_for is negative: ${formatDuration(a.firingForNs)}`,
      `send how long the alert has been firing, zero or more, such as "12m"`,
    );
  }
  // Sigil durations have millisecond resolution; a finer firing time is
  // rounded down, which no comparison a policy makes can tell apart.
  return { name: a.name, severity, labels: a.labels, firing_for: ms(nsToMs(a.firingForNs)) };
}
