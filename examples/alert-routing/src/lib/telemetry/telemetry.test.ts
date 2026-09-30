import { describe, expect, test } from "bun:test";
import { trace } from "@opentelemetry/api";
import { InMemorySpanExporter } from "@opentelemetry/sdk-trace-base";
import { setupTelemetry } from "./telemetry";

describe("setupTelemetry", () => {
  test("sets up, reports and shuts down, and can be set up again afterwards", async () => {
    const spans: string[] = [];
    const lines: string[] = [];
    const env = { OTEL_TRACES_EXPORTER: "none" };
    const destination = { write: (line: string) => lines.push(line) };

    for (const round of [1, 2]) {
      const telemetry = await setupTelemetry({
        env,
        version: "test",
        logFormat: "json",
        debug: false,
        logDestination: destination,
        spanExporter: exporter(spans),
      });
      telemetry.tracer.startActiveSpan("alertrouter.route", (span) => {
        telemetry.logger.info("alert routed", { round });
        span.end();
      });
      telemetry.metrics.observeReceived("firing");
      expect((await telemetry.metrics.render()).body).toContain('alertrouter_alerts_received_total{status="firing"} 1');
      await telemetry.shutdown();
    }

    expect(spans).toEqual(["alertrouter.route", "alertrouter.route"]);
    expect(lines).toHaveLength(2);
    expect(lines.every((l) => l.includes('"trace_id":'))).toBe(true);
    // Shut down, the process has no tracer provider of alertrouter's left.
    expect(trace.getTracer("x").startSpan("after").isRecording()).toBe(false);
  });

  test.each([
    ["an unknown log format", {}, "xml", /unknown log format xml/],
    [
      "an invalid Pyroscope address",
      { PYROSCOPE_SERVER_ADDRESS: "pyroscope:4040" },
      "json",
      /PYROSCOPE_SERVER_ADDRESS/,
    ],
  ])("rejects %s and leaves nothing installed", async (_name, env, logFormat, error) => {
    await expect(
      setupTelemetry({
        env,
        version: "",
        logFormat: logFormat as "json",
        debug: false,
        logDestination: { write() {} },
      }),
    ).rejects.toThrow(error);

    // Nothing is left installed: a setup afterwards succeeds.
    const telemetry = await setupTelemetry({
      env: {},
      version: "",
      logFormat: "json",
      debug: false,
      logDestination: { write() {} },
    });
    await telemetry.shutdown();
  });
});

/** An exporter that keeps the names of the spans it exports, even after it is shut down. */
function exporter(names: string[]): InMemorySpanExporter {
  const e = new InMemorySpanExporter();
  const exportSpans = e.export.bind(e);
  e.export = (spans, done) => {
    names.push(...spans.map((s) => s.name));
    exportSpans(spans, done);
  };
  return e;
}
