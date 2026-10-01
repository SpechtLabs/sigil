// The messages between the worker helper (worker.ts) and the worker it runs
// (worker-entry.ts). Both sides are this package, so the protocol is
// internal and may change with it.

import type { Diagnostic } from "./types.js";

/** Loads the module in the worker; the first message a worker gets. */
export interface InitMessage {
  id: number;
  method: "init";
  /** An absolute URL, the module's bytes, or a compiled module. */
  wasm: string | ArrayBuffer | WebAssembly.Module;
  /** An absolute URL of an ES module whose exports are host functions. */
  functions?: string | undefined;
}

/** Calls one method of the worker's Sigil instance or one of its policies. */
export interface CallMessage {
  id: number;
  method: "version" | "check" | "compile" | "explain" | "format" | "test" | "eval" | "explainPolicy" | "release";
  args: unknown[];
}

export type Request = InitMessage | CallMessage;

/** A SigilError, as it crosses to the client. */
export interface WireError {
  name: string;
  message: string;
  help?: string | undefined;
  diagnostics: Diagnostic[];
}

export type Reply = { id: number; ok: true; value: unknown } | { id: number; ok: false; error: WireError };
