// Tracing: a tracer provider for the spans alertrouter starts itself, the
// OTLP exporter the standard OTEL_* environment picks, and the process
// globals (context manager, propagator, provider) that make the active span
// follow async calls and a traced client's traceparent header continue its
// trace here.

import {
  type Context,
  type ContextManager,
  context,
  ProxyTracerProvider,
  propagation,
  type Span,
  type SpanOptions,
  type Tracer,
  type TracerProvider,
  trace,
} from "@opentelemetry/api";
import { AsyncLocalStorageContextManager } from "@opentelemetry/context-async-hooks";
import { CompositePropagator, W3CBaggagePropagator, W3CTraceContextPropagator } from "@opentelemetry/core";
import { defaultResource, type Resource, resourceFromAttributes } from "@opentelemetry/resources";
import {
  BasicTracerProvider,
  BatchSpanProcessor,
  SimpleSpanProcessor,
  type SpanExporter,
  type SpanProcessor,
} from "@opentelemetry/sdk-trace-base";
import { ATTR_SERVICE_NAME, ATTR_SERVICE_VERSION } from "@opentelemetry/semantic-conventions";
import { humane } from "../errors";
import { DEFAULT_SERVICE_NAME, TRACER_NAME } from "./types";

/**
 * Instrumentation scopes whose spans alertrouter doesn't record. Next.js
 * starts a server span named "POST /api/v1/alerts" of its own for every
 * request; alertrouter's request span (see http.ts) has the same name and is
 * the one the route spans hang off, so Next's would put a second span of that
 * name in every trace. Hidden scopes get a no-op tracer, whose spans only
 * carry the incoming trace context through.
 */
export const HIDDEN_SCOPES: readonly string[] = ["next.js"];

/** The environment the OTEL_* and PYROSCOPE_* settings are read from. */
export type Env = Readonly<Record<string, string | undefined>>;

/** Where the OTLP trace exporter sends spans. */
export interface ExporterTarget {
  protocol: "grpc" | "http";
  /** The collector's URL: the base URL for gRPC, the /v1/traces URL for HTTP. */
  url: string;
}

/** What {@link startTracing} returns: the tracer and how to stop it. */
export interface Tracing {
  readonly tracer: Tracer;
  readonly provider: TracerProvider;
  /** Flushes buffered spans and puts the previous process globals back. */
  shutdown(): Promise<void>;
}

export interface TracingOptions {
  env: Env;
  /** service.version on every span; dev when empty. */
  version: string;
  /**
   * Exports every span synchronously to this exporter instead of the one the
   * environment picks, so a test reads its spans as soon as they end.
   */
  exporter?: SpanExporter;
}

/**
 * Builds the tracer provider and installs it, the async context manager and
 * the W3C trace-context and baggage propagator as the process globals.
 * Spans are always created, so trace ids show up in the logs, but they're
 * exported only when OTEL_EXPORTER_OTLP_ENDPOINT (or its _TRACES_ variant) is
 * set and OTEL_TRACES_EXPORTER isn't "none".
 *
 * It replaces process globals, so call it once, and call shutdown before
 * calling it again.
 */
export async function startTracing(opts: TracingOptions): Promise<Tracing> {
  const processors: SpanProcessor[] = [];
  if (opts.exporter) {
    processors.push(new SimpleSpanProcessor(opts.exporter));
  } else {
    const target = exporterTarget(opts.env);
    if (target) processors.push(new BatchSpanProcessor(await newExporter(target)));
  }

  const sdk = new BasicTracerProvider({ resource: newResource(opts.env, opts.version), spanProcessors: processors });
  const provider = new ScopedTracerProvider(sdk, HIDDEN_SCOPES);
  const contextManager: ContextManager = new AsyncLocalStorageContextManager().enable();

  // Each global is set only if none is: a failed setup undoes what it set
  // itself and leaves an earlier setup's globals alone.
  const contextSet = context.setGlobalContextManager(contextManager);
  const propagatorSet =
    contextSet &&
    // The default global propagator drops incoming trace context, which
    // would start a fresh trace for every request a traced client makes.
    propagation.setGlobalPropagator(
      new CompositePropagator({ propagators: [new W3CTraceContextPropagator(), new W3CBaggagePropagator()] }),
    );
  const providerSet = propagatorSet && trace.setGlobalTracerProvider(provider);
  if (!providerSet) {
    if (propagatorSet) propagation.disable();
    if (contextSet) context.disable();
    contextManager.disable();
    await sdk.shutdown();
    throw humane(
      "OpenTelemetry is already set up in this process",
      "shut the previous telemetry down before setting it up again",
    );
  }

  return {
    tracer: provider.getTracer(TRACER_NAME, opts.version || "dev"),
    provider,
    async shutdown() {
      try {
        await sdk.shutdown();
      } finally {
        uninstall();
        contextManager.disable();
      }
    },
  };
}

