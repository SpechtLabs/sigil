// Writes policies/alert_routing.sigil from the AlertRouting kind defined in
// src/lib/routing/kind.ts: the TypeScript for `sigil export`. The kind file
// is what `sigil check`, `sigil test` and the browser preview read, so it
// must match the kind the server compiles against; kind.test.ts fails when
// it doesn't.

import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

import { AlertRouting } from "../src/lib/routing/kind";

writeFileSync(join(dirname(import.meta.dirname), "policies", AlertRouting.file().path), AlertRouting.schema());
