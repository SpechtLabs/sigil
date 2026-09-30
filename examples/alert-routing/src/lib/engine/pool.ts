// The evaluation pool: team policies compile and evaluate in a few
// worker_threads workers, each running its own Sigil instance, so an
// evaluation never holds the event loop that answers /readyz, streams the
// console and delivers notifications. Each worker holds every team's policy;
// a task goes to the first free worker, and one team may occupy at most half
// of the pool, so a team whose policy is slow can't starve the others.
//
// A worker whose module stopped (a Go panic, a trap, a call that ran out of
// stack) is replaced, and so is one that doesn't answer shortly after its
// evaluation's deadline: the package's SigilWorker terminates it, starts a
// new one on the next call and compiles the policies in it again by itself.
// The pool only reports it.

import type { EvalResult, Explanation, SourceFile } from "@spechtlabs/sigil";
import { SigilTimeoutError, SigilWorker, type WorkerCompileOptions, type WorkerPolicy } from "@spechtlabs/sigil/worker";

import type { Clock } from "../clock";
import { humane } from "../errors";
import { AlertRouting, type Input } from "../routing/kind";
import { isEngineFailure, workerEntryPath } from "../sigil";
import type { Telemetry } from "../telemetry/types";

/** A task the pool didn't start before its start deadline. */
export class NotStartedError extends Error {
  override readonly name = "NotStartedError";
}

export interface PoolOptions {
  /** The compiled sigil.wasm every worker instantiates. */
  module: WebAssembly.Module;
  /** How many workers; at least 1. */
  size: number;
  /** Starts one worker running the package's worker entry. */
  spawn: () => WorkerLike;
  telemetry: Telemetry;
  /** Reads the time start deadlines are measured in. */
  clock: Clock;
  /** Bounds a worker's start and every call but evaluations. */
  callTimeoutMs?: number;
}

/** What the pool needs of a worker: the part of worker_threads' Worker SigilWorker uses. */
export interface WorkerLike {
  postMessage(message: unknown): void;
  terminate(): unknown;
}

/** A team policy compiled in every worker of the pool. */
export class PooledPolicy {
  #released = false;

  /** @internal PooledPolicies come from EvaluatorPool.compile. */
  constructor(
    readonly name: string,
    readonly perWorker: readonly WorkerPolicy<Input>[],
  ) {}

  /** Whether release was called. */
  get released(): boolean {
    return this.#released;
  }

  /** Frees the policy in every worker. Calling it again does nothing. */
  async release(): Promise<void> {
    if (this.#released) return;
    this.#released = true;
    await Promise.allSettled(this.perWorker.map((p) => p.release()));
  }

  /** The policy flattened into its rules, from the first worker. */
  explain(): Promise<Explanation> {
    return (this.perWorker[0] as WorkerPolicy<Input>).explain();
  }
}

interface Slot {
  index: number;
  worker: SigilWorker;
  busy: boolean;
}

interface Task {
  team: string;
  startBy: number | undefined;
  run: (slot: Slot) => Promise<void>;
  reject: (err: unknown) => void;
}

export class EvaluatorPool {
  readonly #opts: PoolOptions;
  readonly #slots: Slot[];
  readonly #queue: Task[] = [];
  readonly #running = new Map<string, number>();
  readonly #perTeam: number;
  #closed = false;

