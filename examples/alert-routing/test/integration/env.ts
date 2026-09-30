// An env is one alertrouter wired up the way the composition root wires it,
// served over real HTTP by Bun.serve on a free port, with everything it
// reports captured: spans in an in-memory exporter, metrics on a registry of
// its own, JSON log lines in memory, every notification the dispatcher
// delivered, and store time from a clock the spec controls. It covers the
// same contract as the e2e suite, and what only an in-process test can see:
// exact metric values, every span attribute and event, every notification,
// a store that never loaded.

import { expect } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { Writable } from "node:stream";
import type { Attributes } from "@opentelemetry/api";
import { InMemorySpanExporter, type ReadableSpan } from "@opentelemetry/sdk-trace-base";
import type { Sigil } from "@spechtlabs/sigil";
import type { Clock } from "@/lib/clock";
import { type Config, DEFAULTS } from "@/lib/config/config";
import { LogNotifier, type Notification, type Notifier } from "@/lib/dispatch/notifier";
import { PLATFORM_FILES, TEAM_FILES, TEAMS_YAML } from "@/lib/embedded";
import type { HumaneError } from "@/lib/errors";
import { directoryBundle, embeddedBundle } from "@/lib/store/bundle";
import { DEFAULT_SOURCE, TeamDirectory } from "@/lib/teams/directory";
import { PromMetrics } from "@/lib/telemetry/metrics";
import { type ServiceTelemetry, setupTelemetry } from "@/lib/telemetry/telemetry";
import type { Telemetry } from "@/lib/telemetry/types";
import { testWasm } from "@/lib/testing";
import { createService, type Service } from "@/server/service";
import { Client } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { type Families, parseMetrics } from "../fixture/metrics";
import { EXAMPLES_DIR } from "../fixture/requests";

/** The span names the service promises. Tempo searches and the Grafana trace panels depend on them. */
export const SPAN_ROUTE = "alertrouter.route";
export const SPAN_LOAD = "alertrouter.policy.load";
export const EVENT_CANDIDATE = "sigil.candidate";
/** The load span's attribute that says what started it. */
export const TRIGGER_KEY = "alertrouter.reload.trigger";

/** The route endpoint's route template, the route label of its request metrics and its server span's name. */
export const ROUTE_TEMPLATE = "/api/v1/teams/:team/route";

/** checkout's policy, relative to the team directory. */
export const CHECKOUT_POLICY = "checkout/alerts.sigil";
/** The line of checkout's policy the failure specs add their rules after. */
export const CHECKOUT_RULES = `routing(muted: ["CheckoutCanaryLatency"])`;

/**
 * Posts a production warning to a second channel for the same reason the
 * platform posts it to the team's, and the kind can't pick one: a conflict,
 * the policy's defect.
 */
export const conflictingRule = `${CHECKOUT_RULES}\n\nwhen alert.severity == warning {\n  notify(reason: routine, channel: "#checkout-oncall")\n}\n`;

/** Reads past the end of a list for every alert, a runtime error. */
export const failingRule = `${CHECKOUT_RULES}\n\nlet names = ["only"]\n\nwhen names[1] == alert.name {\n  drop(reason: muted)\n}\n`;

/**
 * Asserts that every alert names its service, which an alert without a
 * service label fails before any rule runs: the alert's fault, an input
 * assert.
 */
export const assertingRule = `${CHECKOUT_RULES}\n\nassert("names_service", alert.labels["service"] != "")\n`;

/** The evaluation timeout of an env whose spec isn't about timeouts. */
const GENEROUS_EVALUATION_TIMEOUT_MS = 1_000;

/** The team policies the checkout ships. */
const TEAMS_DIR = join(EXAMPLES_DIR, "policies", "teams");

/**
 * When the envs with a fixed clock load their bundle. A fixed time makes
 * loaded_at and the last-reload gauge exact.
 */
export const CLOCK_START = new Date("2026-09-28T12:00:00Z");

/** A clock that stands still at one time, so a spec says exactly how long an alert has fired. */
export class FixedClock implements Clock {
  readonly #at: number;

  constructor(at: Date) {
    this.#at = at.getTime();
  }

  now(): number {
    return this.#at;
  }
}

/** One JSON line the service logged. */
export interface LogLine {
  raw: string;
  level: string;
  msg: string;
  fields: Record<string, unknown>;
}

