// setupTelemetry builds everything the service reports through: the logger,
// the tracer (and the process globals behind it), the metrics, and the
// profiler when Pyroscope is configured. The composition root calls it once;
// shutdown undoes it.

import type { SpanExporter } from "@opentelemetry/sdk-trace-base";
import type { DestinationStream } from "pino";
import { humane } from "../errors";
import { createLogger, type LogFormat, type ProcessLogger } from "./logger";
import { PromMetrics } from "./metrics";
import { type Profiler, startProfiler } from "./profiler";
import { type Env, startTracing, type Tracing } from "./tracing";
import type { Telemetry } from "./types";

export interface TelemetryConfig {
  /** The environment the OTEL_* and PYROSCOPE_* settings are read from. */
  env: Env;
  /** service.version on every span and the version tag of every profile; dev when empty. */
  version: string;
  logFormat: LogFormat;
  debug: boolean;
  /** Where JSON log lines go instead of standard output: a test's capture stream. */
  logDestination?: DestinationStream;
  /** Exports every span synchronously to this exporter: a test's in-memory exporter. */
  spanExporter?: SpanExporter;
  /** The metrics to report through; a fresh registry when unset. */
  metrics?: PromMetrics;
}

/** {@link Telemetry} with the concrete metrics, for /metrics and tests. */
export interface ServiceTelemetry extends Telemetry {
  readonly metrics: PromMetrics;
  readonly logger: ProcessLogger;
}

/**
 * Sets up the logger, tracing and, when PYROSCOPE_SERVER_ADDRESS is set,
 * profiling. It throws for an unknown log format, an invalid Pyroscope
 * address and a second setup before the first one's shutdown, since tracing
 * installs process globals.
 */
export async function setupTelemetry(cfg: TelemetryConfig): Promise<ServiceTelemetry> {
  const logger = await createLogger({ format: cfg.logFormat, debug: cfg.debug, destination: cfg.logDestination });
  const tracing = await startTracing({ env: cfg.env, version: cfg.version, exporter: cfg.spanExporter });

  let profiler: Profiler | undefined;
  try {
    profiler = await startProfiler(cfg.env, cfg.version, logger);
  } catch (err) {
    await tracing.shutdown();
    throw err;
  }

  return {
    tracer: tracing.tracer,
    metrics: cfg.metrics ?? new PromMetrics(),
    logger,
    shutdown: () => shutdown(tracing, profiler, logger),
  };
}

/**
 * Stops profiling, flushes buffered spans and the logger, and removes the
 * tracing globals. Every step runs even when an earlier one fails, and the
 * failures are reported together.
 */
async function shutdown(tracing: Tracing, profiler: Profiler | undefined, logger: ProcessLogger): Promise<void> {
  const results = await Promise.allSettled([profiler?.stop(), tracing.shutdown()]);
  await logger.flush();

  const failures = results.flatMap((r) => (r.status === "rejected" ? [r.reason] : []));
  if (failures.length > 0) {
    throw humane(
      `flushing telemetry on shutdown failed: ${failures.map((f) => (f instanceof Error ? f.message : String(f))).join("; ")}`,
      "buffered spans or profiles may be lost; check that the OTLP collector and Pyroscope are reachable",
    );
  }
}
