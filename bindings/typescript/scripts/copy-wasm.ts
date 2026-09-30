// Copies the module `mise run wasm-build` built into dist/, where the
// package's "./sigil.wasm" export points.

import { copyFileSync, existsSync, mkdirSync } from "node:fs";

const from = new URL("../../../dist/wasm/sigil.wasm", import.meta.url);
const to = new URL("../dist/sigil.wasm", import.meta.url);

if (!existsSync(from)) {
  console.error(`${from.pathname} doesn't exist: build it first with mise run wasm-build`);
  process.exit(1);
}
mkdirSync(new URL(".", to), { recursive: true });
copyFileSync(from, to);
