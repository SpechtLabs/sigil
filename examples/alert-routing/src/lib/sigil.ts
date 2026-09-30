// Loads the Sigil engine, @spechtlabs/sigil's WebAssembly module, from the
// files the package ships. The package stays outside Next's server bundle
// (serverExternalPackages), and every path here is resolved when the server
// runs, so it resolves the same in `next dev`, the standalone server and the
// tests.

import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import { Sigil, SigilStoppedError } from "@spechtlabs/sigil";

import { wrap } from "./errors";

/**
 * Resolves a file of the package from the working directory: the example's
 * root under `bun test` and `next dev`, and the standalone server's
 * directory, which server.js changes to, in the container. It goes through
 * process.getBuiltinModule so the bundler leaves the resolve to Node instead
 * of taking sigil.wasm for an import it should link, or the worker entry for
 * a chunk; next.config.ts traces both files into the standalone output.
 */
function resolvePackageFile(specifier: string): string {
  const { createRequire } = process.getBuiltinModule("node:module");
  return createRequire(join(process.cwd(), "package.json")).resolve(specifier);
}

/** The path of sigil.wasm in the installed package. */
export function wasmPath(): string {
  return resolvePackageFile("@spechtlabs/sigil/sigil.wasm");
}

/**
 * The path of the package's worker entry, which a worker_threads Worker runs.
 * It sits next to sigil.wasm; the package exports it for import only, which
 * a require-style resolve can't use.
 */
export function workerEntryPath(): string {
  return join(dirname(wasmPath()), "worker-entry.js");
}

/**
 * Reads and compiles sigil.wasm once. Every engine, the in-process one and
 * each worker's, instantiates this module instead of compiling the 11 MB
 * binary again, which also makes replacing a failed engine fast.
 */
export async function loadSigilModule(): Promise<WebAssembly.Module> {
  const path = wasmPath();
  let bytes: Uint8Array;
  try {
    bytes = await readFile(path);
  } catch (err) {
    throw wrap(
      err,
      `cannot read the Sigil engine at ${path}`,
      "build it with `mise run wasm-build` and `mise run ts-build` at the repository root, then `bun install --force` here",
    );
  }
  return WebAssembly.compile(bytes);
}

/**
 * Loads a new engine, from module when given. output receives what the
 * module writes to standard error, which it does only when something is
 * badly wrong.
 */
export async function loadSigil(output?: (line: string) => void, module?: WebAssembly.Module): Promise<Sigil> {
  return Sigil.load(
    module ?? (await loadSigilModule()),
    output === undefined ? {} : { output: (_stream, line) => output(line) },
  );
}

/**
 * Whether err says the engine itself failed rather than the request: the
 * module stopped, because a Go panic, a trap or a call that ran out of stack
 * unwound it mid-call, and every later call on it throws the same error. The
 * instance is gone and must be replaced. A SigilError with diagnostics, a
 * released policy or an input that doesn't fit the kind is the request's.
 */
export function isEngineFailure(err: unknown): boolean {
  // Across a worker the class may not survive, so the name counts too.
  return err instanceof SigilStoppedError || (err instanceof Error && err.name === "SigilStoppedError");
}

/** Whether sigil has stopped. */
export function hasStopped(sigil: Sigil): boolean {
  return sigil.stopped !== undefined;
}
