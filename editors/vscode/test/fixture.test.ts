// The integration tests' fixture is the workspace the real language server
// will run against, so it has to be a project sigil check accepts as a
// whole, with its own sigil.yaml read the way the server reads it: from the
// fixture's directory.

import { expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { sigilCLI } from "./sigil-cli";

const sigil = sigilCLI();

test.skipIf(sigil === undefined)("the integration fixture checks clean, sigil.yaml included", () => {
  const fixture = join(import.meta.dir, "integration/fixture");
  let out = "";
  try {
    out = execFileSync(sigil ?? "", ["check", "."], { cwd: fixture, encoding: "utf8", stdio: "pipe" });
  } catch (err) {
    const e = err as { stdout?: string; stderr?: string };
    throw new Error(`${e.stdout ?? ""}${e.stderr ?? ""}`);
  }
  expect(out).toContain("no problems found");
});
