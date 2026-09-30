import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { context, SpanStatusCode, trace } from "@opentelemetry/api";
import { InMemorySpanExporter } from "@opentelemetry/sdk-trace-base";
import { createLogger, type ProcessLogger } from "./logger";
import { startTracing, type Tracing } from "./tracing";
import type { LogLevel } from "./types";

/** A pino destination that keeps every line, decoded and raw. */
function captureLines(): { write(line: string): void; lines: Record<string, unknown>[]; raw: string[] } {
  const raw: string[] = [];
  const lines: Record<string, unknown>[] = [];
  return {
    raw,
    lines,
    write(line: string) {
      raw.push(line);
      lines.push(JSON.parse(line) as Record<string, unknown>);
    },
  };
}

/** How often key appears in a raw JSON line, which JSON.parse would hide. */
function occurrences(raw: string, key: string): number {
  return raw.split(`"${key}":`).length - 1;
}

let tracing: Tracing;
const exporter = new InMemorySpanExporter();

beforeAll(async () => {
  tracing = await startTracing({ env: {}, version: "", exporter });
});
afterAll(async () => {
  await tracing.shutdown();
});
beforeEach(() => exporter.reset());

describe("createLogger", () => {
  test("writes zap's JSON shape: a level name, an RFC 3339 time and msg, without pid or hostname", async () => {
    const out = captureLines();
    const logger = await createLogger({ format: "json", debug: false, destination: out });
    logger.info("alert routed", { team: "checkout" });

    const [line] = out.lines;
    expect(line).toMatchObject({ level: "info", msg: "alert routed", team: "checkout" });
    expect(Date.parse(String(line?.time))).not.toBeNaN();
    expect(String(line?.time)).toMatch(/^\d{4}-\d\d-\d\dT/);
    expect(line).not.toContainKey("pid");
    expect(line).not.toContainKey("hostname");
  });

  test.each([
    [false, ["info", "warn", "error"]],
    [true, ["debug", "info", "warn", "error"]],
  ])("with debug %p it writes %p", async (debug, want) => {
    const out = captureLines();
    const logger = await createLogger({ format: "json", debug, destination: out });
    for (const level of ["debug", "info", "warn", "error"] as const) logger.log(level, level);
    expect(out.lines.map((l) => l.level)).toEqual(want);
  });

  test("builds a console logger", async () => {
    const logger = await createLogger({ format: "console", debug: false });
    expect(typeof logger.flush).toBe("function");
  });

  test("rejects an unknown format, naming the setting", async () => {
    await expect(createLogger({ format: "xml" as "json", debug: false })).rejects.toThrow(/unknown log format xml/);
  });

  test("writes an Error field as its message", async () => {
    const out = captureLines();
    const logger = await createLogger({ format: "json", debug: false, destination: out });
    logger.info("reload failed", { error: new Error("checkout.alerts doesn't compile") });
    expect(out.lines[0]?.error).toBe("checkout.alerts doesn't compile");
  });
});

// A line logged in a span carries its trace and span ids, once each; one
// logged outside any span carries neither.
describe("trace ids", () => {
  let out: ReturnType<typeof captureLines>;
  let logger: ProcessLogger;

  beforeEach(async () => {
    out = captureLines();
    logger = await createLogger({ format: "json", debug: true, destination: out });
  });

  test("a line outside any span has neither id", () => {
    logger.info("alert routed");
    expect(out.lines[0]).not.toContainKey("trace_id");
    expect(out.lines[0]).not.toContainKey("span_id");
  });

  test.each(["debug", "info", "warn", "error"] as const)(
    "a %s line in a span has both, once each",
    (level: LogLevel) => {
      const span = tracing.tracer.startSpan("alertrouter.route");
      context.with(trace.setSpan(context.active(), span), () =>
        logger.log(level, "alert routed", { status: "routed" }),
      );
      span.end();

      expect(out.lines[0]).toMatchObject({
        level,
        trace_id: span.spanContext().traceId,
        span_id: span.spanContext().spanId,
        status: "routed",
      });
      for (const key of ["trace_id", "span_id", "status"]) expect(occurrences(out.raw[0] ?? "", key)).toBe(1);
    },
  );

  test("ids a caller passes win over the active span's, still once each", () => {
    const span = tracing.tracer.startSpan("alertrouter.route");
    context.with(trace.setSpan(context.active(), span), () =>
      logger.info("alert routed", { trace_id: "a".repeat(32), span_id: "b".repeat(16) }),
    );
    span.end();

    expect(out.lines[0]).toMatchObject({ trace_id: "a".repeat(32), span_id: "b".repeat(16) });
    expect(occurrences(out.raw[0] ?? "", "trace_id")).toBe(1);
  });
});

// Like otelzap: warn and error lines are recorded on the active span, and an
// error line marks it failed.
describe("span annotation", () => {
  test.each([
    ["debug", [], SpanStatusCode.UNSET],
    ["info", [], SpanStatusCode.UNSET],
    ["warn", ["alert routed"], SpanStatusCode.UNSET],
    ["error", ["alert routed", "exception"], SpanStatusCode.ERROR],
  ] as const)("a %s line adds events %p and leaves status %p", async (level, events, status) => {
    const logger = await createLogger({ format: "json", debug: true, destination: captureLines() });
    const span = tracing.tracer.startSpan("alertrouter.route");
    context.with(trace.setSpan(context.active(), span), () =>
      logger.log(level, "alert routed", { status: "invalid", count: 2, nested: { no: true } }),
    );
    span.end();

    const [done] = exporter.getFinishedSpans();
    expect(done?.events.map((e) => e.name)).toEqual([...events]);
    expect(done?.status.code).toBe(status);
    if (events.length > 0) {
      expect(done?.events[0]?.attributes).toEqual({ "log.severity": level, status: "invalid", count: 2 });
    }
  });
});
