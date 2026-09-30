// The worker helper: the Sigil API, asynchronous, with the module running
// in a worker the helper terminates when a call runs past its deadline. For
// UIs that must never freeze, like the docs playground; code that can block
// uses the synchronous Sigil class directly.

import { markResult, type ResultShape } from "./decision.js";
import { SigilError, SigilStoppedError, SigilTimeoutError } from "./errors.js";
import type { CallMessage, InitMessage, Reply, Request, WireError } from "./protocol.js";
import type {
  CheckOptions,
  CompileOptions,
  Diagnostic,
  EvalOptions,
  EvalResult,
  Explanation,
  ExplainOptions,
  FormatOptions,
  JsonValue,
  SourceFile,
  VersionInfo,
} from "./types.js";

export { SigilError, SigilStoppedError, SigilTimeoutError } from "./errors.js";
export type * from "./types.js";

// The default deadline of a call, and the extra time an evaluation with its
// own timeoutMs gets to report the module's timeout before the worker is
// terminated.
const DEFAULT_TIMEOUT_MS = 10_000;
const DEFAULT_START_TIMEOUT_MS = 30_000;
const EVAL_GRACE_MS = 500;

/** The part of a Web Worker (or a Node worker_threads Worker) the helper uses. */
export interface WorkerLike {
  postMessage(message: unknown): void;
  terminate(): unknown;
}

export interface SigilWorkerOptions {
  /**
   * Where the worker loads sigil.wasm from: a URL (relative ones resolve
   * against the page), its bytes (copied), or a compiled module.
   */
  wasm: URL | string | ArrayBuffer | WebAssembly.Module;
  /**
   * The URL of an ES module whose named exports are host functions. The
   * worker imports it, and {@link SigilWorker.compile} picks functions from
   * it by name: functions themselves can't cross to a worker.
   */
  functions?: URL | string;
  /** The deadline of each call, in milliseconds. Defaults to 10 seconds. */
  timeoutMs?: number;
  /** The deadline of starting a worker and loading the module. Defaults to 30 seconds. */
  startTimeoutMs?: number;
  /**
   * Creates the worker running `@spechtlabs/sigil/worker-entry`. The default
   * works where the package is used unbundled or through a bundler that
   * understands `new Worker(new URL(..., import.meta.url))`, like Vite.
   */
  worker?: () => WorkerLike | Promise<WorkerLike>;
}

/** Like {@link CompileOptions}, with host functions named from the functions module. */
export interface WorkerCompileOptions extends Omit<CompileOptions, "functions"> {
  functions?: string[];
}

/** Per-call options. */
export interface CallOptions {
  /** This call's deadline, instead of the helper's. */
  timeoutMs?: number;
}

/**
 * The Sigil API in a worker. Every method returns a promise. A call that
 * runs past its deadline terminates the worker and rejects with a
 * {@link SigilTimeoutError}, and a call that stops the module in it (see
 * {@link SigilStoppedError}) does the same with that error; the next call
 * starts a fresh worker, and policies compile again in it transparently.
 */
export class SigilWorker {
  readonly #options: SigilWorkerOptions;
  #session: Session | undefined;
  #generation = 0;

  constructor(options: SigilWorkerOptions) {
    this.#options = options;
  }

  async version(options?: CallOptions): Promise<VersionInfo> {
    return (await this.call("version", [], options)).value as VersionInfo;
  }

  async check(files: SourceFile[], options: CheckOptions = {}, call?: CallOptions): Promise<Diagnostic[]> {
    return (await this.call("check", [files, options], call)).value as Diagnostic[];
  }

  async compile(files: SourceFile[], options: WorkerCompileOptions = {}, call?: CallOptions): Promise<WorkerPolicy> {
    return new WorkerPolicy(this, files, options, await this.compiled(files, options, call));
  }

  async explain(files: SourceFile[], options: ExplainOptions = {}, call?: CallOptions): Promise<Explanation[]> {
    return (await this.call("explain", [files, options], call)).value as Explanation[];
  }

  async format(source: string, options: FormatOptions = {}, call?: CallOptions): Promise<string> {
    return (await this.call("format", [source, options], call)).value as string;
  }

