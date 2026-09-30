// Writes src/lib/embedded.ts: the platform's documents, the team bundle and
// the team directory bundled with the app, as string constants. It's the
// TypeScript for Go's //go:embed: the trusted platform documents are
// compiled into the server, out of reach of whoever can write to the
// mounted policies directory, and the defaults work with no files at all.
// `bun run generate` runs it; embedded.test.ts fails when the output is
// stale.

import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { renderEmbedded } from "../src/lib/embed-source";

const root = dirname(import.meta.dirname);
const target = join(root, "src/lib/embedded.ts");
writeFileSync(target, await renderEmbedded(root));
// Biome's formatting, so `biome ci` passes on the generated file as written.
const fmt = Bun.spawnSync(["bun", "x", "biome", "format", "--write", target], {
  cwd: root,
  stdio: ["ignore", "ignore", "inherit"],
});
if (fmt.exitCode !== 0) process.exit(fmt.exitCode ?? 1);
