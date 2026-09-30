// The flat ABI of sigil.wasm (version 1), from the host's side. The Go
// package cmd/sigil-wasm documents it in full; in short:
//
//   - sigil_alloc(size) -> ptr     the host writes a request there
//   - sigil_call(ptr, len) -> i64  handles one JSON request; the result packs
//                                  the response as (ptr << 32) | len
//   - sigil_free(ptr, size)        releases a request after the call, and a
//                                  response once the host has read it
//
// and the one import, sigil.host_call(ptr, len) -> i64, through which the
// module asks the host to run a host function. The module owns (and frees)
// that request; the host answers in memory from sigil_alloc, which the module
// frees once it has read it.

import { SigilError } from "./errors.js";
import type { JsonValue } from "./types.js";
import { createWasi, WASI_MODULE, WasiExit } from "./wasi.js";

/** The ABI version this package speaks. */
export const ABI_VERSION = 1;

/** The import namespace of the module's own imports. */
const HOST_MODULE = "sigil";

// How much of the module's standard error a stopped module's error quotes.
const STDERR_TAIL = 4096;

/** A request to run a host function, as the module sends it. */
export interface HostCallRequest {
  function: string;
  args: JsonValue[];
}

/**
 * Runs a host function for the module. It returns the result, or throws to
 * make the call fail with the thrown error's message.
 */
export type HostCallHandler = (request: HostCallRequest) => unknown;

/** A response envelope, before the op-specific fields are picked out. */
export interface Envelope {
  ok: boolean;
  id?: number;
  error?: { message: string; help?: string };
  diagnostics?: unknown[];
  [field: string]: unknown;
}

export interface RuntimeOptions {
  /** Receives the module's standard output and error, as they're written. */
  write?: ((fd: 1 | 2, text: string) => void) | undefined;
}

interface Exports {
  memory: WebAssembly.Memory;
  _initialize?: () => void;
  sigil_abi_version: () => number;
  sigil_alloc: (size: number) => number;
  sigil_free: (ptr: number, size: number) => void;
  sigil_call: (ptr: number, len: number) => bigint;
}

/**
 * One instance of the module, speaking the ABI: JSON in, JSON out. It is
 * synchronous and not re-entrant; a host function can't call back into the
 * instance that's running it.
 */
export class Runtime {
  /** Handles the module's host_call import; set around each call that may run host functions. */
  hostCall: HostCallHandler | undefined;

  readonly #exports: Exports;
  readonly #encoder = new TextEncoder();
  readonly #decoder = new TextDecoder();
  #stderr = "";
  #busy = false;
  #stopped: SigilError | undefined;

  private constructor(exports: Exports) {
    this.#exports = exports;
  }

  /** Instantiates the module and initializes the Go runtime. */
  static async instantiate(module: WebAssembly.Module, options: RuntimeOptions = {}): Promise<Runtime> {
    let runtime: Runtime | undefined;
    const wasiNames: string[] = [];
    const imports: WebAssembly.Imports = { [HOST_MODULE]: {} };
    for (const imp of WebAssembly.Module.imports(module)) {
      if (imp.module === WASI_MODULE) {
        wasiNames.push(imp.name);
      } else if (imp.module !== HOST_MODULE || imp.name !== "host_call") {
        throw new SigilError(`the module imports ${imp.module}.${imp.name}, which this package doesn't provide`, {
          help: "Build sigil.wasm from the same Sigil revision as this package (mise run wasm-build).",
        });
      }
    }
    const wasi = createWasi(wasiNames, {
      write: (fd, text) => {
        if (fd === 2 && runtime !== undefined) runtime.#stderr = (runtime.#stderr + text).slice(-STDERR_TAIL);
        options.write?.(fd, text);
      },
    });
    imports[WASI_MODULE] = wasi.imports;
    (imports[HOST_MODULE] as Record<string, unknown>)["host_call"] = (ptr: number, len: number): bigint => {
      if (runtime === undefined) throw new Error("host_call before the module was initialized");
      return runtime.#hostCall(ptr, len);
    };

    const instance = await WebAssembly.instantiate(module, imports);
    const exports = instance.exports as unknown as Exports;
    for (const name of ["memory", "sigil_abi_version", "sigil_alloc", "sigil_free", "sigil_call"] as const) {
      if (exports[name] === undefined) {
        throw new SigilError(`the module doesn't export ${name}; it isn't a sigil.wasm reactor`, {
          help: "Build it with mise run wasm-build (GOOS=wasip1, -buildmode=c-shared).",
        });
      }
    }
    wasi.bind(exports.memory);
    runtime = new Runtime(exports);
    runtime.#guard(() => exports._initialize?.());
    const version = runtime.#guard(() => exports.sigil_abi_version());
    if (version !== ABI_VERSION) {
      throw new SigilError(`the module speaks ABI version ${version}, and this package version ${ABI_VERSION}`, {
        help: "Use the sigil.wasm built from the same Sigil revision as this package.",
      });
    }
    return runtime;
  }

