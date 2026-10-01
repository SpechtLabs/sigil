import { type Envelope, Runtime } from "./abi.js";
import { markResult, type ResultShape } from "./decision.js";
import { SigilError, type SigilStoppedError } from "./errors.js";
import type {
  CheckOptions,
  CompileOptions,
  Diagnostic,
  EvalOptions,
  EvalResult,
  Explanation,
  ExplainOptions,
  FormatOptions,
  HostFunction,
  JsonValue,
  SourceFile,
  TestOptions,
  TestResult,
  VersionInfo,
} from "./types.js";

/** Where to load sigil.wasm from. A string is a URL. */
export type WasmSource =
  | URL
  | string
  | Response
  | PromiseLike<Response>
  | ArrayBuffer
  | ArrayBufferView
  | WebAssembly.Module;

export interface LoadOptions {
  /**
   * Receives what the module writes to standard output and error, a line at
   * a time. The default passes standard error to console.error; the module
   * writes there only when something is badly wrong.
   */
  output?: (stream: "stdout" | "stderr", line: string) => void;
}

// Symbol.dispose where the platform has it; the well-known fallback key
// TypeScript's `using` looks up where it doesn't.
const dispose: typeof Symbol.dispose = Symbol.dispose ?? (Symbol.for("Symbol.dispose") as typeof Symbol.dispose);

/**
 * One instance of the Sigil engine. Every method is synchronous: the work
 * happens inside WebAssembly on the calling thread. A UI that must never
 * block uses the worker helper (`@spechtlabs/sigil/worker`) instead.
 *
 * The answers are the stock `sigil` CLI's for the same files: `check` is
 * `sigil check -o json`, `eval` is `sigil eval -o json`, and so on.
 */
export class Sigil {
  readonly #runtime: Runtime;
  readonly #finalizer: FinalizationRegistry<number>;

  private constructor(runtime: Runtime) {
    this.#runtime = runtime;
    // A policy dropped without release() gives its handle back when it's
    // collected. Collection happens between calls, never during one.
    this.#finalizer = new FinalizationRegistry((handle) => {
      if (runtime.stopped === undefined) {
        try {
          runtime.call({ op: "release", handle });
        } catch {
          // The module is busy or gone; the handle goes with it.
        }
      }
    });
  }

  /** Loads and initializes the module. */
  static async load(source: WasmSource, options: LoadOptions = {}): Promise<Sigil> {
    const module = await compileModule(source);
    const output = options.output ?? defaultOutput;
    const lines = { stdout: "", stderr: "" };
    const runtime = await Runtime.instantiate(module, {
      write(fd, text) {
        const stream = fd === 1 ? "stdout" : "stderr";
        const parts = (lines[stream] + text).split("\n");
        lines[stream] = parts.pop() ?? "";
        for (const line of parts) output(stream, line);
      },
    });
    return new Sigil(runtime);
  }

  /**
   * The error every call throws once the instance has stopped, or
   * undefined while it works. An instance stops when Go's runtime exits or
   * panics, the module traps, or an exception such as a stack overflow
   * unwinds it mid-call; the call that stopped it throws the same
   * {@link SigilStoppedError}. A host that keeps an instance for long
   * checks this (or catches the error) and loads a new one.
   */
  get stopped(): SigilStoppedError | undefined {
    return this.#runtime.stopped;
  }

  /** The module's version and build information. */
  version(): VersionInfo {
    return this.#request("version", {}) as unknown as VersionInfo;
  }

  /**
   * Checks the files like `sigil check`: syntax, types, the kind, the
   * requirements and the lints. Errors and warnings alike come back as
   * diagnostics; it throws only for a request the module can't handle.
   */
  check(files: SourceFile[], options: CheckOptions = {}): Diagnostic[] {
    return this.#request("check", { files, ...wire(options) })["diagnostics"] as Diagnostic[];
  }

  /**
   * Compiles one policy of the files into a {@link Policy} to evaluate.
   * Every document of the policy's kind among the files and the trusted
   * files must check, even one the policy never uses, as with Go's
   * Kind.Load: the compile throws a {@link SigilError} with the
   * diagnostics `check` reports for them otherwise, and so does a
   * document of a kind the files don't provide.
   */
  compile(files: SourceFile[], options: CompileOptions = {}): Policy {
    const { functions, ...rest } = options;
    const names = Object.keys(functions ?? {});
    const res = this.#request("compile", { files, ...wire(rest), ...(names.length > 0 ? { functions: names } : {}) });
    const handle = res["handle"] as number;
    const policy = new Policy(
      this.#runtime,
      handle,
      res["policy"] as string,
      (res["diagnostics"] as Diagnostic[] | undefined) ?? [],
      functions ?? {},
      () => this.#finalizer.unregister(policy),
    );
    this.#finalizer.register(policy, handle, policy);
    return policy;
  }

  /**
   * Explains the files' policies like `sigil explain`: every rule and
   * assert each can reach, with its conditions and call chain. `policy`
   * narrows it to one.
   */
  explain(files: SourceFile[], options: ExplainOptions = {}): Explanation[] {
    return explanations(this.#request("explain", { files, ...wire(options) }));
  }

  /**
   * Formats a source in `sigil fmt`'s canonical style. Throws a
   * {@link SigilError} with the diagnostics when it doesn't parse.
   */
  format(source: string, options: FormatOptions = {}): string {
    return this.#request("format", { source, ...options })["source"] as string;
  }

  /**
   * Runs test files against the files like `sigil test`: one result per
   * test file, in the order of their paths. A test file whose policy the
   * files don't define is a result that says so. Host functions come from
   * the test files' `stubs:`. A test file that can't run is a result with
   * `error`, and a failing case one with `passed: false`; it throws a
   * {@link SigilError} only for a request the CLI's command line couldn't
   * have given, such as no test files, a test file not named
   * `*_test.yaml` or `*_test.yml`, or a `run` that isn't a regular
   * expression.
   */
  test(files: SourceFile[], tests: SourceFile[], options: TestOptions = {}): TestResult[] {
    const { data, run, ...rest } = wire(options);
    const fields: Record<string, unknown> = { files, test_files: tests, ...rest };
    if (data !== undefined) fields["data_files"] = data;
    if (run !== undefined) fields["run"] = run;
    return this.#request("test", fields)["results"] as TestResult[];
  }

  #request(op: string, fields: Record<string, unknown>): Record<string, unknown> {
    return unwrap(this.#runtime.call({ op, ...fields }));
  }
}