export interface EnvOptions {
  /**
   * Serves policies/teams itself instead of a private copy, for the spec that
   * checks the built-in bundle against the directory it was built from. Every
   * other env serves a temporary directory holding the built-in team files,
   * so a spec can edit and break it, and an e2e run editing policies/teams
   * at the same time can't reach into it.
   */
  sharedTeamsDir?: boolean;
  /** Serves the team bundle built into the app, as with ALERTROUTER_POLICIES unset. */
  embedded?: boolean;
  /** Builds the env without loading the bundle, the state before the first load succeeds. */
  unloaded?: boolean;
  /**
   * Bounds each evaluation. It defaults to a generous second instead of the
   * service's 50ms, so a busy machine can't turn a spec's decision into a
   * timeout; the specs about timeouts set their own.
   */
  evaluationTimeoutMs?: number;
  /**
   * Dispatches through the service's own LogNotifier, so a spec can read
   * the "notification dispatched" lines; the recorder stays empty.
   */
  logNotifier?: boolean;
  /** Dispatches through this notifier instead of the recorder, such as one that fails. */
  notifier?: Notifier;
  /**
   * The service's clock: when a bundle loaded, how long a webhook alert has
   * fired, dedup expiry and batch deadlines. The wall clock when unset.
   */
  clock?: Clock;
  /** How long a delivered notification is remembered; the service's default when unset. */
  dedupTtlMs?: number;
  /** How long one webhook may take to route its alerts; the service's default when unset. */
  batchTimeoutMs?: number;
  /**
   * Loads each instance of the platform engine instead of the service's
   * loader, so a spec can make a replacement hang or fail.
   */
  loadPlatformSigil?: () => Promise<Sigil>;
  /** Caps the dedup table instead of the service's default. */
  dedupMaxEntries?: number;
  /** Caps the open event streams instead of the service's default. */
  maxEventStreams?: number;
  /** Whether closeEnvs closes the env; false for one a file shares across its specs. */
  track?: boolean;
}

/**
 * The telemetry of the test file that runs. Tracing installs process globals
 * (the tracer provider and the context manager that carries the active span
 * into log lines), so there is one at a time: every env of a file shares it,
 * with metrics of its own, and clears the spans and lines before it starts.
 * bun runs every test file in one process, so each file releases it when it
 * ends; see {@link releaseTelemetry}.
 */
let processTelemetry:
  | Promise<{ telemetry: ServiceTelemetry; spans: InMemorySpanExporter; lines: string[] }>
  | undefined;

function sharedTelemetry() {
  processTelemetry ??= (async () => {
    const spans = new InMemorySpanExporter();
    const lines: string[] = [];
    let partial = "";
    const capture = new Writable({
      write(chunk: Buffer | string, _enc, done) {
        const parts = (partial + chunk.toString()).split("\n");
        partial = parts.pop() ?? "";
        lines.push(...parts.filter((l) => l !== ""));
        done();
      },
    });
    const telemetry = await setupTelemetry({
      env: {},
      version: "test",
      logFormat: "json",
      debug: true,
      logDestination: capture,
      spanExporter: spans,
    });
    return { telemetry, spans, lines };
  })();
  return processTelemetry;
}

const open: Env[] = [];

/** Closes every env the finished spec built. Register it with afterEach. */
export async function closeEnvs(): Promise<void> {
  for (let e = open.pop(); e !== undefined; e = open.pop()) await e.close();
}

/**
 * Closes every env and shuts the file's telemetry down, so the next test
 * file, or another suite's setupTelemetry, can install its own. Register it
 * with afterAll in every file that builds envs.
 */
export async function releaseTelemetry(): Promise<void> {
  await closeEnvs();
  const shared = processTelemetry;
  processTelemetry = undefined;
  if (shared !== undefined) await (await shared).telemetry.shutdown();
}

/** Builds an env; see {@link EnvOptions}. */
export async function newEnv(opts: EnvOptions = {}): Promise<Env> {
  const e = await Env.start(opts);
  if (opts.track !== false) open.push(e);
  return e;
}

export class Env {
  /** A client of the env's server. */
  readonly client: Client;
  /** The team policies directory the store reads. */
  readonly dir: string;
  readonly service: Service;
  /** Every error the platform engine gave up with, what boot would exit on. */
  readonly fatal: HumaneError[];
  readonly #metrics: PromMetrics;
  readonly #spans: InMemorySpanExporter;
  readonly #lines: string[];
  readonly #recorded: Notification[];
  readonly #server: ReturnType<typeof Bun.serve>;
  readonly #tmp: string | undefined;

