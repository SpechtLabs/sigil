// Builds the sigil CLI from this checkout, once per test run, for the tests
// that check what the extension ships against the real language. Without go
// on PATH it returns undefined and those tests skip.

import { execFileSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

/** The repository root, two levels above the extension. */
export const repo = join(import.meta.dir, "../../..");

let built: string | undefined | null = null;

/** The path of a sigil binary built from this checkout, or undefined without go. */
export function sigilCLI(): string | undefined {
  if (built !== null) return built;
  try {
    const out = join(mkdtempSync(join(tmpdir(), "sigil-cli-")), process.platform === "win32" ? "sigil.exe" : "sigil");
    execFileSync("go", ["build", "-o", out, "./cmd/sigil"], { cwd: repo, stdio: "pipe" });
    built = out;
  } catch {
    built = undefined;
  }
  return built;
}
