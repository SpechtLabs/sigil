// The worker helper's client side against an in-process fake worker: the
// protocol, deadlines, restarts and transparent recompiles, without the
// real module or a real worker.

import { describe, expect, test } from "bun:test";

import { SigilError, SigilStoppedError, SigilTimeoutError } from "../src/errors.js";
import type { CallMessage, Reply, Request } from "../src/protocol.js";
import { SigilWorker, type WorkerLike } from "../src/worker.js";

type Handler = (request: CallMessage, worker: FakeWorker) => unknown;

/** Answers each request on the next tick with what handler returns; a handler returning HANG never answers. */
class FakeWorker implements WorkerLike {
  static readonly HANG = Symbol("hang");
  readonly requests: Request[] = [];
  terminated = false;
  readonly #listeners: ((ev: { data: Reply }) => void)[] = [];

  constructor(readonly id: number, private readonly handler: Handler) {}

  addEventListener(type: string, listener: (ev: { data: Reply }) => void): void {
    if (type === "message") this.#listeners.push(listener);
  }

  postMessage(message: unknown): void {
    const request = message as Request;
    this.requests.push(request);
    setTimeout(() => {
      if (this.terminated) return;
      let reply: Reply;
      try {
        const value = request.method === "init" ? null : this.handler(request, this);
        if (value === FakeWorker.HANG) return;
        reply = { id: request.id, ok: true, value };
      } catch (err) {
        const e = err as SigilError;
        reply = { id: request.id, ok: false, error: { name: e.name, message: e.message, help: e.help, diagnostics: e.diagnostics ?? [] } };
      }
      for (const l of this.#listeners) l({ data: reply });
    }, 0);
  }

  terminate(): void {
    this.terminated = true;
  }
}

function harness(handler: Handler, options: { timeoutMs?: number } = {}) {
  const workers: FakeWorker[] = [];
  const sigil = new SigilWorker({
    wasm: "https://example.com/sigil.wasm",
    functions: "https://example.com/functions.js",
    ...options,
    worker: () => {
      const w = new FakeWorker(workers.length + 1, handler);
      workers.push(w);
      return w;
    },
  });
  return { sigil, workers };
}

const files = [{ path: "p.sigil", source: "policy p {}" }];

describe("SigilWorker", () => {
  test("starts one worker lazily and sends init first", async () => {
    const { sigil, workers } = harness(({ method }) => (method === "version" ? { version: "v1" } : null));
    expect(workers).toHaveLength(0);
    expect(await sigil.version()).toEqual({ version: "v1" } as never);
    await sigil.format("x");
    expect(workers).toHaveLength(1);
    expect(workers[0]?.requests.map((r) => r.method)).toEqual(["init", "version", "format"]);
    expect(workers[0]?.requests[0]).toMatchObject({
      method: "init",
      wasm: "https://example.com/sigil.wasm",
      functions: "https://example.com/functions.js",
    });
  });

  test("passes arguments through", async () => {
    const { sigil, workers } = harness(() => []);
    await sigil.check(files, { lints: { "unused-input": "error" } });
    expect(workers[0]?.requests[1]).toMatchObject({ method: "check", args: [files, { lints: { "unused-input": "error" } }] });
  });

  test("rebuilds a SigilError with its help and diagnostics", async () => {
    const diagnostics = [{ severity: "error" as const, message: "bad", file: "p.sigil", line: 1, column: 1 }];
    const { sigil } = harness(() => {
      throw new SigilError("p.sigil doesn't compile", { help: "Fix it.", diagnostics });
    });
    const err = await sigil.compile(files).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err).not.toBeInstanceOf(SigilTimeoutError);
    expect(err.message).toBe("p.sigil doesn't compile");
    expect(err.help).toBe("Fix it.");
    expect(err.diagnostics).toEqual(diagnostics);
  });

  test("terminates a worker that misses the deadline and starts a fresh one", async () => {
    let hang = true;
    const { sigil, workers } = harness(({ method }) => (method === "format" && hang ? FakeWorker.HANG : "ok"), {
      timeoutMs: 30,
    });
    const err = await sigil.format("x").catch((e) => e);
    expect(err).toBeInstanceOf(SigilTimeoutError);
    expect(err.message).toBe("the Sigil worker didn't answer format within 30 ms");
    expect(workers[0]?.terminated).toBe(true);
    hang = false;
    expect(await sigil.format("x")).toBe("ok");
    expect(workers).toHaveLength(2);
  });

  test("a per-call deadline overrides the helper's", async () => {
    const { sigil } = harness(() => FakeWorker.HANG, { timeoutMs: 60_000 });
    const err = await sigil.version({ timeoutMs: 10 }).catch((e) => e);
    expect(err).toBeInstanceOf(SigilTimeoutError);
  });

  test("fails the other calls waiting on a terminated worker", async () => {
    const { sigil } = harness(() => FakeWorker.HANG);
    const pending = sigil.format("x", {}, { timeoutMs: 60_000 });
    await Bun.sleep(5);
    sigil.terminate();
    const err = await pending.catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe("the Sigil worker was terminated");
  });

  test("an evaluation's own timeout gets a grace period before termination", async () => {
    let handle = 0;
    const { sigil, workers } = harness(({ method }) => {
      if (method === "compile") return { handle: ++handle, name: "p", diagnostics: [] };
      if (method === "eval") return FakeWorker.HANG;
      return null;
    });
    const policy = await sigil.compile(files);
    const start = performance.now();
    const err = await policy.eval({}, { timeoutMs: 20 }).catch((e) => e);
    expect(err).toBeInstanceOf(SigilTimeoutError);
    expect(performance.now() - start).toBeGreaterThanOrEqual(500);
    expect(workers[0]?.requests.at(-1)).toMatchObject({ method: "eval", args: [1, {}, { timeoutMs: 20 }] });
  });

  test("a worker whose instance stopped is replaced, and its policies compile again", async () => {
    let handle = 0;
    let stop = true;
    const { sigil, workers } = harness(({ method }) => {
      if (method === "compile") return { handle: ++handle, name: "p", diagnostics: [] };
      if (method === "eval" && stop) {
        stop = false;
        throw new SigilStoppedError("the Sigil module stopped: it ran out of stack: Maximum call stack size exceeded.");
      }
      return { policy: "p", outcome: [], trace: [] };
    });
    const policy = await sigil.compile(files);
    const err = await policy.eval({}).catch((e) => e);
    expect(err).toBeInstanceOf(SigilStoppedError);
    expect(err.message).toContain("ran out of stack");
    expect(workers[0]?.terminated).toBe(true);
    expect(await policy.eval({})).toEqual({ policy: "p", outcome: [], trace: [] });
    expect(workers).toHaveLength(2);
    expect(workers[1]?.requests.map((r) => r.method)).toEqual(["init", "compile", "eval"]);
  });

  test("a policy compiles again in the worker that replaced a timed-out one", async () => {
    let handle = 0;
    let hang = true;
    const { sigil, workers } = harness(
      ({ method }) => {
        if (method === "compile") return { handle: ++handle, name: "p", diagnostics: [] };
        if (method === "eval" && hang) return FakeWorker.HANG;
        if (method === "eval") return { policy: "p", outcome: [], trace: [] };
        if (method === "explainPolicy") return { policy: "p", policies: 1, modules: 0, rules: [] };
        return null;
      },
      { timeoutMs: 30 },
    );
    const policy = await sigil.compile(files, { policy: "p", functions: ["split"] });
    expect(policy.name).toBe("p");
    await policy.eval({}).catch(() => {});
    hang = false;
    expect(await policy.eval({ a: 1 })).toEqual({ policy: "p", outcome: [], trace: [] });
    expect(workers).toHaveLength(2);
    expect(workers[1]?.requests.map((r) => r.method)).toEqual(["init", "compile", "eval"]);
    expect(workers[1]?.requests[1]).toMatchObject({ args: [files, { policy: "p", functions: ["split"] }] });
    expect(workers[1]?.requests[2]).toMatchObject({ args: [2, { a: 1 }, {}] });
    expect(await policy.explain()).toMatchObject({ policy: "p" });
  });

  test("release frees the handle once, and a released policy can't be used", async () => {
    const { sigil, workers } = harness(({ method }) => (method === "compile" ? { handle: 5, name: "p", diagnostics: [] } : null));
    const policy = await sigil.compile(files);
    await policy.release();
    await policy.release();
    expect(workers[0]?.requests.filter((r) => r.method === "release")).toMatchObject([{ args: [5] }]);
    const err = await policy.eval({}).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe("policy p was released");
  });

  test("release after the worker was replaced sends nothing", async () => {
    const { sigil, workers } = harness(({ method }) => (method === "compile" ? { handle: 5, name: "p", diagnostics: [] } : null));
    const policy = await sigil.compile(files);
    sigil.terminate();
    await policy.release();
    expect(workers.flatMap((w) => w.requests).filter((r) => r.method === "release")).toHaveLength(0);
  });

  test("a worker that fails to start isn't kept", async () => {
    let attempts = 0;
    const sigil = new SigilWorker({
      wasm: new URL("https://example.com/sigil.wasm"),
      worker: () => {
        attempts++;
        if (attempts === 1) throw new Error("no workers here");
        return new FakeWorker(attempts, () => "ok");
      },
    });
    expect(await sigil.version().catch((e) => (e as Error).message)).toBe("no workers here");
    expect(await sigil.version()).toBe("ok" as never);
  });
});
