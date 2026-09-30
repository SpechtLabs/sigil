// The process logger: pino, writing the same JSON lines the Go service's zap
// logger writes ({"level":"info","time":"…","msg":"…",…}), each carrying the
// trace and span ids of the active span, so a line in Loki leads straight to
// its trace. Like otelzap in the Go service, a warn or error line is also
// recorded on the active span as an event, and an error line marks the span
// failed.

import { isSpanContextValid, SpanStatusCode, trace } from "@opentelemetry/api";
import pino, { type DestinationStream } from "pino";
import { humane } from "../errors";
import type { LogFields, Logger, LogLevel } from "./types";

/** The log formats the service accepts (ALERTROUTER_LOG_FORMAT). */
export const LOG_FORMATS = ["json", "console"] as const;
export type LogFormat = (typeof LOG_FORMATS)[number];

export interface LoggerOptions {
  format: LogFormat;
  /** Lowers the level to debug. */
  debug: boolean;
  /**
   * Where the lines go instead of standard output, such as a test's capture
   * stream. The console format ignores it and always pretty-prints to stdout.
   */
  destination?: DestinationStream;
}

/** A {@link Logger} and a way to flush what it buffered. */
export interface ProcessLogger extends Logger {
  flush(): Promise<void>;
}

/**
 * Builds the process logger. It throws for a format other than json or
 * console, with the setting to change.
 */
export async function createLogger(opts: LoggerOptions): Promise<ProcessLogger> {
  const base: pino.LoggerOptions = {
    level: opts.debug ? "debug" : "info",
    // No pid or hostname: zap's production logger doesn't write them, and
    // the container runtime knows both.
    base: undefined,
    timestamp: pino.stdTimeFunctions.isoTime,
    formatters: { level: (label) => ({ level: label }) },
    mixin: traceFields,
  };

  switch (opts.format) {
    case "json":
      return new PinoLogger(opts.destination ? pino(base, opts.destination) : pino(base));
    case "console": {
      // Loaded here, so a JSON logger never loads the pretty printer.
      const { default: pretty } = await import("pino-pretty");
      return new PinoLogger(pino(base, pretty({ sync: true, translateTime: "SYS:HH:MM:ss.l" })));
    }
    default:
      throw humane(
        `unknown log format ${String(opts.format)}`,
        `set ALERTROUTER_LOG_FORMAT to ${LOG_FORMATS.join(" or ")}`,
      );
  }
}

/**
 * The trace_id and span_id of the active span, for every line pino writes.
 * They come from here and nowhere else: pino merges a line's own fields over
 * these, so a line carries each id exactly once, and child loggers never bind
 * them.
 */
export function traceFields(): Record<string, string> {
  const sc = trace.getActiveSpan()?.spanContext();
  if (!sc || !isSpanContextValid(sc)) return {};
  return { trace_id: sc.traceId, span_id: sc.spanId };
}

class PinoLogger implements ProcessLogger {
  constructor(private readonly pino: pino.Logger) {}

  log(level: LogLevel, msg: string, fields: LogFields = {}): void {
    const line = plain(fields);
    annotate(level, msg, line);
    this.pino[level](line, msg);
  }

  debug(msg: string, fields?: LogFields): void {
    this.log("debug", msg, fields);
  }

  info(msg: string, fields?: LogFields): void {
    this.log("info", msg, fields);
  }

  warn(msg: string, fields?: LogFields): void {
    this.log("warn", msg, fields);
  }

  error(msg: string, fields?: LogFields): void {
    this.log("error", msg, fields);
  }

  flush(): Promise<void> {
    return new Promise((resolve) => this.pino.flush(() => resolve()));
  }
}

/**
 * Records a warn or error line on the active span, as otelzap does in the Go
 * service: an event named after the message with the line's fields, and for
 * an error, the span's status and an exception event.
 */
function annotate(level: LogLevel, msg: string, fields: LogFields): void {
  if (level !== "warn" && level !== "error") return;
  const span = trace.getActiveSpan();
  if (!span?.isRecording()) return;

  const attributes: Record<string, string | number | boolean> = { "log.severity": level };
  for (const [key, value] of Object.entries(fields)) {
    if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") attributes[key] = value;
  }
  span.addEvent(msg, attributes);
  if (level === "error") {
    span.setStatus({ code: SpanStatusCode.ERROR, message: msg });
    span.recordException({ name: "Error", message: msg });
  }
}

/**
 * Replaces Error values with their message. JSON.stringify writes an Error
 * as {}, and a Go error field is its message too.
 */
function plain(fields: LogFields): LogFields {
  let out: LogFields | undefined;
  for (const [key, value] of Object.entries(fields)) {
    if (value instanceof Error) {
      out ??= { ...fields };
      out[key] = value.message;
    }
  }
  return out ?? fields;
}
