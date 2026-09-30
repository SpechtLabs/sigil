// Bundles test/node/lib/harness.ts for Node, with every package left to Node's
// resolution, into node_modules/.cache so the bundle finds the packages the
// way the service does.

import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");

/** Builds the harness and returns the bundle's path. */
export function buildHarness(): string {
  const out = join(root, "node_modules", ".cache", "alertrouter-node-test", "harness.mjs");
  execFileSync(
    "bun",
    ["build", "test/node/lib/harness.ts", "--target=node", "--format=esm", "--packages=external", `--outfile=${out}`],
    { cwd: root, stdio: ["ignore", "ignore", "inherit"] },
  );
  return out;
}
