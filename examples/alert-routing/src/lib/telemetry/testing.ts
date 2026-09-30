// Telemetry for tests: the real logger, tracer and metrics, with the spans
// kept in memory and the log lines captured, so a test reads back exactly
// what the service reported. It installs the same process globals as the
// real setup, so a test file uses one at a time and shuts it down after.

import type { ReadableSpan } from "@opentelemetry/sdk-trace-base";
import { InMemorySpanExporter } from "@opentelemetry/sdk-trace-base";
import { type ServiceTelemetry, setupTelemetry } from "./telemetry";
import type { Env } from "./tracing";

export interface TestTelemetry {
  readonly telemetry: ServiceTelemetry;
  /** Every span that ended, in the order they ended; they stay readable after shutdown. */
  spans(): ReadableSpan[];
  /** The spans called name. */
  spansNamed(name: string): ReadableSpan[];
  /** Every log line, decoded. */
  logs(): Record<string, unknown>[];
  /** The lines logged with msg. */
  logsWith(msg: string): Record<string, unknown>[];
  /** GET /metrics as the service would answer it. */
  metricsText(): Promise<string>;
  /** Forgets the spans and log lines so far; the metrics keep counting. */
  reset(): void;
  /** Shuts the telemetry down, which frees the process globals for the next one. */
  shutdown(): Promise<void>;
}

/**
 * Sets up telemetry for a test: debug logging into memory, every span kept,
 * and nothing exported or profiled whatever the environment says.
 */
export async function createTestTelemetry(opts: { env?: Env; debug?: boolean } = {}): Promise<TestTelemetry> {
  const spans: ReadableSpan[] = [];
  const lines: Record<string, unknown>[] = [];

  const telemetry = await setupTelemetry({
    env: { ...opts.env, OTEL_TRACES_EXPORTER: "none", PYROSCOPE_SERVER_ADDRESS: "" },
    version: "test",
    logFormat: "json",
    debug: opts.debug ?? true,
    logDestination: { write: (line: string) => lines.push(JSON.parse(line) as Record<string, unknown>) },
    spanExporter: new RecordingExporter(spans),
  });

  return {
    telemetry,
    spans: () => [...spans],
    spansNamed: (name) => spans.filter((s) => s.name === name),
    logs: () => [...lines],
    logsWith: (msg) => lines.filter((l) => l.msg === msg),
    metricsText: async () => (await telemetry.metrics.render()).body,
    reset: () => {
      spans.length = 0;
      lines.length = 0;
    },
    shutdown: () => telemetry.shutdown(),
  };
}

/**
 * An in-memory exporter whose spans outlive its shutdown, which empties
 * InMemorySpanExporter's own list.
 */
class RecordingExporter extends InMemorySpanExporter {
  constructor(private readonly into: ReadableSpan[]) {
    super();
  }

  override export(spans: ReadableSpan[], done: Parameters<InMemorySpanExporter["export"]>[1]): void {
    this.into.push(...spans);
    super.export(spans, done);
  }
}