/**
 * A compiled policy, held inside the module. Evaluating it sends only the
 * input across. Release it when done (or let `using` do it); a policy
 * that's garbage collected is released too, eventually.
 */
export class Policy<I extends object = Record<string, JsonValue>> {
  /** The policy's name. */
  readonly name: string;
  /**
   * Diagnostics from compiling it. compile checks the whole bundle and
   * fails on any error, as Go's Kind.Load does, so this is empty today;
   * lint warnings come from check.
   */
  readonly diagnostics: Diagnostic[];

  readonly #runtime: Runtime;
  #handle: number | undefined;
  readonly #functions: Record<string, HostFunction>;
  readonly #unregister: () => void;

  /** @internal Policies come from {@link Sigil.compile}. */
  constructor(
    runtime: Runtime,
    handle: number,
    name: string,
    diagnostics: Diagnostic[],
    functions: Record<string, HostFunction>,
    unregister: () => void,
  ) {
    this.#runtime = runtime;
    this.#handle = handle;
    this.name = name;
    this.diagnostics = diagnostics;
    this.#functions = functions;
    this.#unregister = unregister;
  }

  /** The module's handle for the policy; undefined once released. */
  get handle(): number | undefined {
    return this.#handle;
  }

  /**
   * Evaluates the policy against one input. A failed evaluation (a runtime
   * error, a conflict, a failing assert) still returns a result, with
   * `error` set and the kind's fallback as the outcome. It throws a
   * {@link SigilError} for an input that doesn't fit the kind.
   */
  eval(input: I, options: EvalOptions = {}): EvalResult {
    const handle = this.#live();
    const fields: Record<string, unknown> = { op: "eval", handle, input };
    if (options.timeoutMs !== undefined) fields["timeout_ms"] = options.timeoutMs;
    const functions = this.#functions;
    this.#runtime.hostCall = ({ function: name, args }) => {
      const fn = functions[name];
      if (fn === undefined) throw new Error(`no host function ${name} is registered`);
      const result = fn(...args);
      if (result !== null && typeof result === "object" && typeof (result as { then?: unknown }).then === "function") {
        throw new Error(`host function ${name} returned a promise; host functions must be synchronous`);
      }
      return result;
    };
    try {
      return markResult(unwrap(this.#runtime.call(fields)) as unknown as EvalResult, shapes.get(this));
    } finally {
      this.#runtime.hostCall = undefined;
    }
  }

  /** Explains the policy like `sigil explain`. */
  explain(): Explanation {
    const all = explanations(unwrap(this.#runtime.call({ op: "explain", handle: this.#live() })));
    const first = all[0];
    if (first === undefined) throw new SigilError(`the module returned no explanation for policy ${this.name}`);
    return first;
  }

  /** Frees the policy inside the module. Calling it again does nothing. */
  release(): void {
    const handle = this.#handle;
    if (handle === undefined) return;
    this.#handle = undefined;
    this.#unregister();
    if (this.#runtime.stopped === undefined) unwrap(this.#runtime.call({ op: "release", handle }));
  }

  [dispose](): void {
    this.release();
  }

  #live(): number {
    if (this.#handle === undefined) {
      throw new SigilError(`policy ${this.name} was released`, { help: "Compile it again to evaluate it." });
    }
    return this.#handle;
  }
}

/** Turns a failed envelope into a SigilError, and strips ok and id off a successful one. */
export function unwrap(res: Envelope): Record<string, unknown> {
  const { ok, id: _id, ...fields } = res;
  if (ok !== true) {
    throw new SigilError(res.error?.message ?? "the module returned an error without a message", {
      help: res.error?.help,
      diagnostics: (res.diagnostics as Diagnostic[] | undefined) ?? [],
    });
  }
  return fields;
}

function explanations(res: Record<string, unknown>): Explanation[] {
  return (res["explanations"] as Explanation[] | undefined) ?? [];
}

function defaultOutput(stream: "stdout" | "stderr", line: string): void {
  if (stream === "stderr") console.error(`sigil.wasm: ${line}`);
}

/** Compiles sigil.wasm from any {@link WasmSource}. */
export async function compileModule(source: WasmSource): Promise<WebAssembly.Module> {
  if (source instanceof WebAssembly.Module) return source;
  if (source instanceof ArrayBuffer || ArrayBuffer.isView(source)) return WebAssembly.compile(source as BufferSource);
  if (typeof source === "string" || source instanceof URL) {
    let url: URL;
    try {
      url = new URL(source, (globalThis as { location?: { href: string } }).location?.href);
    } catch (err) {
      throw new SigilError(`${String(source)} isn't a URL`, {
        help: "Pass an absolute URL: a file: URL (pathToFileURL(path)) for a local file, or import.meta.resolve(...).",
        cause: err,
      });
    }
    if (url.protocol === "file:") return WebAssembly.compile(await readFile(url));
    return compileResponse(await fetch(url));
  }
  return compileResponse(await source);
}

async function compileResponse(res: Response): Promise<WebAssembly.Module> {
  if (!res.ok) throw new SigilError(`fetching sigil.wasm from ${res.url}: ${res.status} ${res.statusText}`);
  // Streaming compilation needs the right content type; a server that
  // doesn't send it still gets a working, if slower, load.
  if (typeof WebAssembly.compileStreaming === "function" && res.headers.get("content-type")?.startsWith("application/wasm")) {
    return WebAssembly.compileStreaming(res);
  }
  return WebAssembly.compile(await res.arrayBuffer());
}

/**
 * Reads a file: URL on the server-side runtimes, which all have
 * node:fs/promises. The specifier is a variable so browser bundlers don't
 * try to resolve it.
 */
async function readFile(url: URL): Promise<Uint8Array<ArrayBuffer>> {
  const specifier = "node:fs/promises";
  const fs = (await import(/* @vite-ignore */ specifier)) as { readFile(path: URL): Promise<Uint8Array<ArrayBuffer>> };
  return fs.readFile(url);
}

// The shape of each kind-compiled policy's results, which Kind.compile
// records so decision handles read them right.
const shapes = new WeakMap<Policy<object>, ResultShape>();

/** @internal Records how a policy's results read; see {@link markResult}. */
export function setResultShape(policy: Policy<object>, shape: ResultShape): void {
  shapes.set(policy, shape);
}

/** Renames the options whose request field is spelled differently: trustedFiles is trusted_files. */
function wire<T extends { trustedFiles?: SourceFile[] | undefined }>(options: T): Omit<T, "trustedFiles"> & { trusted_files?: SourceFile[] } {
  const { trustedFiles, ...rest } = options;
  return trustedFiles === undefined ? rest : { ...rest, trusted_files: trustedFiles };
}
