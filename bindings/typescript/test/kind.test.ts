import { beforeAll, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

import { pathToFileURL } from "node:url";

import { type InputOf, Sigil } from "../src/index.js";
import { SigilWorker } from "../src/worker.js";
import { DEPLOY_GATES, HAVE_WASM, json, ROOT, sigilFiles, WASM } from "./fixtures.js";
import { Collecting, Everything, Minimal } from "./kinds/coverage.js";
import { AccessGrant, AlertRouting, Approve, DeployApproval, Deny, Deployer, Reader } from "./kinds/examples.js";

describe("schema() equals the kind files the Go hosts exported", () => {
  test.each([
    ["DeployApproval", DeployApproval, "deploy_approval.sigil"],
    ["AccessGrant", AccessGrant, "access_grant.sigil"],
  ] as const)("%s", (_name, kind, file) => {
    expect(kind.schema()).toBe(readFileSync(join(DEPLOY_GATES, file), "utf8"));
  });
});

describe("schema() equals Go's Kind.Schema for the same kind", () => {
  // What test/testdata/gokinds prints: the same kinds, built with
  // policy.NewKind.
  let golden: Record<string, string>;

  beforeAll(() => {
    const run = Bun.spawnSync(["go", "run", "./bindings/typescript/test/testdata/gokinds"], { cwd: ROOT, stderr: "pipe" });
    if (run.exitCode !== 0) throw new Error(`go run gokinds failed:\n${run.stderr.toString()}`);
    golden = JSON.parse(run.stdout.toString()) as Record<string, string>;
  }, 300_000);

  test.each([
    ["AlertRouting", AlertRouting],
    ["Everything", Everything],
    ["Collecting", Collecting],
    ["Minimal", Minimal],
  ] as const)("%s", (name, kind) => {
    expect(golden[name]).toBeString();
    expect(kind.schema()).toBe(golden[name] as string);
  });
});

describe.skipIf(!HAVE_WASM)("kinds with sigil.wasm", () => {
  let sigil: Sigil;
  const files = sigilFiles(DEPLOY_GATES);
  // Each kind's documents without its kind file. compile checks the whole
  // bundle, as Go's Kind.Load does, so a document of a kind nobody provides
  // fails it; each compile gets only its own kind's documents.
  const deployDocs = files.filter((f) => f.path.startsWith("platform/deploy/") || f.path.startsWith("teams/"));
  const accessDocs = files.filter((f) => f.path.startsWith("platform/access/") || f.path.startsWith("access/"));
  const sre = json(DEPLOY_GATES, "teams/payments/testdata/sre.json") as InputOf<typeof DeployApproval>;

  beforeAll(async () => {
    sigil = await Sigil.load(pathToFileURL(WASM));
  });

  test.each([
    ["AlertRouting", AlertRouting],
    ["DeployApproval", DeployApproval],
    ["AccessGrant", AccessGrant],
    ["Everything", Everything],
    ["Collecting", Collecting],
    ["Minimal", Minimal],
  ] as const)("the engine accepts %s's kind file", (_name, kind) => {
    expect(kind.check(sigil)).toEqual([]);
  });

  test("compile uses the kind's host functions and reads typed results", () => {
    using policy = DeployApproval.compile(sigil, files, { policy: "payments.production" });
    const res = policy.eval(sre);
    expect(res.error).toBeUndefined();
    expect(Approve.match(res)).toEqual({ bake: "15m" });
    expect(Approve.reason("payments_sre").is(res)).toBe(true);
    expect(Deny.match(res)).toBeUndefined();
  });

  test("compile adds the kind file when the files don't hold it", () => {
    using a = DeployApproval.compile(sigil, deployDocs, { policy: "payments.production" });
    using b = DeployApproval.compile(sigil, files, { policy: "payments.production" });
    expect(a.eval(sre)).toEqual(b.eval(sre));
  });

  test("a stale kind file fails the compile", () => {
    const stale = files.map((f) => (f.path === "deploy_approval.sigil" ? { ...f, source: f.source.replace("bake: duration = 1h", "bake: duration = 2h") } : f));
    expect(() => DeployApproval.compile(sigil, stale, { policy: "payments.production" })).toThrow(
      "deploy_approval.sigil declares kind DeployApproval, but not as this program defines it",
    );
  });

  test("options override the kind's host functions, and stubs replace both", () => {
    using overridden = DeployApproval.compile(sigil, files, {
      policy: "payments.production",
      functions: { split: () => ["ap"] },
    });
    expect(Deny.reason("no_rule_matched").is(overridden.eval(sre))).toBe(true);
    using stubbed = DeployApproval.compile(sigil, files, {
      policy: "payments.production",
      stubs: { split: { returns: ["eu", "us"] } },
    });
    expect(Approve.reason("payments_sre").is(stubbed.eval(sre))).toBe(true);
  });

  test("a collecting kind's results read with matchAll", () => {
    using policy = AccessGrant.compile(sigil, files, { policy: "access.main" });
    const res = policy.eval(json(DEPLOY_GATES, "access/testdata/team-member.json") as InputOf<typeof AccessGrant>);
    expect(Reader.matchAll(res).map((m) => m.reason)).toEqual(["team_member"]);
    expect(Deployer.matchAll(res)).toMatchObject([{ reason: "team_member", payload: { ttl: "8h" }, policy: "access.main" }]);
    expect(() => Reader.match(res)).toThrow("matchAll");
  });

  test("compile in the worker helper reads typed results too", async () => {
    const worker = new SigilWorker({
      wasm: pathToFileURL(WASM),
      functions: new URL("./host-functions.ts", import.meta.url),
      worker: () => new Worker(new URL("../src/worker-entry.ts", import.meta.url), { type: "module" }),
    });
    try {
      const policy = await DeployApproval.compile(worker, deployDocs, { policy: "payments.production", functions: ["split"] });
      const res = await policy.eval(sre);
      expect(Approve.match(res)).toEqual({ bake: "15m" });
      const access = await AccessGrant.compile(worker, accessDocs, { policy: "access.main" });
      const grants = await access.eval(json(DEPLOY_GATES, "access/testdata/team-member.json") as InputOf<typeof AccessGrant>);
      expect(() => Reader.match(grants)).toThrow("matchAll");
    } finally {
      worker.terminate();
    }
  });
});