  /** Stops the worker and fails the calls waiting on it. The next call starts a new one. */
  terminate(reason: Error = new SigilError("the Sigil worker was terminated")): void {
    const session = this.#session;
    this.#session = undefined;
    void session?.connection.then(
      (c) => c.close(reason),
      () => {},
    );
  }

  /** @internal Whether the worker that compiled a policy is still the current one. */
  current(generation: number): boolean {
    return this.#session?.generation === generation;
  }

  /** @internal Compiles in the current worker, noting which one that is. */
  async compiled(files: SourceFile[], options: WorkerCompileOptions, call?: CallOptions): Promise<Compiled> {
    const { value, generation } = await this.call("compile", [files, options], call);
    return { ...(value as Omit<Compiled, "generation">), generation };
  }

  /**
   * @internal Sends one call to the current worker, starting one if there
   * is none, and terminates the worker when the call times out.
   */
  async call(method: CallMessage["method"], args: unknown[], options?: CallOptions, deadline?: number): Promise<{ value: unknown; generation: number }> {
    const session = this.#connect();
    const connection = await session.connection;
    const timeoutMs = deadline ?? options?.timeoutMs ?? this.#options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    try {
      return { value: await connection.send({ id: 0, method, args }, timeoutMs), generation: session.generation };
    } catch (err) {
      // A worker past its deadline, or whose instance stopped, can't take
      // another call; the next one starts a fresh worker.
      if ((err instanceof SigilTimeoutError || err instanceof SigilStoppedError) && this.#session === session) this.terminate(err);
      throw err;
    }
  }

  #connect(): Session {
    if (this.#session === undefined) {
      const session: Session = { connection: this.#start(), generation: ++this.#generation };
      this.#session = session;
      // A worker that fails to start isn't kept: the next call tries again.
      session.connection.catch(() => {
        if (this.#session === session) this.#session = undefined;
      });
    }
    return this.#session;
  }

  async #start(): Promise<Connection> {
    const o = this.#options;
    const connection = new Connection(await (o.worker ?? defaultWorker)());
    const init: InitMessage = {
      id: 0,
      method: "init",
      wasm: typeof o.wasm === "string" || o.wasm instanceof URL ? absolute(o.wasm) : o.wasm,
      functions: o.functions === undefined ? undefined : absolute(o.functions),
    };
    try {
      await connection.send(init, o.startTimeoutMs ?? DEFAULT_START_TIMEOUT_MS);
    } catch (err) {
      connection.close(err instanceof Error ? err : new SigilError(String(err)));
      throw err;
    }
    return connection;
  }
}

/** One started worker; generation tells policies compiled in an earlier one apart. */
interface Session {
  connection: Promise<Connection>;
  generation: number;
}

/**
 * A policy compiled in the worker. It remembers its files and options, so it
 * compiles again by itself after a timeout replaced the worker.
 */
export class WorkerPolicy<I extends object = Record<string, JsonValue>> {
  readonly name: string;
  readonly diagnostics: Diagnostic[];
  /** @internal How the policy's results read, when a kind compiled it. */
  shape: ResultShape | undefined;

  readonly #worker: SigilWorker;
  readonly #files: SourceFile[];
  readonly #options: WorkerCompileOptions;
  #compiled: Compiled | undefined;

  /** @internal Policies come from {@link SigilWorker.compile}. */
  constructor(worker: SigilWorker, files: SourceFile[], options: WorkerCompileOptions, compiled: Compiled) {
    this.#worker = worker;
    this.#files = files;
    this.#options = options;
    this.#compiled = compiled;
    this.name = compiled.name;
    this.diagnostics = compiled.diagnostics;
  }

