import { afterEach, expect, test } from "bun:test";
import { createTestTelemetry, type TestTelemetry } from "./testing";

let t: TestTelemetry | undefined;

afterEach(async () => {
  await t?.shutdown();
  t = undefined;
});

test("reads back spans, log lines and metrics, and keeps the spans after shutdown", async () => {
  t = await createTestTelemetry({ env: { OTEL_EXPORTER_OTLP_ENDPOINT: "http://127.0.0.1:1" } });
  const { telemetry } = t;
  telemetry.tracer.startActiveSpan("alertrouter.route", (span) => {
    telemetry.logger.debug("evaluating");
    telemetry.logger.info("alert routed", { team: "checkout" });
    span.end();
  });
  telemetry.metrics.observeReceived("firing");

  expect(t.spansNamed("alertrouter.route")).toHaveLength(1);
  expect(t.logs().map((l) => l.msg)).toEqual(["evaluating", "alert routed"]);
  expect(t.logsWith("alert routed")[0]).toMatchObject({
    team: "checkout",
    trace_id: t.spans()[0]?.spanContext().traceId,
  });
  expect(await t.metricsText()).toContain('alertrouter_alerts_received_total{status="firing"} 1');

  await t.shutdown();
  expect(t.spans()).toHaveLength(1);
  t = undefined;
});

test("reset forgets spans and lines but not metrics", async () => {
  t = await createTestTelemetry({ debug: false });
  t.telemetry.logger.debug("hidden");
  t.telemetry.logger.info("seen");
  t.telemetry.metrics.observeReceived("resolved");
  expect(t.logs()).toHaveLength(1);

  t.reset();
  expect(t.logs()).toEqual([]);
  expect(await t.metricsText()).toContain('alertrouter_alerts_received_total{status="resolved"} 1');
});