  constructor(opts: PoolOptions) {
    this.#opts = opts;
    const size = Math.max(1, Math.floor(opts.size));
    this.#perTeam = Math.max(1, Math.floor(size / 2));
    this.#slots = Array.from({ length: size }, (_, index) => ({
      index,
      busy: false,
      worker: new SigilWorker({
        wasm: opts.module,
        worker: opts.spawn,
        timeoutMs: opts.callTimeoutMs ?? 10_000,
      }),
    }));
  }

  /** How many workers the pool runs. */
  get size(): number {
    return this.#slots.length;
  }

  /** Starts every worker, so the first evaluations don't pay for it. */
  async start(): Promise<void> {
    await Promise.all(this.#slots.map((s) => s.worker.version()));
  }

  /**
   * Compiles one team root in every worker. Throws the compiler's error when
   * it doesn't compile; a worker the bundle made fail is replaced.
   */
  async compile(files: SourceFile[], options: WorkerCompileOptions & { policy: string }): Promise<PooledPolicy> {
    const results = await Promise.allSettled(
      this.#slots.map((slot) =>
        AlertRouting.compile(slot.worker, files, options).catch((err: unknown) => {
          if (isEngineFailure(err)) this.#replaced(slot);
          throw err;
        }),
      ),
    );
    const compiled = results.flatMap((r) => (r.status === "fulfilled" ? [r.value] : []));
    const failed = results.find((r): r is PromiseRejectedResult => r.status === "rejected");
    if (failed !== undefined) {
      await Promise.allSettled(compiled.map((p) => p.release()));
      throw failed.reason;
    }
    return new PooledPolicy(options.policy, compiled);
  }

  /**
   * Evaluates policy for team in the first free worker, once team has fewer
   * than half the workers busy. A task not started by startBy, in
   * the clock's milliseconds, rejects with NotStartedError instead. An
   * evaluation past timeoutMs comes back with a canceled failure, or, when
   * the worker doesn't answer in time, rejects with SigilTimeoutError after
   * the worker was replaced.
   */
  evaluate(
    team: string,
    policy: PooledPolicy,
    input: Input,
    opts: { timeoutMs: number; startBy?: number },
  ): Promise<EvalResult> {
    if (this.#closed) return Promise.reject(humane("the evaluation pool is shut down", "alertrouter is stopping"));
    return new Promise<EvalResult>((resolve, reject) => {
      this.#queue.push({
        team,
        startBy: opts.startBy,
        reject,
        run: async (slot) => {
          try {
            const p = policy.perWorker[slot.index];
            if (p === undefined)
              throw humane(
                `${policy.name} isn't compiled in worker ${slot.index}`,
                "this is a bug in alertrouter; please report it",
              );
            resolve(await p.eval(input, { timeoutMs: opts.timeoutMs }));
          } catch (err) {
            if (err instanceof SigilTimeoutError || isEngineFailure(err)) this.#replaced(slot);
            reject(err);
          }
        },
      });
      this.#pump();
    });
  }

  /** Stops every worker and fails what's queued. */
  close(): void {
    this.#closed = true;
    for (const task of this.#queue.splice(0))
      task.reject(humane("the evaluation pool is shut down", "alertrouter is stopping"));
    for (const slot of this.#slots) slot.worker.terminate();
  }

  // Starts queued tasks on free workers, oldest first, skipping a task whose
  // team already holds its share of the pool.
  #pump(): void {
    for (const slot of this.#slots) {
      if (slot.busy) continue;
      const i = this.#queue.findIndex((t) => (this.#running.get(t.team) ?? 0) < this.#perTeam);
      if (i < 0) return;
      const task = this.#queue.splice(i, 1)[0] as Task;
      if (task.startBy !== undefined && this.#opts.clock.now() > task.startBy) {
        task.reject(new NotStartedError("no worker was free before the batch's evaluation deadline"));
        // The slot is still free for the next task.
        this.#pump();
        return;
      }
      slot.busy = true;
      this.#running.set(task.team, (this.#running.get(task.team) ?? 0) + 1);
      void task.run(slot).finally(() => {
        slot.busy = false;
        this.#running.set(task.team, (this.#running.get(task.team) ?? 1) - 1);
        this.#pump();
      });
    }
  }

  // Reports a worker the helper terminated; the next call on the slot starts
  // a fresh one, and the policies compile in it again by themselves.
  #replaced(slot: Slot): void {
    this.#opts.telemetry.metrics.observeEngineRestart("worker");
    this.#opts.telemetry.logger.error("an evaluation worker failed; starting a new one", { worker: slot.index });
  }
}

/**
 * Starts a worker_threads Worker on the package's worker entry, on Node and
 * under `bun test` alike. It's unref'd: the pool, not a worker, decides
 * when the process may exit.
 */
export function spawnWorker(): WorkerLike {
  const { Worker } = process.getBuiltinModule("node:worker_threads");
  const worker = new Worker(workerEntryPath());
  worker.unref();
  return worker;
}
