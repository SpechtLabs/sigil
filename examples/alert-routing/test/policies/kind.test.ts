// The kind file policies/alert_routing.sigil is exported from the kind the
// server compiles against (src/lib/routing/kind.ts, `bun run generate`).
// `sigil check`, `sigil test` and the browser preview read the file, so a
// kind changed in TypeScript and not exported again would check policies
// against a contract the server no longer has.
import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Sigil } from "@spechtlabs/sigil";
import { AlertRouting } from "@/lib/routing/kind";
import { EXAMPLES_DIR } from "../fixture/requests";

const KIND_FILE = join(EXAMPLES_DIR, "policies", "alert_routing.sigil");

describe("The AlertRouting kind file", () => {
  test("is the kind the server compiles against, byte for byte; run `bun run generate` when it isn't", () => {
    expect(readFileSync(KIND_FILE, "utf8")).toBe(AlertRouting.schema());
  });

  test("is a kind the engine accepts", async () => {
    const sigil = await Sigil.load(import.meta.resolve("@spechtlabs/sigil/sigil.wasm"));
    expect(AlertRouting.check(sigil)).toEqual([]);
  });
});
