// The service's Prometheus metrics: what a host of Sigil records about its
// policies (alerts received and how each was routed, decisions by team,
// policy, decision and reason, evaluation durations, failed evaluations, the
// notifications dispatched, the state of the loaded bundle) and its HTTP
// requests, plus Node's runtime and process metrics.
//
// Names, labels, help texts and buckets are the Go service's
// (internal/telemetry/metrics.go), so its dashboard, alerts and e2e suite read
// this service unchanged. The metrics listed after alertrouter_notifications_total,
// down to alertrouter_event_streams_closed_total, are new here: the fixes
// after the Go service's review (failed and deduplicated dispatch, truncated
// batches, the paging guardrail, engine restarts, the console's event
// streams) have to be visible.

import { Counter, collectDefaultMetrics, Gauge, Histogram, Registry } from "prom-client";
import type { AlertStatus, ErrorKind, LoadedPolicy, Metrics, Outcome } from "./types";

/**
 * Every alertrouter_* metric and the labels it carries. It is the metric
 * contract: the collectors below take their names and labels from it, and the
 * dashboard test checks every query against it.
 */
export const METRIC_LABELS = {
  alertrouter_alerts_received_total: ["status"],
  alertrouter_alerts_routed_total: ["team", "outcome"],
  alertrouter_webhook_batch_size: [],
  alertrouter_notifications_total: ["decision", "destination"],
  alertrouter_notification_errors_total: ["decision"],
  alertrouter_notifications_deduplicated_total: ["decision"],
  alertrouter_alerts_truncated_total: ["by"],
  alertrouter_notifications_dedup_evicted_total: [],
  alertrouter_guardrail_violations_total: ["team"],
  alertrouter_engine_restarts_total: ["engine"],
  alertrouter_engine_up: ["engine"],
  alertrouter_event_streams_closed_total: ["reason"],
  alertrouter_decisions_total: ["team", "policy", "decision", "reason"],
  alertrouter_evaluation_duration_seconds: ["team"],
  alertrouter_evaluation_errors_total: ["team", "kind"],
  alertrouter_policy_reloads_total: ["result"],
  alertrouter_policy_last_reload_timestamp_seconds: [],
  alertrouter_policy_last_reload_successful: [],
  alertrouter_policy_loaded_info: ["team", "policy", "fingerprint", "source"],
  alertrouter_requests_total: ["code", "method", "route"],
  alertrouter_request_duration_seconds: ["method", "route"],
} as const satisfies Record<string, readonly string[]>;

type MetricName = keyof typeof METRIC_LABELS;
type LabelOf<N extends MetricName> = (typeof METRIC_LABELS)[N][number];

/** Suits a policy evaluation, which takes microseconds in Go and a little more across the WASM boundary. */
export const EVALUATION_BUCKETS = [0.00005, 0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1];

/** Suits a webhook's alert count, from a single alert up to the thousand a webhook may carry. */
export const BATCH_BUCKETS = [1, 2, 5, 10, 25, 50, 100, 250, 500, 1000];

/** Prometheus' default buckets, which the Go service's request histogram uses. */
export const REQUEST_BUCKETS = [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10];

const RELOAD_SUCCESS = "success";
const RELOAD_FAILURE = "failure";

/**
 * The service's collectors on a registry of their own, so a test can build as
 * many as it likes and /metrics serves exactly what is registered here.
 */
export class PromMetrics implements Metrics {
  /** The registry the collectors live on, for callers that add their own. */
  readonly registry = new Registry();

