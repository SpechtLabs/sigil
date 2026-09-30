// The composition root: every long-lived part of alertrouter, built once
// and wired explicitly. It touches no process globals (no environment, no
// signals, no exit), so tests build as many services as they like; boot.ts
// does the process wiring around it.

import { SpanStatusCode } from "@opentelemetry/api";
import type { Sigil, SourceFile } from "@spechtlabs/sigil";

import { type Clock, wallClock } from "@/lib/clock";
import type { Config } from "@/lib/config/config";
import { Dispatcher } from "@/lib/dispatch/dispatcher";
import { History } from "@/lib/dispatch/history";
import { LogNotifier, type Notifier } from "@/lib/dispatch/notifier";
import { goDurationString, msToNs } from "@/lib/duration";
import { PlatformEngine } from "@/lib/engine/platform";
import { EvaluatorPool, spawnWorker, type WorkerLike } from "@/lib/engine/pool";
import { errorResponse, type HumaneError, humane } from "@/lib/errors";
import { Api } from "@/lib/http/api";
import { Handlers } from "@/lib/http/handlers";
import { AlertRouter } from "@/lib/http/route";
import { EventHub } from "@/lib/http/sse";
import { formatTime } from "@/lib/http/time";
import { DEFAULT_CHANNEL } from "@/lib/routing/kind";
import { loadSigil } from "@/lib/sigil";
import type { BundleSource } from "@/lib/store/bundle";
import { PolicyStore, type Trigger } from "@/lib/store/store";
import type { TeamDirectory } from "@/lib/teams/directory";
import type { Telemetry } from "@/lib/telemetry/types";

export interface ServiceDeps {
  config: Config;
  telemetry: Telemetry;
  /** The compiled sigil.wasm (loadSigilModule); every engine instantiates it. */
  wasm: WebAssembly.Module;
  teams: TeamDirectory;
  /** Where the team bundle comes from: ALERTROUTER_POLICIES or the embedded bundle. */
  bundle: BundleSource;
  /** The platform's trusted documents, bundled with the app. */
  platform: readonly SourceFile[];
  clock?: Clock;
  /** Delivers notifications; by default each is logged. */
  notifier?: Notifier;
  /** Loads the platform engine's instance; the tests pass one they can break. */
  loadPlatformSigil?: () => Promise<Sigil>;
  /** Starts one evaluation worker; by default a worker_threads Worker on the package's entry. */
  spawnWorker?: () => WorkerLike;
  /** Called when the platform engine can't be replaced; boot exits the process. */
  onFatal?: (err: HumaneError) => void;
}

export interface Service {
  readonly config: Config;
  readonly telemetry: Telemetry;
  readonly teams: TeamDirectory;
  readonly store: PolicyStore;
  readonly platform: PlatformEngine;
  readonly pool: EvaluatorPool;
  readonly dispatcher: Dispatcher;
  readonly history: History;
  readonly events: EventHub;
  readonly api: Api;
  /** Starts the engines and loads the bundle; throws when the bundle doesn't load. */
  start(): Promise<void>;
  /** Starts polling the policies directory at the configured interval, until shutdown. */
  watch(): void;
  /** Loads the bundle now; throws when it doesn't load, and the previous bundle keeps serving. */
  reload(trigger: Trigger): Promise<void>;
  /** Whether the service takes traffic: a bundle is loaded, the platform engine runs, and shutdown hasn't begun. */
  ready(): boolean;
  /**
   * Stops taking requests, waits up to the shutdown timeout for the ones in
   * flight, and releases every compiled policy and engine. Returns the error
   * when the wait ran out. Telemetry is left for the caller to flush last.
   */
  shutdown(): Promise<HumaneError | undefined>;
}

