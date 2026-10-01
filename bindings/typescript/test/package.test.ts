// The package as a bundler sees it. worker-entry exists for what its top
// level does (it listens for the client's messages), so a bundler that
// took the package's word that nothing has side effects would drop it.

import { afterAll, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

const PACKAGE = join(import.meta.dir, "..", "package.json");
const dir = mkdtempSync(join(tmpdir(), "sigil-package-"));
afterAll(() => rmSync(dir, { recursive: true, force: true }));

describe("package.json", () => {
  test("keeps worker-entry when a bundler tree-shakes an import of it", async () => {
    // The package.json as published, with a worker-entry that only marks
    // that it ran, installed where a bundler resolves the package name.
    const pkg = join(dir, "node_modules", "@spechtlabs", "sigil");
    const manifest = JSON.parse(readFileSync(PACKAGE, "utf8")) as { exports: Record<string, { import?: string }> };
    const entry = join(pkg, manifest.exports["./worker-entry"]?.import ?? "");
    mkdirSync(dirname(entry), { recursive: true });
    writeFileSync(join(pkg, "package.json"), JSON.stringify(manifest));
    writeFileSync(entry, 'globalThis.sigilWorkerEntry = "listening";\n');
    writeFileSync(join(dir, "worker.js"), 'import "@spechtlabs/sigil/worker-entry";\n');

    const build = await Bun.build({ entrypoints: [join(dir, "worker.js")], target: "browser" });
    expect(build.success).toBe(true);
    expect(await build.outputs[0]?.text()).toContain('globalThis.sigilWorkerEntry = "listening"');
  });
});