  private constructor(fields: {
    service: Service;
    dir: string;
    tmp: string | undefined;
    metrics: PromMetrics;
    spans: InMemorySpanExporter;
    lines: string[];
    recorded: Notification[];
    fatal: HumaneError[];
    server: ReturnType<typeof Bun.serve>;
  }) {
    this.fatal = fields.fatal;
    this.service = fields.service;
    this.dir = fields.dir;
    this.#tmp = fields.tmp;
    this.#metrics = fields.metrics;
    this.#spans = fields.spans;
    this.#lines = fields.lines;
    this.#recorded = fields.recorded;
    this.#server = fields.server;
    this.client = new Client(`http://127.0.0.1:${fields.server.port}`);
  }

  static async start(opts: EnvOptions): Promise<Env> {
    const shared = await sharedTelemetry();
    shared.spans.reset();
    shared.lines.length = 0;

    let dir = TEAMS_DIR;
    let tmp: string | undefined;
    if (!opts.sharedTeamsDir) {
      tmp = mkdtempSync(join(tmpdir(), "alertrouter-teams-"));
      dir = join(tmp, "teams");
      for (const f of TEAM_FILES) {
        mkdirSync(dirname(join(dir, f.path)), { recursive: true });
        writeFileSync(join(dir, f.path), f.source);
      }
    }

    const metrics = new PromMetrics();
    const telemetry: Telemetry = {
      tracer: shared.telemetry.tracer,
      logger: shared.telemetry.logger,
      metrics,
      shutdown: async () => {},
    };

    const recorded: Notification[] = [];
    const fatal: HumaneError[] = [];
    const recorder: Notifier = {
      notify: async (n) => {
        recorded.push({ ...n });
      },
    };
    const notifier = opts.notifier ?? (opts.logNotifier ? new LogNotifier(shared.telemetry.logger) : recorder);

    const config: Config = {
      ...DEFAULTS,
      policiesDir: dir,
      teamsFile: "",
      debug: true,
      // The specs drive polls themselves; see tick.
      reloadIntervalMs: 0,
      shutdownTimeoutMs: 2_000,
      evaluationTimeoutMs: opts.evaluationTimeoutMs ?? GENEROUS_EVALUATION_TIMEOUT_MS,
      dedupTtlMs: opts.dedupTtlMs ?? DEFAULTS.dedupTtlMs,
      batchTimeoutMs: opts.batchTimeoutMs ?? DEFAULTS.batchTimeoutMs,
      dedupMaxEntries: opts.dedupMaxEntries ?? DEFAULTS.dedupMaxEntries,
      maxEventStreams: opts.maxEventStreams ?? DEFAULTS.maxEventStreams,
    };
    const service = createService({
      config,
      telemetry,
      wasm: await testWasm(),
      teams: TeamDirectory.parse(TEAMS_YAML, DEFAULT_SOURCE),
      bundle: opts.embedded ? embeddedBundle(TEAM_FILES) : directoryBundle(dir),
      platform: PLATFORM_FILES,
      clock: opts.clock,
      notifier,
      loadPlatformSigil: opts.loadPlatformSigil,
      onFatal: (err) => fatal.push(err),
    });
    if (opts.unloaded) {
      // Where a pod is when its first load failed: the engines run, no bundle serves.
      await service.platform.start();
      await service.pool.start();
    } else {
      await service.start();
    }

    const server = Bun.serve({ port: 0, hostname: "127.0.0.1", fetch: (req) => service.api.fetch(req) });
    return new Env({ service, dir, tmp, metrics, spans: shared.spans, lines: shared.lines, recorded, fatal, server });
  }

  /** The first load, as the composition root makes it before serving. */
  initialLoad(): Promise<void> {
    return this.service.store.initialLoad();
  }