  /**
   * Evaluates the policy. `timeoutMs` goes to the module, which stops the
   * evaluation with a `canceled` failure; the worker is terminated only if that
   * doesn't happen shortly after.
   */
  async eval(input: I, options: EvalOptions = {}): Promise<EvalResult> {
    const handle = await this.#handle();
    const deadline = options.timeoutMs === undefined ? undefined : options.timeoutMs + EVAL_GRACE_MS;
    const res = (await this.#worker.call("eval", [handle, input, options], undefined, deadline)).value as EvalResult;
    return markResult(res, this.shape);
  }

  async explain(options?: CallOptions): Promise<Explanation> {
    return (await this.#worker.call("explainPolicy", [await this.#handle()], options)).value as Explanation;
  }

  /** Frees the policy in the worker. Calling it again does nothing. */
  async release(): Promise<void> {
    const compiled = this.#compiled;
    this.#compiled = undefined;
    if (compiled !== undefined && this.#worker.current(compiled.generation)) {
      await this.#worker.call("release", [compiled.handle]);
    }
  }

  async #handle(): Promise<number> {
    const compiled = this.#compiled;
    if (compiled === undefined) {
      throw new SigilError(`policy ${this.name} was released`, { help: "Compile it again to evaluate it." });
    }
    if (!this.#worker.current(compiled.generation)) {
      this.#compiled = await this.#worker.compiled(this.#files, this.#options);
    }
    return (this.#compiled as Compiled).handle;
  }
}

/** @internal What the worker returns for a compile, and which worker holds it. */
export interface Compiled {
  handle: number;
  name: string;
  diagnostics: Diagnostic[];
  generation: number;
}

/** One worker and the calls waiting on it. */
class Connection {
  readonly #worker: WorkerLike;
  readonly #pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();
  #next = 1;
  #closed: Error | undefined;

  constructor(worker: WorkerLike) {
    this.#worker = worker;
    const onMessage = (reply: Reply) => {
      const p = this.#pending.get(reply.id);
      if (p === undefined) return;
      this.#pending.delete(reply.id);
      if (reply.ok) p.resolve(reply.value);
      else p.reject(fromWire(reply.error));
    };
    const onError = (err: unknown) => {
      const message = err instanceof Error ? err.message : ((err as { message?: string }).message ?? String(err));
      this.close(new SigilError(`the Sigil worker failed: ${message}`));
    };
    const w = worker as {
      addEventListener?: (type: string, l: (ev: { data?: Reply }) => void) => void;
      on?: (type: string, l: (v: unknown) => void) => void;
    };
    // A worker_threads Worker is an event emitter; Bun's also has an
    // addEventListener that never hears its messages, so `on` comes first.
    if (typeof w.on === "function") {
      w.on("message", (data) => onMessage(data as Reply));
      w.on("error", onError);
      w.on("exit", (code) => this.close(new SigilError(`the Sigil worker exited with code ${String(code)}`)));
    } else if (typeof w.addEventListener === "function") {
      w.addEventListener("message", (ev) => onMessage(ev.data as Reply));
      w.addEventListener("error", (ev) => onError(ev));
      w.addEventListener("messageerror", (ev) => onError(ev));
    }
  }

  send(message: Request, timeoutMs: number): Promise<unknown> {
    if (this.#closed !== undefined) return Promise.reject(this.#closed);
    const id = this.#next++;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new SigilTimeoutError(`the Sigil worker didn't answer ${message.method} within ${timeoutMs} ms`, {
          help: "The worker was terminated; the next call starts a new one.",
        }));
      }, timeoutMs);
      this.#pending.set(id, {
        resolve: (v) => {
          clearTimeout(timer);
          resolve(v);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
      this.#worker.postMessage({ ...message, id });
    });
  }

  /** Terminates the worker and fails every waiting call with err. */
  close(err: Error): void {
    if (this.#closed !== undefined) return;
    this.#closed = err;
    void this.#worker.terminate();
    for (const p of this.#pending.values()) p.reject(err);
    this.#pending.clear();
  }
}

function fromWire(e: WireError): SigilError {
  const Ctor = e.name === "SigilTimeoutError" ? SigilTimeoutError : e.name === "SigilStoppedError" ? SigilStoppedError : SigilError;
  return new Ctor(e.message, { help: e.help, diagnostics: e.diagnostics });
}

function absolute(url: URL | string): string {
  return new URL(url, (globalThis as { location?: { href: string } }).location?.href).href;
}

/**
 * Starts worker-entry.js next to this file: a Web Worker where the platform
 * has them (browsers, Bun, Deno), a worker_threads Worker on Node.
 */
async function defaultWorker(): Promise<WorkerLike> {
  if (typeof Worker !== "undefined") {
    return new Worker(new URL("./worker-entry.js", import.meta.url), { type: "module" });
  }
  const specifier = "node:worker_threads";
  const threads = (await import(/* @vite-ignore */ specifier)) as { Worker: new (url: URL) => WorkerLike };
  return new threads.Worker(new URL("./worker-entry.js", import.meta.url));
}
