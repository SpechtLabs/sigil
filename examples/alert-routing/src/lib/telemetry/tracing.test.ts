import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { context, propagation, SpanStatusCode, trace } from "@opentelemetry/api";
import { InMemorySpanExporter } from "@opentelemetry/sdk-trace-base";
import {
  exporterTarget,
  newResource,
  speaksGRPC,
  splitEndpoint,
  startTracing,
  type Tracing,
  withSpan,
} from "./tracing";
import { DEFAULT_SERVICE_NAME, TRACER_NAME } from "./types";

describe("splitEndpoint", () => {
  test.each([
    ["http://otel-collector:4317", "otel-collector:4317", "http"],
    ["https://collector.example.com:4318", "collector.example.com:4318", "https"],
    ["otel-collector:4317", "otel-collector:4317", ""],
    ["localhost:4318", "localhost:4318", ""],
  ])("%s", (raw, hostPort, scheme) => {
    expect(splitEndpoint(raw)).toEqual({ hostPort, scheme });
  });
});

describe("speaksGRPC", () => {
  test.each([
    ["grpc port", "collector:4317", undefined, true],
    ["http port", "collector:4318", undefined, false],
    ["protocol wins over port", "collector:4317", "http/protobuf", false],
    ["grpc on another port", "collector:9000", "grpc", true],
    ["no port", "collector", undefined, false],
  ])("%s", (_name, hostPort, protocol, want) => {
    expect(speaksGRPC(hostPort, protocol)).toBe(want);
  });
});

describe("exporterTarget", () => {
  test.each([
    ["no endpoint", {}, undefined],
    [
      "exporter none",
      { OTEL_EXPORTER_OTLP_ENDPOINT: "http://collector:4317", OTEL_TRACES_EXPORTER: "none" },
      undefined,
    ],
    [
      "plaintext grpc",
      { OTEL_EXPORTER_OTLP_ENDPOINT: "http://collector:4317" },
      { protocol: "grpc", url: "http://collector:4317" },
    ],
    [
      "tls http",
      { OTEL_EXPORTER_OTLP_ENDPOINT: "https://collector:4318" },
      { protocol: "http", url: "https://collector:4318/v1/traces" },
    ],
    [
      "traces endpoint wins",
      { OTEL_EXPORTER_OTLP_ENDPOINT: "http://other:4317", OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: "collector:4318" },
      { protocol: "http", url: "https://collector:4318/v1/traces" },
    ],
    [
      "insecure by flag",
      { OTEL_EXPORTER_OTLP_ENDPOINT: "collector:4317", OTEL_EXPORTER_OTLP_INSECURE: "true" },
      { protocol: "grpc", url: "http://collector:4317" },
    ],
    [
      "protocol from the environment",
      { OTEL_EXPORTER_OTLP_ENDPOINT: "http://collector:4317", OTEL_EXPORTER_OTLP_PROTOCOL: "http/protobuf" },
      { protocol: "http", url: "http://collector:4317/v1/traces" },
    ],
  ] as const)("%s", (_name, env, want) => {
    expect(exporterTarget(env)).toEqual(want);
  });
});

describe("newResource", () => {
  test.each([
    ["defaults", {}, "", DEFAULT_SERVICE_NAME, "dev"],
    ["from the environment", { OTEL_SERVICE_NAME: "router-canary" }, "v1.2.3", "router-canary", "v1.2.3"],
  ])("%s", (_name, env, version, wantName, wantVersion) => {
    const attrs = newResource(env, version).attributes;
    expect(attrs["service.name"]).toBe(wantName);
    expect(attrs["service.version"]).toBe(wantVersion);
  });
});