  /** The env's registry, which holds everything its /metrics serves. */
  async families(): Promise<Families> {
    return parseMetrics((await this.#metrics.render()).body);
  }

  /** Every notification the recorder got, oldest first. */
  notifications(): Notification[] {
    return [...this.#recorded];
  }

  /** Every JSON line logged since the env started, decoded. */
  logs(): LogLine[] {
    return this.#lines.map((raw) => {
      const fields = JSON.parse(raw) as Record<string, unknown>;
      return { raw, level: String(fields.level), msg: String(fields.msg), fields };
    });
  }

  /** Every finished span, oldest first. */
  spans(): ReadableSpan[] {
    return this.#spans.getFinishedSpans();
  }

  /** Drops the spans finished so far, such as the startup load's. */
  resetSpans(): void {
    this.#spans.reset();
  }

  /** The finished spans called name, oldest first. */
  spansNamed(name: string): ReadableSpan[] {
    return this.spans().filter((s) => s.name === name);
  }

  /**
   * Waits until exactly one span called name has finished and returns it.
   * The handler answers before its span ends, and the HTTP span ends after
   * that, so a client can hold the answer a moment before the exporter
   * holds the spans.
   */
  waitForSpan(name: string): Promise<ReadableSpan> {
    return this.#waitFor((s) => s.name === name, name);
  }

  /** Waits until exactly one HTTP server span has finished and returns it. */
  serverSpan(): Promise<ReadableSpan> {
    return this.#waitFor((s) => s.kind === SPAN_KIND_SERVER, "the HTTP server span");
  }

  /** The AlertRouting bundle the env's listing reports. */
  served() {
    return this.client.served();
  }

  /** The compiled root policies that serve right now, with their snapshot. */
  snapshot() {
    return this.service.store.snapshot();
  }

  /** Replaces a file in the env's team directory, creating it when it doesn't exist. */
  writeTeamFile(rel: string, content: string): void {
    const path = join(this.dir, rel);
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, content);
  }

  /** Replaces the one occurrence of from in the env's copy of checkout's policy with to. */
  editCheckout(from: string, to: string): void {
    const path = join(this.dir, CHECKOUT_POLICY);
    const data = readFileSync(path, "utf8");
    expect(data.split(from).length - 1, `${path} no longer holds ${from} once; update the spec`).toBe(1);
    writeFileSync(path, data.replace(from, to));
  }

  /** Reloads the env's bundle through the API and expects it to load. */
  async reloadOK(): Promise<void> {
    const a = await this.client.reload();
    expect({ status: a.status, body: a.status === 200 ? "" : a.body }).toEqual({ status: 200, body: "" });
  }

  /**
   * Posts v as JSON to path and gives up after waitMs, the way a client that
   * stops waiting closes its connection mid-request, from a worker so the
   * abort fires while this thread is busy evaluating. It expects the server
   * not to have answered by then.
   */
  async abandon(path: string, v: unknown, waitMs: number): Promise<void> {
    const worker = new Worker(new URL("./abandon.worker.ts", import.meta.url).href);
    try {
      const outcome = await new Promise<{ answered?: number; abandoned?: string }>((resolve, reject) => {
        worker.onmessage = (event: MessageEvent<{ answered?: number; abandoned?: string }>) => resolve(event.data);
        worker.onerror = (event) => reject(new Error(event.message));
        worker.postMessage({ url: this.client.baseURL + path, body: JSON.stringify(v), waitMs });
      });
      expect(outcome, `POST ${path} answered within ${waitMs}ms`).toEqual({ abandoned: "TimeoutError" });
    } finally {
      worker.terminate();
    }
  }

  /**
   * Runs one poll, as the watch loop's interval would, and waits for it to
   * finish. The loop itself is a setInterval the store's own tests cover.
   */
  tick(): Promise<void> {
    return this.service.store.reloadIfChanged();
  }

  /** Reloads the way the SIGHUP handler does, and waits for it. */
  sighup(): Promise<void> {
    return this.service.reload("sighup");
  }

  async close(): Promise<void> {
    this.#server.stop(true);
    await this.service.shutdown();
    if (this.#tmp !== undefined) rmSync(this.#tmp, { recursive: true, force: true });
  }

  async #waitFor(match: (s: ReadableSpan) => boolean, what: string): Promise<ReadableSpan> {
    return eventually(() => {
      const spans = this.spans().filter(match);
      expect(spans.length, `finished spans matching ${what}`).toBe(1);
      return spans[0] as ReadableSpan;
    });
  }
}

/** SpanKind.SERVER. */
const SPAN_KIND_SERVER = 1;

/** Span or event attributes as a plain record. */
export function attrs(a: Attributes | undefined): Record<string, unknown> {
  return { ...(a ?? {}) };
}

/** The attributes of span's events called name, in order. */
export function events(span: ReadableSpan, name: string): Record<string, unknown>[] {
  return span.events.filter((ev) => ev.name === name).map((ev) => attrs(ev.attributes));
}
