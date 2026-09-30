// The policy gates a policy repository runs in CI, through @spechtlabs/sigil
// instead of the CLI: every document type-checks with the platform's paging
// required from its own trusted documents and the lints sigil.yaml sets,
// and every case of every `*_test.yaml` passes. `mise run policies` runs the
// stock CLI over the same files, so the two engines can't drift apart
// unnoticed.
import { describe, expect, test } from "bun:test";
import { join } from "node:path";
import { Sigil } from "@spechtlabs/sigil";
import { EXAMPLES_DIR } from "../fixture/requests";
import { displayPath, readConfig, readSources, readSuites, runCase } from "./suite";

const dir = join(EXAMPLES_DIR, "policies");
const sigil = await Sigil.load(import.meta.resolve("@spechtlabs/sigil/sigil.wasm"));
const files = readSources(dir);
const config = readConfig(dir);
const suites = readSuites(dir);

describe("The policies", () => {
  test("check clean against sigil.yaml's requirements and lints", () => {
    const diagnostics = sigil.check(files, { require: config.require, lints: config.lints });
    const errors = diagnostics
      .filter((d) => d.severity === "error")
      .map((d) => `${d.file}:${d.line}:${d.column}: ${d.message}`);
    expect(errors).toEqual([]);
  });

  test("have a test file for every team", () => {
    const teams = [...new Bun.Glob("teams/*/alerts.sigil").scanSync({ cwd: dir })].map((p) => p.split("/")[1]);
    const tested = suites.map((s) => s.file.split("/")[1]);
    expect(tested.sort()).toEqual(teams.sort());
    expect(teams.length).toBeGreaterThan(0);
  });
});

for (const suite of suites) {
  describe(displayPath(dir, suite), () => {
    test.each(suite.cases.map((c) => [c.name, c] as const))("%s", (_, c) => {
      expect(runCase(sigil, dir, files, suite, c)).toEqual([]);
    });
  });
}
