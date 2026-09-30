// The worker helper with the real sigil.wasm in a real worker (Bun's Web
// Worker): answers equal the synchronous API's, host functions come from a
// module the worker imports, and a runaway evaluation is cut off.

import { beforeAll, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

import { Sigil } from "../src/index.js";
import { SigilError, SigilStoppedError, SigilTimeoutError, SigilWorker } from "../src/worker.js";
import type { InputOf } from "../src/index.js";
import { Minimal } from "./kinds/coverage.js";
import { Approve, DeployApproval } from "./kinds/examples.js";
import { DEPLOY_GATES, HAVE_WASM, json, sigilFiles, WASM } from "./fixtures.js";

const ENTRY = new URL("../src/worker-entry.ts", import.meta.url);
const FUNCTIONS = new URL("./host-functions.ts", import.meta.url);
const worker = () => new Worker(ENTRY, { type: "module" });

describe.skipIf(!HAVE_WASM)("SigilWorker with sigil.wasm", () => {
  const files = sigilFiles(DEPLOY_GATES);
  const input = json(DEPLOY_GATES, "teams/payments/testdata/sre.json");
  let direct: Sigil;

  beforeAll(async () => {
    direct = await Sigil.load(pathToFileURL(WASM));
  });

  test("answers like the synchronous API", async () => {
    const sigil = new SigilWorker({ wasm: pathToFileURL(WASM), functions: FUNCTIONS, worker });
    try {
      expect(await sigil.version()).toEqual(direct.version());
      expect(await sigil.check(files)).toEqual(direct.check(files));
      expect(await sigil.explain(files, { policy: "access.main" })).toEqual(direct.explain(files, { policy: "access.main" }));
      const source = files[0]?.source ?? "";
      expect(await sigil.format(source)).toBe(direct.format(source));

      const policy = await sigil.compile(files, { policy: "payments.production", functions: ["split"] });
      using expected = direct.compile(files, { policy: "payments.production", functions: { split: (s: string, sep: string) => s.split(sep) } });
      expect(policy.name).toBe("payments.production");
      expect(await policy.eval(input)).toEqual(expected.eval(input));
      expect(await policy.explain()).toEqual(expected.explain());
      await policy.release();
    } finally {
      sigil.terminate();
    }
  });

  test("loads from bytes and from a compiled module", async () => {
    const bytes = readFileSync(WASM);
    for (const wasm of [bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength), await WebAssembly.compile(bytes)]) {
      const sigil = new SigilWorker({ wasm, worker });
      try {
        expect((await sigil.version()).platform).toBe("wasip1/wasm");
      } finally {
        sigil.terminate();
      }
    }
  });

  test("errors keep their diagnostics across the worker", async () => {
    const sigil = new SigilWorker({ wasm: pathToFileURL(WASM), worker });
    try {
      const err = await sigil.format("policy x {", { path: "x.sigil" }).catch((e) => e);
      expect(err).toBeInstanceOf(SigilError);
      expect(err.diagnostics[0]?.file).toBe("x.sigil");
      const missing = await sigil.compile(files, { functions: ["split"] }).catch((e) => e);
      expect(missing).toBeInstanceOf(SigilError);
      expect(missing.message).toContain("functions module");
    } finally {
      sigil.terminate();
    }
  });

  test("a runaway evaluation is cut off, and the policy works again in a new worker", async () => {
    const sigil = new SigilWorker({ wasm: pathToFileURL(WASM), functions: FUNCTIONS, worker, timeoutMs: 2_000 });
    try {
      const policy = await sigil.compile(files, { policy: "payments.production", functions: ["split"] });
      const hang = structuredClone(input) as unknown as { service: { labels: Record<string, string> } };
      hang.service.labels["regions"] = "hang";
      const start = performance.now();
      const err = await policy.eval(hang as never, { timeoutMs: 50 }).catch((e) => e);
      expect(err).toBeInstanceOf(SigilTimeoutError);
      expect(performance.now() - start).toBeLessThan(1_500);
      expect(await policy.eval(input)).toEqual(direct.compile(files, { policy: "payments.production", stubs: { split: { returns: ["eu", "us"] } } }).eval(input));
    } finally {
      sigil.terminate();
    }
  }, 20_000);
});

describe.skipIf(!HAVE_WASM)("SigilWorker on a deeply nested policy", () => {
  test("rejects it with a diagnostic, or replaces the stopped worker", async () => {
    const deep = `policy deep.nesting: Minimal@1\n\nwhen ${"(".repeat(100_000)}true${")".repeat(100_000)} {\n  ok(reason: yes)\n}\n`;
    const sigil = new SigilWorker({ wasm: pathToFileURL(WASM), worker, timeoutMs: 20_000 });
    try {
      const err = await sigil.compile([Minimal.file(), { path: "deep.sigil", source: deep }]).catch((e) => e);
      expect(err).toBeInstanceOf(SigilError);
      if (!(err instanceof SigilStoppedError)) expect(err.diagnostics.length).toBeGreaterThan(0);
      // Whichever it was, the next call works: on the same worker, or a new one.
      expect((await sigil.version()).platform).toBe("wasip1/wasm");
    } finally {
      sigil.terminate();
    }
  }, 30_000);
});

describe.skipIf(!HAVE_WASM)("SigilWorker on worker_threads", () => {
  // A worker_threads Worker on Bun has the web scope's postMessage too, but
  // hears its parent only on parentPort; the helper must use the Node side.
  test("runs with a node:worker_threads Worker and a shared compiled module", async () => {
    const { Worker: ThreadWorker } = await import("node:worker_threads");
    const module = await WebAssembly.compile(readFileSync(WASM));
    const sigil = new SigilWorker({ wasm: module, worker: () => new ThreadWorker(ENTRY) });
    try {
      expect((await sigil.version()).platform).toBe("wasip1/wasm");
      const policy = await DeployApproval.compile(sigil, sigilFiles(DEPLOY_GATES), { policy: "payments.production", stubs: { split: { returns: ["eu", "us"] } } });
      expect(Approve.match(await policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json") as InputOf<typeof DeployApproval>))).toEqual({ bake: "15m" });
    } finally {
      sigil.terminate();
    }
  });
});
