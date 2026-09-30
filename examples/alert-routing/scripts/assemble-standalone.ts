// Completes Next's standalone output in .next/standalone/ so it runs on its
// own, the way the Dockerfile lays out /app: the static assets and public/
// next to server.js, and the entry scripts from bin/ that set the port from
// ALERTROUTER_ADDR and start it. `bun run start` then runs
// .next/standalone/alertrouter.mjs from that directory, which is also where
// the service looks for sigil.wasm.
import { cpSync, existsSync, readdirSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dir, "..");
const standalone = join(root, ".next", "standalone");

if (!existsSync(join(standalone, "server.js"))) {
  throw new Error(`${standalone}/server.js is missing: run next build with output: "standalone" first`);
}

cpSync(join(root, ".next", "static"), join(standalone, ".next", "static"), { recursive: true });
cpSync(join(root, "public"), join(standalone, "public"), { recursive: true });
for (const name of readdirSync(join(root, "bin"))) {
  if (name.endsWith(".mjs")) cpSync(join(root, "bin", name), join(standalone, name));
}
