// The policy store: one compiled root policy per team, <team>.alerts, from
// the team bundle, with platform.paging required from the platform's own
// documents. A load compiles every root or none: a bundle with one broken
// team policy is rejected as a whole, and the bundle that served before
// keeps serving (last known good). Loads happen at startup, on the reload
// endpoint, on SIGHUP and when polling finds the directory changed.
//
// Team policies compile in the evaluation pool's workers, never in the
// platform engine, and live inside their modules until released. A snapshot
// that's been replaced is released as soon as the last request that took it
// is done with it, so reloads don't leak module memory.

import { context, type Span, SpanStatusCode, trace } from "@opentelemetry/api";
import { SigilError, type SourceFile } from "@spechtlabs/sigil";

import type { Clock } from "../clock";
import type { EvaluatorPool, PooledPolicy } from "../engine/pool";
import { adviceOf, HumaneError, humane, messageOf, wrap } from "../errors";
import { AlertRouting } from "../routing/kind";
import { isEngineFailure } from "../sigil";
import type { Telemetry } from "../telemetry/types";
import type { BundleSource } from "./bundle";

/** The policy every team root must invoke, from the platform's documents. */
export const REQUIRED_POLICY = "platform.paging";

/** A team's root policy is the team's name and this suffix. */
export const ROOT_SUFFIX = ".alerts";

/** What started a load, the alertrouter.reload.trigger span attribute. */
export type Trigger = "startup" | "manual" | "sighup" | "poll";

/** One team's root policy. */
export interface Root {
  team: string;
  policy: string;
}

/** A loaded bundle: immutable, and released once replaced and no longer in use. */
export class Snapshot {
  readonly kind = AlertRouting.name;
  readonly kindVersion = AlertRouting.version;
  #refs = 0;
  #retired = false;
  #released = false;

  /** @internal Snapshots come from PolicyStore loads. */
  constructor(
    readonly source: string,
    readonly fingerprint: string,
    readonly loadedAt: Date,
    readonly roots: readonly Root[],
    readonly files: readonly SourceFile[],
    readonly policies: ReadonlyMap<string, PooledPolicy>,
  ) {}

  /** The compiled root policy of team, or undefined when the bundle has none. */
  policy(team: string): PooledPolicy | undefined {
    return this.#released ? undefined : this.policies.get(team);
  }

  /** Every root policy's name, in the team directory's order. */
  policyNames(): string[] {
    return this.roots.map((r) => r.policy);
  }

  /** Whether the snapshot's policies have been released. */
  get released(): boolean {
    return this.#released;
  }

  /** @internal Takes a reference; see PolicyStore.acquire. */
  retain(): void {
    this.#refs++;
  }

  /** @internal Drops a reference, and releases the policies once replaced and unused. */
  drop(): void {
    this.#refs--;
    this.#maybeRelease();
  }

  /** @internal The store replaced it. */
  retire(): void {
    this.#retired = true;
    this.#maybeRelease();
  }

  #maybeRelease(): void {
    if (!this.#retired || this.#refs > 0 || this.#released) return;
    this.#released = true;
    for (const p of this.policies.values()) void p.release();
  }
}

/** A snapshot taken for the length of one request. Release it when done, or `using` it. */
export interface Lease extends Disposable {
  readonly snapshot: Snapshot;
  release(): void;
}

/** The last load that was rejected, for the console. */
export interface LoadFailure {
  at: Date;
  trigger: Trigger;
  error: HumaneError;
}

export interface StoreOptions {
  /** Where team policies compile and evaluate. */
  pool: EvaluatorPool;
  telemetry: Telemetry;
  clock: Clock;
  /** The teams to compile a root for, <team>.alerts each, from the team directory. */
  teams: readonly string[];
  /** Where the team bundle comes from. */
  bundle: BundleSource;
  /** The platform's trusted documents, bundled with the app; never read from the team bundle. */
  platform: readonly SourceFile[];
  /** Called after every load attempt, for the console's event stream. */
  onLoad?: (event: { at: Date; trigger: Trigger; ok: boolean; fingerprint?: string; error?: HumaneError }) => void;
}

export class PolicyStore {
  readonly #opts: StoreOptions;
  readonly #roots: readonly Root[];
  #current: Snapshot | undefined;
  /** The fingerprint of the last bundle a load read, loaded or not, so polling doesn't retry a broken one. */
  #lastSeen: string | undefined;
  #lastFailure: LoadFailure | undefined;
  /** Loads run one at a time, in order. */
  #queue: Promise<unknown> = Promise.resolve();
  #closed = false;

