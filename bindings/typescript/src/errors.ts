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
