// The observability every HTTP request gets, in the Go service's middleware
// order: a server span named after the route template, an access log line
// carrying the trace ids, and the request metrics. Probes and scrapes are
// hit every few seconds, so they get no span and no access line, and
// /metrics isn't counted, or it would dominate the request rate.

import { context, propagation, ROOT_CONTEXT, SpanKind, SpanStatusCode, type Tracer, trace } from "@opentelemetry/api";
import { asException } from "./tracing";
import type { Logger, Metrics } from "./types";

/** Probe and scrape paths: no span, no access log line. */
export const QUIET_PATHS: readonly string[] = ["/healthz", "/readyz", "/metrics"];

/** The path whose requests aren't counted. */
export const METRICS_PATH = "/metrics";

/** The route label of a request no route matched. */
export const UNMATCHED_ROUTE = "unmatched";

/** The methods the request metrics name; any other is "other". */
export const STANDARD_METHODS: readonly string[] = [
  "GET",
  "HEAD",
  "POST",
  "PUT",
  "PATCH",
  "DELETE",
  "CONNECT",
  "OPTIONS",
  "TRACE",
];

/** What {@link observeRequest} reports through. */
export interface RequestTelemetry {
  tracer: Tracer;
  metrics: Metrics;
  logger: Logger;
}

/**
 * Answers req with handle, observed: handle runs in the request's server
 * span, named "<METHOD> <route>", which continues the trace of an incoming
 * traceparent header. route is the route template, in the Go service's
 * syntax (/api/v1/teams/:team/route), or {@link UNMATCHED_ROUTE}; it's the
 * span name and the metrics' route label, so it must never be the raw path.
 *
 * A 5xx marks the span failed. An exception handle throws is recorded, counted
 * as a 500 and rethrown.
 */
export async function observeRequest(
  deps: RequestTelemetry,
  route: string,
  req: Request,
  handle: () => Promise<Response>,
): Promise<Response> {
  const url = new URL(req.url);
  const method = methodLabel(req.method);
  const done = url.pathname === METRICS_PATH ? undefined : deps.metrics.requestTimer();
  const started = performance.now();

  if (QUIET_PATHS.includes(url.pathname)) {
    let code = 500;
    try {
      const res = await handle();
      code = res.status;
      return res;
    } finally {
      done?.(String(code), method, route);
    }
  }

  // The parent comes from the request's headers alone, never from whatever
  // context the framework runs the handler in, so this is the request's
  // server span with or without Next around it, and a trace holds one span
  // of this name.
  const parent = propagation.extract(ROOT_CONTEXT, req.headers, headerGetter);
  const span = deps.tracer.startSpan(
    `${req.method} ${route}`,
    {
      kind: SpanKind.SERVER,
      attributes: {
        "http.request.method": req.method,
        "http.route": route,
        "url.path": url.pathname,
        "url.scheme": url.protocol.replace(/:$/, ""),
        ...(url.search ? { "url.query": url.search.slice(1) } : {}),
        ...(req.headers.get("user-agent") ? { "user_agent.original": req.headers.get("user-agent") ?? "" } : {}),
      },
    },
    parent,
  );

  const inSpan = trace.setSpan(parent, span);
  let code = 500;
  try {
    const res = await context.with(
      // The span, not the extracted remote parent, is what handle sees as
      // active, so its spans and log lines hang off the request.
      inSpan,
      handle,
    );
    code = res.status;
    return res;
  } catch (err) {
    span.recordException(asException(err));
    throw err;
  } finally {
    span.setAttribute("http.response.status_code", code);
    if (code >= 500) span.setStatus({ code: SpanStatusCode.ERROR, message: `HTTP ${code}` });
    // The access line is written inside the span, so it carries its ids.
    context.with(inSpan, () =>
      deps.logger.info(url.pathname, {
        status: code,
        method: req.method,
        path: url.pathname,
        query: url.search.slice(1),
        "user-agent": req.headers.get("user-agent") ?? "",
        latency: goDuration((performance.now() - started) / 1000),
      }),
    );
    span.end();
    done?.(String(code), method, route);
  }
}

/** The request metrics' method label: the method when it's a standard one, "other" otherwise. */
export function methodLabel(method: string): string {
  return STANDARD_METHODS.includes(method) ? method : "other";
}

/**
 * Formats seconds the way Go's time.Duration prints, such as 812.5µs,
 * 1.234567ms or 2.5s, so the access line's latency reads like the Go
 * service's.
 */
export function goDuration(seconds: number): string {
  const ns = Math.round(seconds * 1e9);
  if (ns === 0) return "0s";
  const sign = ns < 0 ? "-" : "";
  const abs = Math.abs(ns);
  if (abs < 1e3) return `${sign}${abs}ns`;
  if (abs < 1e6) return `${sign}${trim(abs / 1e3)}µs`;
  if (abs < 1e9) return `${sign}${trim(abs / 1e6)}ms`;

  let rest = abs / 1e9;
  const h = Math.floor(rest / 3600);
  rest -= h * 3600;
  const m = Math.floor(rest / 60);
  rest -= m * 60;
  return `${sign}${h ? `${h}h` : ""}${h || m ? `${m}m` : ""}${trim(rest)}s`;
}

/** A number with at most nine decimals and no trailing zeros. */
function trim(n: number): string {
  return String(Number(n.toFixed(9)));
}

const headerGetter = {
  keys: (carrier: Headers) => [...carrier.keys()],
  get: (carrier: Headers, key: string) => carrier.get(key) ?? undefined,
};