  constructor(opts: StoreOptions) {
    this.#opts = opts;
    this.#roots = opts.teams.map((team) => ({ team, policy: team + ROOT_SUFFIX }));
  }

  /** Where the team bundle comes from: its directory, or "embedded". */
  get source(): string {
    return this.#opts.bundle.source;
  }

  /** The bundle that serves, or undefined before the first successful load. */
  snapshot(): Snapshot | undefined {
    return this.#current;
  }

  /**
   * Takes the bundle that serves for the length of a request, so a reload
   * during the request can't release policies the request still evaluates.
   * undefined before the first successful load.
   */
  acquire(): Lease | undefined {
    const snap = this.#current;
    if (snap === undefined) return undefined;
    snap.retain();
    let released = false;
    const release = () => {
      if (released) return;
      released = true;
      snap.drop();
    };
    return { snapshot: snap, release, [Symbol.dispose]: release };
  }

  /** The last rejected load since the bundle that serves loaded, for the console. */
  lastFailure(): LoadFailure | undefined {
    return this.#lastFailure;
  }

  /**
   * The first load. It throws when the bundle doesn't load, since serving
   * without team policies would route every alert to the default channel.
   */
  initialLoad(): Promise<void> {
    return this.load("startup");
  }

  /**
   * Loads the bundle as it is now and makes it serve. Throws a HumaneError
   * when it doesn't load; the previous bundle keeps serving.
   */
  load(trigger: Trigger): Promise<void> {
    const run = this.#queue.then(() => this.#load(trigger));
    this.#queue = run.catch(() => undefined);
    return run;
  }