export function createService(deps: ServiceDeps): Service {
  const { config, telemetry, teams } = deps;
  const clock = deps.clock ?? wallClock;
  const shutdown = new AbortController();
  const history = new History(config.historySize);

  const platform = new PlatformEngine({
    platform: deps.platform,
    telemetry,
    timeoutMs: config.evaluationTimeoutMs,
    load:
      deps.loadPlatformSigil ??
      (() =>
        loadSigil(
          (line) => telemetry.logger.error("the platform engine wrote to standard error", { line }),
          deps.wasm,
        )),
    onFatal:
      deps.onFatal ?? ((err) => telemetry.logger.error("the platform engine is gone for good", { error: err.message })),
  });

  const pool = new EvaluatorPool({
    module: deps.wasm,
    size: config.workers,
    spawn: deps.spawnWorker ?? spawnWorker,
    telemetry,
    clock,
  });

  const store = new PolicyStore({
    pool,
    telemetry,
    clock,
    teams: teams.names(),
    bundle: deps.bundle,
    platform: deps.platform,
    onLoad: (e) =>
      history.reload({
        at: formatTime(e.at),
        trigger: e.trigger,
        ok: e.ok,
        source: deps.bundle.source,
        ...(e.fingerprint === undefined ? {} : { fingerprint: e.fingerprint }),
        ...(e.error === undefined ? {} : { error: errorResponse(e.error) ?? { message: e.error.message } }),
      }),
  });

  const dispatcher = new Dispatcher({
    notifier: deps.notifier ?? new LogNotifier(telemetry.logger),
    metrics: telemetry.metrics,
    logger: telemetry.logger,
    clock,
    dedupTtlMs: config.dedupTtlMs,
    dedupMaxEntries: config.dedupMaxEntries,
    concurrency: config.dispatchConcurrency,
    isKnownDestination: (dest) => dest === DEFAULT_CHANNEL || teams.isKnownDestination(dest),
  });

  const router = new AlertRouter({
    store,
    teams,
    pool,
    platform,
    dispatcher,
    history,
    telemetry,
    clock,
    evaluationTimeoutMs: config.evaluationTimeoutMs,
  });

  const events = new EventHub({
    history,
    metrics: telemetry.metrics,
    maxStreams: config.maxEventStreams,
    shutdown: shutdown.signal,
  });

  const handlers = new Handlers({
    store,
    teams,
    router,
    history,
    telemetry,
    clock,
    batchTimeoutMs: config.batchTimeoutMs,
    platform,
    events,
    shutdown: shutdown.signal,
  });

  const api = new Api({ handlers, telemetry, platform: deps.platform, draining: () => shutdown.signal.aborted });

  let stopWatch: () => void = () => {};

  return {
    config,
    telemetry,
    teams,
    store,
    platform,
    pool,
    dispatcher,
    history,
    events,
    api,
    async start() {
      await platform.start();
      await pool.start();
      await store.initialLoad();
    },
    watch() {
      stopWatch();
      stopWatch = store.watch(config.reloadIntervalMs);
    },
    reload: (trigger) => store.load(trigger),
    ready: () => store.snapshot() !== undefined && !shutdown.signal.aborted && platform.probe(),
    async shutdown() {
      if (shutdown.signal.aborted) return undefined;
      shutdown.abort();
      stopWatch();
      const span = telemetry.tracer.startSpan("server.shutdown", {
        attributes: { "alertrouter.shutdown.timeout": goDurationString(msToNs(config.shutdownTimeoutMs)) },
      });
      let timer: ReturnType<typeof setTimeout> | undefined;
      const timedOut = await Promise.race([
        api.idle().then(() => false),
        new Promise<boolean>((resolve) => {
          timer = setTimeout(() => resolve(true), config.shutdownTimeoutMs);
        }),
      ]);
      clearTimeout(timer);
      let err: HumaneError | undefined;
      if (timedOut) {
        err = humane(
          `in-flight requests didn't finish within ${goDurationString(msToNs(config.shutdownTimeoutMs))}`,
          "raise ALERTROUTER_SHUTDOWN_TIMEOUT if requests legitimately take longer",
        );
        span.recordException(err);
        span.setStatus({ code: SpanStatusCode.ERROR, message: "graceful shutdown timed out" });
      } else {
        span.setStatus({ code: SpanStatusCode.OK });
      }
      span.end();
      store.close();
      pool.close();
      platform.close();
      return err;
    },
  };
}