describe("startTracing", () => {
  // A fresh exporter per test: shutting tracing down stops its exporter.
  let tracing: Tracing | undefined;
  let exporter: InMemorySpanExporter;

  beforeEach(() => {
    exporter = new InMemorySpanExporter();
  });
  afterEach(async () => {
    await tracing?.shutdown();
    tracing = undefined;
  });

  test("records alertrouter's spans with the service resource and scope", async () => {
    tracing = await startTracing({ env: { OTEL_SERVICE_NAME: "router-test" }, version: "v9", exporter });
    tracing.tracer.startSpan("alertrouter.route").end();

    const [span] = exporter.getFinishedSpans();
    expect(span?.name).toBe("alertrouter.route");
    expect(span?.instrumentationScope.name).toBe(TRACER_NAME);
    expect(span?.resource.attributes["service.name"]).toBe("router-test");
    expect(span?.resource.attributes["service.version"]).toBe("v9");
  });

  test("hides Next.js's spans but keeps its context flowing into alertrouter's", async () => {
    tracing = await startTracing({ env: {}, version: "", exporter });
    const next = trace.getTracer("next.js", "0.0.1");

    const parent = tracing.tracer.startSpan("parent");
    context.with(trace.setSpan(context.active(), parent), () => {
      const hidden = next.startSpan("POST /api/v1/alerts");
      expect(hidden.isRecording()).toBe(false);
      context.with(trace.setSpan(context.active(), hidden), () => tracing?.tracer.startSpan("child").end());
      hidden.end();
    });
    parent.end();

    const spans = exporter.getFinishedSpans();
    expect(spans.map((s) => s.name)).toEqual(["child", "parent"]);
    expect(spans[0]?.parentSpanContext?.spanId).toBe(parent.spanContext().spanId);
  });

  test("installs the W3C propagator, so a traceparent header continues its trace", async () => {
    tracing = await startTracing({ env: {}, version: "", exporter });
    const traceId = "4bf92f3577b34da6a3ce929d0e0e4736";
    const remote = propagation.extract(context.active(), { traceparent: `00-${traceId}-00f067aa0ba902b7-01` });
    tracing.tracer.startSpan("alertrouter.route", {}, remote).end();

    expect(exporter.getFinishedSpans()[0]?.spanContext().traceId).toBe(traceId);
  });

  test("refuses a second setup until the first shuts down", async () => {
    tracing = await startTracing({ env: {}, version: "", exporter });
    await expect(startTracing({ env: {}, version: "", exporter: new InMemorySpanExporter() })).rejects.toThrow(
      /already set up/,
    );

    // The refused setup left the first one working.
    const first = trace.getTracer("check").startSpan("first");
    context.with(trace.setSpan(context.active(), first), () => {
      expect(trace.getActiveSpan()).toBe(first);
    });
    first.end();
    expect(exporter.getFinishedSpans().map((s) => s.name)).toEqual(["first"]);

    await tracing.shutdown();
    exporter = new InMemorySpanExporter();
    tracing = await startTracing({ env: {}, version: "", exporter });
    tracing.tracer.startSpan("again").end();
    expect(exporter.getFinishedSpans().map((s) => s.name)).toEqual(["again"]);
  });

  test("creates an exporter from the environment without sending anything until a span ends", async () => {
    tracing = await startTracing({ env: { OTEL_EXPORTER_OTLP_ENDPOINT: "http://127.0.0.1:1" }, version: "" });
    expect(tracing.tracer.startSpan("unsent").isRecording()).toBe(true);
  });
});

describe("withSpan", () => {
  // A fresh exporter per test: shutting tracing down stops its exporter.
  let tracing: Tracing | undefined;
  let exporter: InMemorySpanExporter;

  beforeEach(() => {
    exporter = new InMemorySpanExporter();
  });
  afterEach(async () => {
    await tracing?.shutdown();
    tracing = undefined;
  });

  // Runs fn in a span and returns the id of the span that was active in it.
  async function inSpan(fn: () => unknown): Promise<string | undefined> {
    tracing = await startTracing({ env: {}, version: "", exporter });
    let active: string | undefined;
    await withSpan(tracing.tracer, "alertrouter.route", {}, () => {
      active = trace.getActiveSpan()?.spanContext().spanId;
      return fn();
    });
    return active;
  }

  test.each([
    ["returns", () => 42],
    ["resolves", async () => 42],
  ])("ends the span when fn %s", async (_name, fn) => {
    const active = await inSpan(fn);
    const [span] = exporter.getFinishedSpans();
    expect(span?.spanContext().spanId).toBe(active ?? "");
    expect(span?.events).toEqual([]);
    expect(span?.status.code).toBe(SpanStatusCode.UNSET);
  });

  test.each([
    [
      "throws",
      () => {
        throw new Error("boom");
      },
    ],
    [
      "rejects",
      async () => {
        throw new Error("boom");
      },
    ],
  ])("ends the span and records the exception when fn %s, leaving the status to the caller", async (_name, fn) => {
    await expect(inSpan(fn)).rejects.toThrow("boom");
    const [span] = exporter.getFinishedSpans();
    expect(span?.events.map((e) => e.name)).toEqual(["exception"]);
    expect(span?.status.code).toBe(SpanStatusCode.UNSET);
  });
});