  // Alerts and their routing.
  private readonly received = this.counter(
    "alertrouter_alerts_received_total",
    "Alerts received, by status as Alertmanager sent it (firing, resolved). Only firing alerts are routed.",
  );
  private readonly routed = this.counter(
    "alertrouter_alerts_routed_total",
    "Firing alerts routed, by team and outcome: routed by the team's policy, unowned by any team, invalid, or failed in evaluation. Every outcome ends in a notification; team is - for an unowned alert.",
  );
  private readonly batchSize = this.histogram(
    "alertrouter_webhook_batch_size",
    "Alerts per Alertmanager webhook received.",
    BATCH_BUCKETS,
  );
  private readonly notifications = this.counter(
    "alertrouter_notifications_total",
    "Notifications dispatched, by decision and destination: the paged target, the channel posted to, or - for a drop.",
  );
  private readonly notificationErrors = this.counter(
    "alertrouter_notification_errors_total",
    "Notifications a notifier failed to deliver, by decision. The webhook answers 503 so Alertmanager retries, and deduplication keeps the retry from notifying twice.",
  );
  private readonly deduplicated = this.counter(
    "alertrouter_notifications_deduplicated_total",
    "Notifications skipped because the same alert was dispatched with the same decision within the deduplication window, such as an Alertmanager redelivery, by decision.",
  );
  private readonly truncated = this.counter(
    "alertrouter_alerts_truncated_total",
    "Alerts of a group that no policy routed because a limit cut them off, by who cut them: alertmanager for the alerts its max_alerts left out of the webhook, alertrouter for the alerts past its own limit, which went to the fallback decision unevaluated.",
  );

  private readonly dedupEvicted = this.counter(
    "alertrouter_notifications_dedup_evicted_total",
    "Deduplication entries dropped before their window ended because the table reached ALERTROUTER_DEDUP_MAX_ENTRIES. A redelivery of an evicted alert can notify twice.",
  );

  // The guardrail and the engine.
  private readonly guardrailViolations = this.counter(
    "alertrouter_guardrail_violations_total",
    "Firing alerts whose team policy didn't page where platform.paging pages, by team. The platform's page was sent instead; the policy has a defect.",
  );
  private readonly engineRestarts = this.counter(
    "alertrouter_engine_restarts_total",
    "Sigil engine instances replaced after a trap or a hang, by engine: platform, the in-process instance that runs platform.paging, or worker, an evaluation worker.",
  );
  private readonly engineUp = this.gauge(
    "alertrouter_engine_up",
    "Whether a Sigil engine instance is serving (1) or down (0), by engine. /readyz answers 503 while the platform engine is down.",
  );

  // The console.
  private readonly streamsClosed = this.counter(
    "alertrouter_event_streams_closed_total",
    "Console event streams (SSE) alertrouter ended, by reason: slow, a client that fell behind, or limit, one refused because ALERTROUTER_MAX_EVENT_STREAMS were open.",
  );

  // Policy decisions.
  private readonly decisions = this.counter(
    "alertrouter_decisions_total",
    "Decisions a team's policy made, by team, evaluated policy, decision and reason. A failed evaluation made no decision and counts only in alertrouter_evaluation_errors_total, and an unowned or invalid alert is never evaluated.",
  );
  private readonly evaluationDuration = this.histogram(
    "alertrouter_evaluation_duration_seconds",
    "Time spent evaluating a team's policy against one alert.",
    EVALUATION_BUCKETS,
  );
  private readonly evaluationErrors = this.counter(
    "alertrouter_evaluation_errors_total",
    "Evaluations that failed, by team and kind of failure (assertion, runtime, conflict, timeout). A request the client canceled isn't a failure and isn't counted.",
  );

  // The policy bundle.
  private readonly reloads = this.counter(
    "alertrouter_policy_reloads_total",
    "Attempts to load the team policy bundle, by result. A failure keeps the previous bundle serving.",
  );
  private readonly lastReload = this.gauge(
    "alertrouter_policy_last_reload_timestamp_seconds",
    "Unix time of the last successful load of the team policy bundle; 0 before the first one.",
  );
  private readonly reloadSuccessful = this.gauge(
    "alertrouter_policy_last_reload_successful",
    "Whether the latest attempt to load the team policy bundle succeeded (1) or failed (0). A failed bundle stays 0 until a load succeeds, while the previous bundle keeps serving.",
  );
  private readonly loadedInfo = this.gauge(
    "alertrouter_policy_loaded_info",
    "One series per team policy currently serving, always 1, with the fingerprint and source of the bundle it was loaded from.",
  );

  // HTTP requests.
  private readonly requests = this.counter(
    "alertrouter_requests_total",
    "HTTP requests answered, by status code, method and route template.",
  );
  private readonly requestDuration = this.histogram(
    "alertrouter_request_duration_seconds",
    "Time spent answering an HTTP request, by method and route template.",
    REQUEST_BUCKETS,
  );