/**
 * Picks the OTLP exporter from the standard environment, the way the Go
 * service does: OTEL_EXPORTER_OTLP_TRACES_ENDPOINT or
 * OTEL_EXPORTER_OTLP_ENDPOINT, as a URL or a bare host:port. An http scheme
 * or OTEL_EXPORTER_OTLP_INSECURE=true means plaintext. The protocol comes from
 * OTEL_EXPORTER_OTLP_PROTOCOL, or, when that is unset, from the port: 4317 is
 * OTLP's gRPC port, anything else speaks HTTP. Returns undefined when nothing
 * should be exported.
 */
export function exporterTarget(env: Env): ExporterTarget | undefined {
  if (env.OTEL_TRACES_EXPORTER === "none") return undefined;
  const raw = env.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT || env.OTEL_EXPORTER_OTLP_ENDPOINT;
  if (!raw) return undefined;

  const { hostPort, scheme } = splitEndpoint(raw);
  const insecure = scheme === "http" || env.OTEL_EXPORTER_OTLP_INSECURE === "true";
  const base = `${insecure ? "http" : "https"}://${hostPort}`;
  if (speaksGRPC(hostPort, env.OTEL_EXPORTER_OTLP_PROTOCOL)) return { protocol: "grpc", url: base };
  return { protocol: "http", url: `${base}/v1/traces` };
}

/**
 * Returns the host:port of an OTLP endpoint given as a URL or a bare
 * host:port, and its scheme, empty for a bare host:port.
 */
export function splitEndpoint(raw: string): { hostPort: string; scheme: string } {
  if (!raw.includes("://")) return { hostPort: raw, scheme: "" };
  try {
    const url = new URL(raw);
    if (!url.host) return { hostPort: raw, scheme: "" };
    return { hostPort: url.host, scheme: url.protocol.replace(/:$/, "") };
  } catch {
    return { hostPort: raw, scheme: "" };
  }
}

/**
 * Reports whether the exporter should speak gRPC: when the protocol says so,
 * or, without one, when the endpoint is on the OTLP gRPC port.
 */
export function speaksGRPC(hostPort: string, protocol: string | undefined): boolean {
  switch (protocol) {
    case "grpc":
      return true;
    case "http/protobuf":
    case "http/json":
      return false;
  }
  return /:4317$/.test(hostPort);
}

/** The resource every span carries: service.name and service.version. */
export function newResource(env: Env, version: string): Resource {
  return defaultResource().merge(
    resourceFromAttributes({
      [ATTR_SERVICE_NAME]: env.OTEL_SERVICE_NAME || DEFAULT_SERVICE_NAME,
      [ATTR_SERVICE_VERSION]: version || "dev",
    }),
  );
}

/**
 * Loads the exporter for target on demand: the gRPC exporter pulls in
 * @grpc/grpc-js, which a process that exports over HTTP, or not at all,
 * never needs.
 */
async function newExporter(target: ExporterTarget): Promise<SpanExporter> {
  if (target.protocol === "grpc") {
    const { OTLPTraceExporter } = await import("@opentelemetry/exporter-trace-otlp-grpc");
    return new OTLPTraceExporter({ url: target.url });
  }
  const { OTLPTraceExporter } = await import("@opentelemetry/exporter-trace-otlp-proto");
  return new OTLPTraceExporter({ url: target.url });
}

/** Removes the process globals startTracing installed. */
function uninstall(): void {
  trace.disable();
  propagation.disable();
  context.disable();
}

/**
 * A tracer provider that hands the scopes in hidden a no-op tracer and every
 * other scope the SDK's. A no-op span continues the context it started in,
 * so spans started inside one still join the incoming trace.
 */
export class ScopedTracerProvider implements TracerProvider {
  private readonly noop = new ProxyTracerProvider();

  constructor(
    private readonly inner: TracerProvider,
    private readonly hidden: readonly string[],
  ) {}

  getTracer(name: string, version?: string, options?: { schemaUrl?: string }): Tracer {
    if (this.hidden.includes(name)) return this.noop.getTracer(name, version, options);
    return this.inner.getTracer(name, version, options);
  }
}

/**
 * Runs fn in a new active span called name and ends the span when fn
 * returns or its promise settles. An exception fn throws is recorded on the
 * span and rethrown; the span's status is fn's business, since only the
 * caller knows whether an outcome is a failure.
 */
export function withSpan<T>(
  tracer: Tracer,
  name: string,
  options: SpanOptions,
  fn: (span: Span) => T,
  parent: Context = context.active(),
): T {
  return tracer.startActiveSpan(name, options, parent, (span) => {
    let result: T;
    try {
      result = fn(span);
    } catch (err) {
      span.recordException(asException(err));
      span.end();
      throw err;
    }
    if (result instanceof Promise) {
      return result.then(
        (value) => {
          span.end();
          return value;
        },
        (err: unknown) => {
          span.recordException(asException(err));
          span.end();
          throw err;
        },
      ) as T;
    }
    span.end();
    return result;
  });
}

/** An exception OpenTelemetry can record, from anything thrown. */
export function asException(err: unknown): Error {
  return err instanceof Error ? err : new Error(String(err));
}
