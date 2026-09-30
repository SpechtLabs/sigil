import { afterEach, describe, expect, test } from "bun:test";
import type { EvalResult } from "@spechtlabs/sigil";
import { SigilStoppedError } from "@spechtlabs/sigil";
import type { WorkerPolicy } from "@spechtlabs/sigil/worker";

import { PLATFORM_FILES, TEAM_FILES } from "../embedded";
import type { Input } from "../routing/kind";
import { FakeClock, fakeTelemetry, testWasm } from "../testing";
import { EvaluatorPool, NotStartedError, PooledPolicy, spawnWorker } from "./pool";

const INPUT: Input = {
  alert: { name: "X", severity: "critical", labels: { env: "production" }, firing_for: "1m" },
  team: { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" },
};

const RESULT: EvalResult = { policy: "x", decision: "page", reason: "critical_alert", outcome: [], trace: [] };

const pools: EvaluatorPool[] = [];
afterEach(() => {
  for (const p of pools.splice(0)) p.close();
});

async function newPool(size: number) {
  const t = fakeTelemetry();
  const clock = new FakeClock();
  const pool = new EvaluatorPool({ module: await testWasm(), size, spawn: spawnWorker, telemetry: t.telemetry, clock });
  pools.push(pool);
  return { pool, clock, ...t };
}

/**
 * A policy whose evaluations in each worker the test controls: every call
 * is recorded with its worker, and waits until the test settles it.
 */
function heldPolicy(size: number) {
  const calls: { worker: number; settle: (r: EvalResult | Error) => void }[] = [];
  const perWorker = Array.from(
    { length: size },
    (_, worker) =>
      ({
        eval: () =>
          new Promise<EvalResult>((resolve, reject) => {
            calls.push({ worker, settle: (r) => (r instanceof Error ? reject(r) : resolve(r)) });
          }),
      }) as unknown as WorkerPolicy<Input>,
  );
  return { policy: new PooledPolicy("x.alerts", perWorker), calls };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 5));

describe("EvaluatorPool", () => {
  test("compiles a real team policy in every worker and evaluates it", async () => {
    const { pool } = await newPool(2);
    const policy = await pool.compile([...TEAM_FILES], {
      policy: "checkout.alerts",
      require: [{ policy: "platform.paging" }],
      trustedFiles: [...PLATFORM_FILES],
    });
    expect(policy.perWorker).toHaveLength(2);
    const results = await Promise.all(
      [1, 2, 3].map(() => pool.evaluate("checkout", policy, INPUT, { timeoutMs: 1_000 })),
    );
    expect(results.map((r) => r.decision)).toEqual(["page", "page", "page"]);
    expect((await policy.explain()).policy).toBe("checkout.alerts");
    await policy.release();
    expect(policy.released).toBe(true);
  });

  test("a bundle that doesn't compile is refused in every worker", async () => {
    const { pool } = await newPool(2);
    await expect(
      pool.compile([{ path: "checkout/alerts.sigil", source: "policy checkout.alerts: AlertRouting@1\n\nbroken\n" }], {
        policy: "checkout.alerts",
        require: [{ policy: "platform.paging" }],
        trustedFiles: [...PLATFORM_FILES],
      }),
    ).rejects.toThrow();
  });

  test("one team gets at most half the workers, so another team's alert still runs", async () => {
    const { pool } = await newPool(4);
    const { policy, calls } = heldPolicy(4);
    // Never settled: afterEach's close rejects the queued ones, which is fine.
    for (let i = 0; i < 5; i++) pool.evaluate("slow", policy, INPUT, { timeoutMs: 1_000 }).catch(() => {});
    await tick();
    expect(calls).toHaveLength(2); // half of four
    const fast = pool.evaluate("fast", policy, INPUT, { timeoutMs: 1_000 });
    await tick();
    expect(calls).toHaveLength(3);
    calls[2]?.settle(RESULT);
    expect((await fast).decision).toBe("page");
  });

  test("a queued task starts when a worker frees up", async () => {
    const { pool } = await newPool(2);
    const { policy, calls } = heldPolicy(2);
    const first = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    const second = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    await tick();
    expect(calls).toHaveLength(1);
    calls[0]?.settle(RESULT);
    await first;
    await tick();
    expect(calls).toHaveLength(2);
    calls[1]?.settle(RESULT);
    await second;
  });

  test("a task not started by its start deadline is refused, and the worker goes to the next", async () => {
    const { pool, clock } = await newPool(2);
    const { policy, calls } = heldPolicy(2);
    const busy = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    const late = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000, startBy: clock.now() + 10 });
    const other = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    await tick();
    clock.t += 20;
    calls[0]?.settle(RESULT);
    await busy;
    await expect(late).rejects.toBeInstanceOf(NotStartedError);
    await tick();
    expect(calls).toHaveLength(2);
    calls[1]?.settle(RESULT);
    await other;
  });

  test("an engine failure replaces the worker and fails that evaluation alone", async () => {
    const { pool, metricsText, logs } = await newPool(2);
    const { policy, calls } = heldPolicy(2);
    const failing = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    await tick();
    calls[0]?.settle(new SigilStoppedError("the Sigil module stopped: Maximum call stack size exceeded"));
    await expect(failing).rejects.toThrow("Maximum call stack size exceeded");
    expect(await metricsText()).toContain('alertrouter_engine_restarts_total{engine="worker"} 1');
    expect(logs).toContainEqual(expect.objectContaining({ msg: "an evaluation worker failed; starting a new one" }));
    const next = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    await tick();
    calls[1]?.settle(RESULT);
    expect((await next).decision).toBe("page");
  });

  test("closed, it refuses what's queued and what comes", async () => {
    const { pool } = await newPool(2);
    const { policy } = heldPolicy(2);
    pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 }).catch(() => {});
    const queued = pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 });
    pool.close();
    await expect(queued).rejects.toThrow("the evaluation pool is shut down");
    await expect(pool.evaluate("a", policy, INPUT, { timeoutMs: 1_000 })).rejects.toThrow("shut down");
  });
});
