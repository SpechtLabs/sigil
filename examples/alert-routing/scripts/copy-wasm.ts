// Copies @spechtlabs/sigil's browser half into public/sigil/: the worker
// helper, the worker entry, the modules they import, and sigil.wasm. The
// console's preview loads /sigil/worker.js as a plain ES module and the
// helper starts /sigil/worker-entry.js next to it, which fetches
// /sigil/sigil.wasm.
//
// Serving the files as they are, instead of bundling them, is deliberate:
// bun installs the file: dependency as symlinks out of the project, which
// Turbopack won't follow, and a worker plus a 10 MB module gain nothing from
// a bundler anyway. The server loads its own copy from node_modules.
import { copyFileSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";

const require = createRequire(import.meta.url);
const dist = dirname(require.resolve("@spechtlabs/sigil/sigil.wasm"));
const target = join(dirname(import.meta.dirname), "public", "sigil");

rmSync(target, { recursive: true, force: true });
mkdirSync(target, { recursive: true });
for (const name of readdirSync(dist)) {
  if (name === "sigil.wasm" || name.endsWith(".js") || name.endsWith(".js.map")) {
    copyFileSync(join(dist, name), join(target, name));
  }
}