  /** The error every call throws once the module has stopped, or undefined while it runs. */
  get stopped(): SigilError | undefined {
    return this.#stopped;
  }

  /** Sends one request and returns the decoded response envelope. */
  call(request: Record<string, unknown>): Envelope {
    if (this.#busy) {
      throw new SigilError("the module is busy: a host function can't call back into the Sigil instance that runs it");
    }
    this.#busy = true;
    try {
      return this.#guard(() => {
        const e = this.#exports;
        const [ptr, len] = this.#write(JSON.stringify(request));
        let packed: bigint;
        try {
          packed = e.sigil_call(ptr, len);
        } finally {
          e.sigil_free(ptr, len);
        }
        return JSON.parse(this.#take(packed)) as Envelope;
      });
    } finally {
      this.#busy = false;
    }
  }

  /** Runs the host function the module asks for and returns the packed response. */
  #hostCall(ptr: number, len: number): bigint {
    let response: string;
    try {
      const request = JSON.parse(this.#read(ptr, len)) as HostCallRequest;
      if (this.hostCall === undefined) throw new Error(`no host function ${request.function} is registered`);
      const result = this.hostCall(request);
      response = JSON.stringify({ result: result === undefined ? null : result });
    } catch (err) {
      response = JSON.stringify({ error: err instanceof Error ? err.message : String(err) });
    }
    const [rptr, rlen] = this.#write(response);
    return (BigInt(rptr) << 32n) | BigInt(rlen);
  }

  /** Copies text into memory from sigil_alloc and returns where. */
  #write(text: string): [number, number] {
    const data = this.#encoder.encode(text);
    const ptr = this.#exports.sigil_alloc(data.length) >>> 0;
    if (ptr === 0 && data.length > 0) throw new SigilError("the module couldn't allocate memory for a request");
    new Uint8Array(this.#exports.memory.buffer, ptr, data.length).set(data);
    return [ptr, data.length];
  }

  #read(ptr: number, len: number): string {
    return this.#decoder.decode(new Uint8Array(this.#exports.memory.buffer, ptr >>> 0, len >>> 0));
  }

  /** Reads and frees a packed response. */
  #take(packed: bigint): string {
    const bits = BigInt.asUintN(64, packed);
    const ptr = Number(bits >> 32n);
    const len = Number(bits & 0xffff_ffffn);
    try {
      return this.#read(ptr, len);
    } finally {
      this.#exports.sigil_free(ptr, len);
    }
  }

  /**
   * Runs fn against the module, and turns the module stopping (proc_exit, a
   * trap) into a SigilError every later call repeats: Go's runtime can't
   * resume after either.
   */
  #guard<T>(fn: () => T): T {
    if (this.#stopped !== undefined) throw this.#stopped;
    try {
      return fn();
    } catch (err) {
      if (err instanceof SigilError) throw err;
      if (err instanceof WasiExit || err instanceof WebAssembly.RuntimeError) {
        const why = err instanceof WasiExit ? `exited with code ${err.code}` : `trapped: ${err.message}`;
        const stderr = this.#stderr.trim();
        this.#stopped = new SigilError(`the Sigil module ${why}${stderr === "" ? "" : `\n${stderr}`}`, {
          help: "The instance can't be used again; load a new one with Sigil.load. This is a bug in Sigil: please report it with the output above.",
          cause: err,
        });
        throw this.#stopped;
      }
      throw err;
    }
  }
}
