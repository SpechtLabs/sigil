// The telemetry contract: what the service calls to report about itself. The
// implementation (OpenTelemetry, prom-client, pino, Pyroscope) lives next to
// this file; the service only ever sees these interfaces, so a test hands it
// an in-memory implementation and reads back what was reported.

import type { Tracer } from "@opentelemetry/api";

/** The instrumentation scope of the spans alertrouter starts itself. */
export const TRACER_NAME = "github.com/spechtlabs/sigil/examples/alert-routing/alertrouter";

/** service.name when OTEL_SERVICE_NAME is unset. */
export const DEFAULT_SERVICE_NAME = "alertrouter";

/**
 * The team label of an alert no team in the directory owns. The alert's own
 * team label is chosen by whoever wrote the alert rule, so it never becomes a
 * label value: every distinct one would be a new series.
 */
export const NO_TEAM = "-";

/** The alert statuses alertrouter_alerts_received_total counts. */
export type AlertStatus = "firing" | "resolved";

/** The outcomes alertrouter_alerts_routed_total counts, one per firing alert. */
export type Outcome = "routed" | "unowned" | "invalid" | "failed";

/** The Sigil engines alertrouter runs: the in-process one for platform.paging, and the evaluation workers. */
export type Engine = "platform" | "worker";

/** The failure kinds alertrouter_evaluation_errors_total counts. */
export type ErrorKind = "assertion" | "runtime" | "conflict" | "timeout";

/** One team policy a bundle serves, for alertrouter_policy_loaded_info. */
export interface LoadedPolicy {
  team: string;
  policy: string;
}

/**
 * The service's Prometheus metrics, on a registry of its own. Names, labels,
 * help texts and buckets match the Go service's internal/telemetry/metrics.go.
 */
export interface Metrics {
  /**
   * Starts timing one HTTP request. Call the returned function once the
   * request is answered, with its labels; the caller keeps them bounded.
   */
  requestTimer(): (code: string, method: string, route: string) => void;
  /** alertrouter_webhook_batch_size */
  observeBatch(size: number): void;
  /** alertrouter_alerts_received_total{status} */
  observeReceived(status: AlertStatus): void;
  /** alertrouter_alerts_routed_total{team,outcome}; team is NO_TEAM for an unowned alert. */
  observeRouted(team: string, outcome: Outcome): void;
  /** alertrouter_notifications_total{decision,destination}; destination "-" for a drop. */
  observeNotification(decision: string, destination: string): void;
  /**
   * Starts timing one evaluation of team's policy
   * (alertrouter_evaluation_duration_seconds{team}). The returned function
   * records it and returns the seconds it took.
   */
  evaluationTimer(team: string): () => number;
  /**
   * alertrouter_notification_errors_total{decision}: a notifier that failed to
   * deliver. Not in the Go service (lead addendum item 3).
   */
  observeNotificationError(decision: string): void;
  /**
   * alertrouter_notifications_deduplicated_total{decision}: a notification
   * skipped because the same alert (fingerprint) was dispatched with the same
   * decision within the dedup TTL, such as an Alertmanager redelivery. Not in
   * the Go service (lead addendum item 2).
   */
  observeDeduplicated(decision: string): void;
  /**
   * alertrouter_alerts_truncated_total{by}, increased by count: alerts of a
   * group that weren't routed by a policy because a limit cut them off.
   * by is "alertmanager" for the alerts Alertmanager left out of the webhook
   * (its truncatedAlerts, the receiver's max_alerts), and "alertrouter" for
   * the alerts past MAX_ALERTS that went to the fallback unevaluated. Not in
   * the Go service (lead addendum item 4).
   */
  observeTruncated(by: "alertmanager" | "alertrouter", count: number): void;
  /**
   * alertrouter_guardrail_violations_total{team}: platform.paging paged for
   * an alert and the team's policy decided something else than a page to
   * the same target, so the platform's page went out instead.
   */
  observeGuardrailViolation(team: string): void;
  /**
   * alertrouter_notifications_dedup_evicted_total, by count: deduplication
   * entries dropped before their TTL because the table was full.
   */
  observeDedupEvicted(count: number): void;
  /**
   * alertrouter_engine_restarts_total{engine}: a Sigil module stopped or
   * hung and was replaced; "platform" runs platform.paging in process,
   * "worker" is one of the evaluation workers.
   */
  observeEngineRestart(engine: Engine): void;
  /** alertrouter_engine_up{engine}: whether the platform engine is running (1) or being replaced (0). */
  setEngineUp(engine: "platform", up: boolean): void;
  /**
   * alertrouter_event_streams_closed_total{reason}: console event streams
   * closed because the client fell behind ("slow") or refused because too
   * many were open ("limit").
   */
  observeStreamClosed(reason: "slow" | "limit"): void;
  /** alertrouter_decisions_total{team,policy,decision,reason} */
  observeDecision(team: string, policy: string, decision: string, reason: string): void;
  /** alertrouter_evaluation_errors_total{team,kind} */
  observeEvaluationError(team: string, kind: ErrorKind): void;
  /** Creates both results of alertrouter_policy_reloads_total at 0. */
  prepareReloads(): void;
  /** Counts a rejected load and sets policy_last_reload_successful to 0. */
  observeReloadFailure(): void;
  /**
   * Counts a successful load at `at`, sets the last-reload gauges, and
   * replaces the policy_loaded_info series with `loaded`.
   */
  observeReloadSuccess(at: Date, source: string, fingerprint: string, loaded: readonly LoadedPolicy[]): void;
  /** The registry in the Prometheus text format, for GET /metrics. */
  render(): Promise<{ contentType: string; body: string }>;
}

export type LogLevel = "debug" | "info" | "warn" | "error";
export type LogFields = Record<string, unknown>;

/**
 * The process logger. Every line carries trace_id and span_id of the active
 * span, exactly once, when there is one. Like otelzap in the Go service, a
 * warn line also adds an event to the active span and an error line marks it
 * failed.
 */
export interface Logger {
  log(level: LogLevel, msg: string, fields?: LogFields): void;
  debug(msg: string, fields?: LogFields): void;
  info(msg: string, fields?: LogFields): void;
  warn(msg: string, fields?: LogFields): void;
  error(msg: string, fields?: LogFields): void;
}

/** Everything the service reports through, built once in the composition root. */
export interface Telemetry {
  readonly tracer: Tracer;
  readonly metrics: Metrics;
  readonly logger: Logger;
  /** Stops profiling and flushes spans and logs. Call it last. */
  shutdown(): Promise<void>;
}
