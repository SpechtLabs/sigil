// Test doubles shared by the unit tests: a telemetry that records log lines
// and metrics without installing process globals, a clock the test moves,
// and team bundles written to a temporary directory.

import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { trace } from "@opentelemetry/api";
import { type Sigil, SigilStoppedError, type SourceFile } from "@spechtlabs/sigil";

import type { Clock } from "./clock";
import { TEAM_FILES } from "./embedded";
import { loadSigilModule } from "./sigil";
import { PromMetrics } from "./telemetry/metrics";
import { type LogFields, type Logger, type LogLevel, type Telemetry, TRACER_NAME } from "./telemetry/types";

export interface LogLine extends LogFields {
  level: LogLevel;
  msg: string;
}

/** A telemetry that keeps log lines in memory, with real metrics and a no-op tracer. */
export function fakeTelemetry(): {
  telemetry: Telemetry & { metrics: PromMetrics };
  logs: LogLine[];
  metricsText: () => Promise<string>;
} {
  const logs: LogLine[] = [];
  const log = (level: LogLevel, msg: string, fields: LogFields = {}) => logs.push({ level, msg, ...fields });
  const logger: Logger = {
    log,
    debug: (m, f) => log("debug", m, f),
    info: (m, f) => log("info", m, f),
    warn: (m, f) => log("warn", m, f),
    error: (m, f) => log("error", m, f),
  };
  const metrics = new PromMetrics();
  return {
    telemetry: { tracer: trace.getTracer(TRACER_NAME), metrics, logger, shutdown: async () => {} },
    logs,
    metricsText: async () => (await metrics.render()).body,
  };
}

/** A clock that stands still until the test moves it. */
export class FakeClock implements Clock {
  constructor(public t = Date.parse("2026-01-01T00:12:00Z")) {}
  now = (): number => this.t;
}

/** A temporary team bundle directory, seeded with the bundled team policies. */
export async function tempBundle(files: readonly SourceFile[] = TEAM_FILES): Promise<{
  dir: string;
  write: (path: string, source: string) => Promise<void>;
  remove: (path: string) => Promise<void>;
  cleanup: () => Promise<void>;
}> {
  const dir = await mkdtemp(join(tmpdir(), "alertrouter-policies-"));
  const write = async (path: string, source: string) => {
    await mkdir(dirname(join(dir, path)), { recursive: true });
    await writeFile(join(dir, path), source);
  };
  for (const f of files) await write(f.path, f.source);
  return {
    dir,
    write,
    remove: (path) => rm(join(dir, path), { force: true }),
    cleanup: () => rm(dir, { recursive: true, force: true }),
  };
}

/** The bundled source of a team's policy. */
export function teamSource(team: string): string {
  const f = TEAM_FILES.find((t) => t.path === `${team}/alerts.sigil`);
  if (f === undefined) throw new Error(`no bundled policy for ${team}`);
  return f.source;
}

let wasm: Promise<WebAssembly.Module> | undefined;

/** The compiled sigil.wasm, compiled once per test process. */
export function testWasm(): Promise<WebAssembly.Module> {
  wasm ??= loadSigilModule();
  return wasm;
}

/** The error a stopped Sigil module throws, for tests that simulate one. */
export function stoppedError(): SigilStoppedError {
  return new SigilStoppedError("the Sigil module stopped: stopped by the test");
}

/**
 * Marks sigil stopped, as the package does after its module traps:
 * sigil.stopped reports the error from now on. Tests use it instead of a
 * document that happens to crash the engine, which a parser limit may fix.
 */
export function stopSigil(sigil: Sigil): SigilStoppedError {
  const err = stoppedError();
  Object.defineProperty(sigil, "stopped", { get: () => err, configurable: true });
  return err;
}