  /** Loads the bundle when its fingerprint changed since the last load read it. Never throws. */
  async reloadIfChanged(): Promise<void> {
    let fingerprint: string;
    try {
      fingerprint = (await this.#opts.bundle.read()).fingerprint;
    } catch (err) {
      fingerprint = `unreadable: ${messageOf(err)}`;
    }
    if (fingerprint === this.#lastSeen) return;
    await this.load("poll").catch(() => undefined);
  }

  /**
   * Polls the bundle every intervalMs (none when 0) until the returned
   * function is called.
   */
  watch(intervalMs: number): () => void {
    if (intervalMs <= 0) return () => {};
    let running = false;
    const timer = setInterval(() => {
      if (running || this.#closed) return;
      running = true;
      void this.reloadIfChanged().finally(() => {
        running = false;
      });
    }, intervalMs);
    timer.unref?.();
    return () => clearInterval(timer);
  }

  /** Releases every compiled policy. The store serves nothing afterwards. */
  close(): void {
    this.#closed = true;
    const snap = this.#current;
    this.#current = undefined;
    snap?.retire();
  }

  async #load(trigger: Trigger): Promise<void> {
    const { telemetry } = this.#opts;
    telemetry.metrics.prepareReloads();
    const span = telemetry.tracer.startSpan("alertrouter.policy.load", {
      attributes: {
        "sigil.source": this.source,
        "sigil.kind": AlertRouting.name,
        "alertrouter.reload.trigger": trigger,
      },
    });
    const ctx = trace.setSpan(context.active(), span);
    try {
      await context.with(ctx, () => this.#loadIn(span, trigger));
    } finally {
      span.end();
    }
  }

  async #loadIn(span: Span, trigger: Trigger): Promise<void> {
    const { telemetry, clock } = this.#opts;
    if (this.#closed) {
      throw this.#fail(
        span,
        trigger,
        humane("the policy store is shut down", "nothing to do; alertrouter is stopping"),
      );
    }

    let bundle: Awaited<ReturnType<BundleSource["read"]>>;
    try {
      bundle = await this.#opts.bundle.read();
    } catch (err) {
      this.#lastSeen = `unreadable: ${messageOf(err)}`;
      throw this.#fail(span, trigger, asHumane(err));
    }
    this.#lastSeen = bundle.fingerprint;

    let snap: Snapshot;
    try {
      snap = await this.#compile(bundle.files, bundle.fingerprint, new Date(clock.now()));
    } catch (err) {
      throw this.#fail(span, trigger, asHumane(err));
    }

    const previous = this.#current;
    this.#current = snap;
    this.#lastFailure = undefined;
    previous?.retire();

    const names = snap.policyNames();
    span.setAttributes({ "sigil.policies": names, "sigil.fingerprint": snap.fingerprint });
    span.setStatus({ code: SpanStatusCode.OK });
    telemetry.metrics.observeReloadSuccess(snap.loadedAt, snap.source, snap.fingerprint, snap.roots);
    telemetry.logger.info("policy bundle loaded", {
      kind: snap.kind,
      source: snap.source,
      fingerprint: snap.fingerprint,
      trigger,
      policies: names,
    });
    this.#opts.onLoad?.({ at: snap.loadedAt, trigger, ok: true, fingerprint: snap.fingerprint });
  }

  async #compile(files: SourceFile[], fingerprint: string, loadedAt: Date): Promise<Snapshot> {
    const { pool, platform } = this.#opts;
    if (this.#roots.length === 0) {
      throw humane(
        `no ${AlertRouting.name} policy is configured, so there is nothing to load`,
        "list at least one team in the team directory, ALERTROUTER_TEAMS_FILE",
      );
    }
    const rejected = (root: Root, cause: unknown) =>
      wrap(
        compileCause(cause),
        `the ${AlertRouting.name} policies from ${this.source} don't load: ${root.policy} failed to compile${
          this.#current === undefined
            ? ", and there is no earlier bundle to fall back to"
            : ", so the previous bundle keeps serving"
        }`,
        "fix the diagnostics in the policies and reload",
        "run `sigil check --config policies/sigil.yaml policies` on the policies to see the same diagnostics before deploying",
      );

    const loaded = new Map<string, PooledPolicy>();
    try {
      for (const root of this.#roots) {
        try {
          // Like Go's Kind.Load, compile reads every document of the bundle,
          // so a broken one anywhere rejects the bundle rather than waiting
          // for the day a policy starts using it.
          //
          // The platform's documents go in as the trusted files, apart from
          // the team bundle: platform.paging must be defined there, so a team
          // file that omits, gates or redefines it, or pages outside its
          // param bounds, fails to compile. Trust comes from the list, not
          // from a path a team could imitate.
          loaded.set(
            root.team,
            await pool.compile(files, {
              policy: root.policy,
              require: [{ policy: REQUIRED_POLICY }],
              trustedFiles: [...platform],
            }),
          );
        } catch (err) {
          throw rejected(root, err);
        }
      }
    } catch (err) {
      await Promise.allSettled([...loaded.values()].map((p) => p.release()));
      throw err;
    }
    return new Snapshot(this.source, fingerprint, loadedAt, this.#roots, files, loaded);
  }

  #fail(span: Span, trigger: Trigger, err: HumaneError): HumaneError {
    const { telemetry, clock } = this.#opts;
    span.recordException(err);
    span.setStatus({ code: SpanStatusCode.ERROR, message: "policy bundle rejected" });
    telemetry.metrics.observeReloadFailure();
    const at = new Date(clock.now());
    this.#opts.onLoad?.({ at, trigger, ok: false, error: err });
    if (trigger === "startup") return err;

    this.#lastFailure = { at, trigger, error: err };
    telemetry.logger.error(
      this.#current === undefined
        ? "policy bundle rejected, nothing is loaded yet"
        : "policy bundle rejected, the previous bundle keeps serving",
      {
        kind: AlertRouting.name,
        source: this.source,
        trigger,
        error: err.message,
        cause: err.cause === undefined ? undefined : messageOf(err.cause),
        advice: adviceOf(err),
      },
    );
    return err;
  }
}

/** A compile error as the cause of a rejected load: the message with every diagnostic, one per line. */
function compileCause(err: unknown): Error {
  if (isEngineFailure(err)) {
    return new Error(`the bundle made the evaluation engine fail (${messageOf(err)}); the engine was replaced`);
  }
  if (!(err instanceof SigilError)) return err instanceof Error ? err : new Error(String(err));
  const lines = err.diagnostics
    .filter((d) => d.severity === "error")
    .map((d) => {
      const at = [d.file, d.line, d.column].filter((p) => p !== undefined).join(":");
      return `${at === "" ? "" : `${at}: `}${d.message}${d.help === undefined ? "" : ` (${d.help})`}`;
    });
  const text = lines.length === 0 ? err.message : `${err.message}\n${lines.join("\n")}`;
  return new Error(text);
}

function asHumane(err: unknown): HumaneError {
  if (err instanceof HumaneError) return err;
  return wrap(
    err,
    `loading the policy bundle failed: ${messageOf(err)}`,
    "check the alertrouter logs around this line for the cause",
  );
}