  constructor() {
    // Node's runtime and process metrics take the place of Go's runtime and
    // process collectors: event loop lag, heap, GC, handles, CPU and memory.
    // They're collected when /metrics is scraped, so no timer runs here.
    collectDefaultMetrics({ register: this.registry });
  }

  requestTimer(): (code: string, method: string, route: string) => void {
    const started = process.hrtime.bigint();
    return (code, method, route) => {
      this.requestDuration.observe({ method, route }, seconds(started));
      this.requests.inc({ code, method, route });
    };
  }

  observeBatch(size: number): void {
    this.batchSize.observe(size);
  }

  observeReceived(status: AlertStatus): void {
    this.received.inc({ status });
  }

  observeRouted(team: string, outcome: Outcome): void {
    this.routed.inc({ team, outcome });
  }

  observeNotification(decision: string, destination: string): void {
    this.notifications.inc({ decision, destination });
  }

  observeNotificationError(decision: string): void {
    this.notificationErrors.inc({ decision });
  }

  observeDeduplicated(decision: string): void {
    this.deduplicated.inc({ decision });
  }

  observeTruncated(by: "alertmanager" | "alertrouter", count: number): void {
    if (count > 0) this.truncated.inc({ by }, count);
  }

  observeDedupEvicted(count: number): void {
    if (count > 0) this.dedupEvicted.inc(count);
  }

  observeGuardrailViolation(team: string): void {
    this.guardrailViolations.inc({ team });
  }

  observeEngineRestart(engine: "platform" | "worker"): void {
    this.engineRestarts.inc({ engine });
  }

  setEngineUp(engine: "platform", up: boolean): void {
    this.engineUp.set({ engine }, up ? 1 : 0);
  }

  observeStreamClosed(reason: "slow" | "limit"): void {
    this.streamsClosed.inc({ reason });
  }

  evaluationTimer(team: string): () => number {
    return this.evaluationDuration.startTimer({ team });
  }

  observeDecision(team: string, policy: string, decision: string, reason: string): void {
    this.decisions.inc({ team, policy, decision, reason });
  }

  observeEvaluationError(team: string, kind: ErrorKind): void {
    this.evaluationErrors.inc({ team, kind });
  }

  prepareReloads(): void {
    // inc(0) creates the series without moving an existing one.
    this.reloads.inc({ result: RELOAD_SUCCESS }, 0);
    this.reloads.inc({ result: RELOAD_FAILURE }, 0);
  }

  observeReloadFailure(): void {
    // The last-reload time and the loaded-policy series stay where the last
    // good load left them: the bundle they describe still serves.
    this.reloads.inc({ result: RELOAD_FAILURE });
    this.reloadSuccessful.set(0);
  }

  observeReloadSuccess(at: Date, source: string, fingerprint: string, loaded: readonly LoadedPolicy[]): void {
    this.reloads.inc({ result: RELOAD_SUCCESS });
    this.lastReload.set(at.getTime() / 1000);
    this.reloadSuccessful.set(1);

    // Replaced, not added to, so a dropped team disappears from the gauge and
    // a changed bundle shows only its new fingerprint.
    this.loadedInfo.reset();
    for (const p of loaded) {
      this.loadedInfo.set({ team: p.team, policy: p.policy, fingerprint, source }, 1);
    }
  }

  async render(): Promise<{ contentType: string; body: string }> {
    return { contentType: this.registry.contentType, body: await this.registry.metrics() };
  }

  private counter<N extends MetricName>(name: N, help: string): Counter<LabelOf<N>> {
    return new Counter({ name, help, labelNames: METRIC_LABELS[name], registers: [this.registry] });
  }

  private gauge<N extends MetricName>(name: N, help: string): Gauge<LabelOf<N>> {
    return new Gauge({ name, help, labelNames: METRIC_LABELS[name], registers: [this.registry] });
  }

  private histogram<N extends MetricName>(name: N, help: string, buckets: number[]): Histogram<LabelOf<N>> {
    return new Histogram({ name, help, buckets, labelNames: METRIC_LABELS[name], registers: [this.registry] });
  }
}

/** Seconds since a process.hrtime.bigint() reading. */
function seconds(started: bigint): number {
  return Number(process.hrtime.bigint() - started) / 1e9;
}
