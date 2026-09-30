import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { context, SpanKind, SpanStatusCode, trace } from "@opentelemetry/api";
import { InMemorySpanExporter } from "@opentelemetry/sdk-trace-base";
import { goDuration, methodLabel, observeRequest, type RequestTelemetry, UNMATCHED_ROUTE } from "./http";
import { createLogger } from "./logger";
import { PromMetrics } from "./metrics";
import { startTracing, type Tracing } from "./tracing";

const exporter = new InMemorySpanExporter();
let tracing: Tracing;
let lines: Record<string, unknown>[];
let deps: RequestTelemetry & { metrics: PromMetrics };

beforeAll(async () => {
  tracing = await startTracing({ env: {}, version: "", exporter });
});
afterAll(async () => {
  await tracing.shutdown();
});
beforeEach(async () => {
  exporter.reset();
  lines = [];
  const logger = await createLogger({
    format: "json",
    debug: false,
    destination: { write: (line: string) => lines.push(JSON.parse(line) as Record<string, unknown>) },
  });
  deps = { tracer: tracing.tracer, metrics: new PromMetrics(), logger };
});

async function requests(labels: string): Promise<number> {
  const body = (await deps.metrics.render()).body;
  const line = body.split("\n").find((l) => l.startsWith(`alertrouter_requests_total{${labels}} `));
  return line ? Number(line.split(" ")[1]) : 0;
}

describe("observeRequest", () => {
  test("answers inside a server span named after the route template, which continues the caller's trace", async () => {
    const traceId = "4bf92f3577b34da6a3ce929d0e0e4736";
    const req = new Request("http://alertrouter/api/v1/teams/checkout/route?dry=1", {
      method: "POST",
      headers: { traceparent: `00-${traceId}-00f067aa0ba902b7-01`, "user-agent": "k6" },
    });
    let inside: string | undefined;
    const res = await observeRequest(deps, "/api/v1/teams/:team/route", req, async () => {
      inside = trace.getActiveSpan()?.spanContext().spanId;
      deps.tracer.startSpan("alertrouter.route").end();
      return new Response("{}", { status: 200 });
    });
    expect(res.status).toBe(200);

    const spans = exporter.getFinishedSpans();
    const server = spans.find((s) => s.name === "POST /api/v1/teams/:team/route");
    const route = spans.find((s) => s.name === "alertrouter.route");
    expect(server?.kind).toBe(SpanKind.SERVER);
    expect(server?.spanContext().traceId).toBe(traceId);
    expect(server?.parentSpanContext?.spanId).toBe("00f067aa0ba902b7");
    expect(server?.spanContext().spanId).toBe(inside ?? "");
    expect(route?.parentSpanContext?.spanId).toBe(inside ?? "");
    expect(server?.attributes).toMatchObject({
      "http.request.method": "POST",
      "http.route": "/api/v1/teams/:team/route",
      "url.path": "/api/v1/teams/checkout/route",
      "url.query": "dry=1",
      "user_agent.original": "k6",
      "http.response.status_code": 200,
    });
    expect(server?.status.code).toBe(SpanStatusCode.UNSET);

    expect(await requests('code="200",method="POST",route="/api/v1/teams/:team/route"')).toBe(1);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatchObject({
      msg: "/api/v1/teams/checkout/route",
      status: 200,
      method: "POST",
      path: "/api/v1/teams/checkout/route",
      query: "dry=1",
      "user-agent": "k6",
      trace_id: traceId,
      span_id: inside,
    });
    expect(String(lines[0]?.latency)).toMatch(/^[\d.]+(ns|µs|ms|s)$/);
  });

  test("ignores the span the framework runs it in: the request span is a root without a traceparent", async () => {
    const outer = deps.tracer.startSpan("framework");
    await context.with(trace.setSpan(context.active(), outer), () =>
      observeRequest(
        deps,
        "/api/v1/alerts",
        new Request("http://alertrouter/api/v1/alerts"),
        async () => new Response(),
      ),
    );
    outer.end();

    const server = exporter.getFinishedSpans().find((s) => s.name === "GET /api/v1/alerts");
    expect(server?.parentSpanContext).toBeUndefined();
    expect(server?.spanContext().traceId).not.toBe(outer.spanContext().traceId);
  });

  test.each([
    [200, SpanStatusCode.UNSET],
    [404, SpanStatusCode.UNSET],
    [499, SpanStatusCode.UNSET],
    [500, SpanStatusCode.ERROR],
    [503, SpanStatusCode.ERROR],
  ])("a %d answer leaves the span status %p", async (status, want) => {
    await observeRequest(deps, "/api/v1/alerts", new Request("http://alertrouter/api/v1/alerts"), async () => {
      return new Response(null, { status });
    });
    expect(exporter.getFinishedSpans()[0]?.status.code).toBe(want);
    expect(await requests(`code="${status}",method="GET",route="/api/v1/alerts"`)).toBe(1);
  });

  test("records a handler's exception, counts it as a 500 and rethrows it", async () => {
    const req = new Request("http://alertrouter/api/v1/alerts", { method: "POST" });
    await expect(
      observeRequest(deps, "/api/v1/alerts", req, async () => {
        throw new Error("boom");
      }),
    ).rejects.toThrow("boom");

    const [span] = exporter.getFinishedSpans();
    expect(span?.status.code).toBe(SpanStatusCode.ERROR);
    expect(span?.events.map((e) => e.name)).toContain("exception");
    expect(await requests('code="500",method="POST",route="/api/v1/alerts"')).toBe(1);
  });

  test.each([
    ["/healthz", 1],
    ["/readyz", 1],
    ["/metrics", 0],
  ])("%s gets no span and no access line, and is counted %d times", async (path, counted) => {
    const res = await observeRequest(
      deps,
      path,
      new Request(`http://alertrouter${path}`),
      async () => new Response("ok"),
    );
    expect(res.status).toBe(200);
    expect(exporter.getFinishedSpans()).toHaveLength(0);
    expect(lines).toHaveLength(0);
    expect(await requests(`code="200",method="GET",route="${path}"`)).toBe(counted);
  });

  test("labels an unmatched request with a fixed route and a non-standard method as other", async () => {
    await observeRequest(
      deps,
      UNMATCHED_ROUTE,
      new Request("http://alertrouter/wp-admin", { method: "PROPFIND" }),
      async () => new Response(null, { status: 404 }),
    );
    expect(await requests('code="404",method="other",route="unmatched"')).toBe(1);
  });
});

describe("methodLabel", () => {
  test.each([
    ["GET", "GET"],
    ["POST", "POST"],
    ["PROPFIND", "other"],
    ["get", "other"],
  ])("%s → %s", (method, want) => {
    expect(methodLabel(method)).toBe(want);
  });
});

describe("goDuration", () => {
  test.each([
    [0, "0s"],
    [0.0000005, "500ns"],
    [0.0008125, "812.5µs"],
    [0.001234567, "1.234567ms"],
    [2.5, "2.5s"],
    [90, "1m30s"],
    [3600, "1h0m0s"],
    [3725.5, "1h2m5.5s"],
  ])("%p seconds → %s", (seconds, want) => {
    expect(goDuration(seconds)).toBe(want);
  });
});
