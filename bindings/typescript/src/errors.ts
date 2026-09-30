import type { Diagnostic } from "./types.js";

/**
 * Why an operation failed: files that don't compile, a request the module
 * rejects, or a module that has stopped. `diagnostics` holds the errors and
 * warnings when there are any, the same records `sigil check -o json`
 * prints. An evaluation that fails at runtime is not a SigilError: it
 * returns a result whose `error` says why, like the CLI.
 */
export class SigilError extends Error {
  override readonly name: string = "SigilError";
  /** How to fix it, when the module knows. */
  readonly help: string | undefined;
  readonly diagnostics: Diagnostic[];

  constructor(message: string, options: { help?: string | undefined; diagnostics?: Diagnostic[] | undefined; cause?: unknown } = {}) {
    super(message, options.cause === undefined ? undefined : { cause: options.cause });
    this.help = options.help;
    this.diagnostics = options.diagnostics ?? [];
  }
}

/**
 * The worker helper's error for an operation that ran past its deadline. The
 * worker was terminated; the next call starts a fresh one.
 */
export class SigilTimeoutError extends SigilError {
  override readonly name: string = "SigilTimeoutError";
}

/**
 * The error of a module instance that has stopped: Go's runtime exited or
 * panicked, the module trapped, or an exception (such as running out of
 * stack on a deeply nested policy) unwound it mid-call. Go can't resume
 * after any of these, so every later call on the instance throws this same
 * error. Load a new instance with Sigil.load; the worker helper starts a
 * new worker by itself.
 */
export class SigilStoppedError extends SigilError {
  override readonly name: string = "SigilStoppedError";
}
