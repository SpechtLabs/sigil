// Shared helpers for the tests that run the real sigil.wasm: where it is,
// the repository's fixtures as virtual files, and the stock sigil CLI to
// compare answers with.

import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

import type { JsonValue, SourceFile } from "../src/types.js";

/** The repository root. */
export const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");

/** The module under test: $SIGIL_WASM, or what `mise run wasm-build` writes. */
export const WASM = process.env["SIGIL_WASM"] ?? join(ROOT, "dist", "wasm", "sigil.wasm");

/**
 * Whether the module exists. Locally a missing module skips the tests that
 * need it; in CI (which builds it first) it fails them instead, so a broken
 * build can't pass as a skip.
 */
export const HAVE_WASM = existsSync(WASM) || process.env["CI"] !== undefined;

/** The deploy-gates example's policies, as the virtual files `sigil` reads from there. */
export const DEPLOY_GATES = join(ROOT, "examples", "deploy-gates", "policies");

/** Every .sigil file below dir, with paths relative to it, in a stable order. */
export function sigilFiles(dir: string): SourceFile[] {
  const files: SourceFile[] = [];
  const walk = (d: string) => {
    for (const entry of readdirSync(d, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const path = join(d, entry.name);
      if (entry.isDirectory()) walk(path);
      else if (entry.name.endsWith(".sigil")) {
        files.push({ path: relative(dir, path).split(sep).join("/"), source: readFileSync(path, "utf8") });
      }
    }
  };
  walk(dir);
  return files;
}

/** Reads a JSON file below dir. */
export function json<T = Record<string, JsonValue>>(dir: string, path: string): T {
  return JSON.parse(readFileSync(join(dir, path), "utf8")) as T;
}

let cliPath: string | undefined;

/** Builds the stock sigil CLI once per test run, from this checkout. */
export function buildCli(): string {
  if (cliPath !== undefined) return cliPath;
  const out = join(mkdtempSync(join(tmpdir(), "sigil-cli-")), "sigil");
  const build = Bun.spawnSync(["go", "build", "-o", out, "./cmd/sigil"], { cwd: ROOT, stderr: "pipe" });
  if (build.exitCode !== 0) throw new Error(`go build ./cmd/sigil failed:\n${build.stderr.toString()}`);
  cliPath = out;
  return out;
}

/**
 * Runs the stock CLI with -o json in a fresh directory holding exactly
 * files (plus extra files, such as a sigil.yaml or an input), so its paths
 * are the virtual files' paths, and returns the parsed output.
 */
export function cli(files: SourceFile[], args: string[], extra: Record<string, string> = {}): unknown {
  const dir = mkdtempSync(join(tmpdir(), "sigil-parity-"));
  for (const { path, source } of [...files, ...Object.entries(extra).map(([path, source]) => ({ path, source }))]) {
    mkdirSync(dirname(join(dir, path)), { recursive: true });
    writeFileSync(join(dir, path), source);
  }
  // No configuration file is found above a temporary directory, so only
  // the one extra holds (if any) applies.
  const run = Bun.spawnSync([buildCli(), ...args, "-o", "json", ...files.map((f) => f.path)], {
    cwd: dir,
    stderr: "pipe",
    env: { ...process.env, NO_COLOR: "1" },
  });
  const stdout = run.stdout.toString();
  if (stdout.trim() === "") throw new Error(`sigil ${args.join(" ")} printed nothing:\n${run.stderr.toString()}`);
  return JSON.parse(stdout);
}
