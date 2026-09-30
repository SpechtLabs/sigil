import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";

import { PLATFORM_FILES, TEAM_FILES } from "../embedded";
import { EvaluatorPool, type PooledPolicy, spawnWorker } from "../engine/pool";
import type { Input } from "../routing/kind";
import { FakeClock, fakeTelemetry, stoppedError, teamSource, tempBundle, testWasm } from "../testing";
import { type BundleSource, directoryBundle, embeddedBundle } from "./bundle";
import { PolicyStore, type Snapshot } from "./store";

let pool: EvaluatorPool;
const poolTelemetry = fakeTelemetry();
const cleanups: (() => Promise<void>)[] = [];

beforeAll(async () => {
  pool = new EvaluatorPool({
    module: await testWasm(),
    size: 2,
    spawn: spawnWorker,
    telemetry: poolTelemetry.telemetry,
    clock: new FakeClock(),
  });
  await pool.start();
});

afterAll(() => pool.close());

const decide = async (p: PooledPolicy | undefined, input: Input) =>
  (await pool.evaluate(input.team.name, p as PooledPolicy, input, { timeoutMs: 1_000 })).decision;

afterEach(async () => {
  for (const c of cleanups.splice(0)) await c();
});

function newStore(bundle: BundleSource, teams = ["checkout", "payments"]) {
  const t = fakeTelemetry();
  const clock = new FakeClock();
  const loads: { trigger: string; ok: boolean }[] = [];
  const store = new PolicyStore({
    pool,
    telemetry: t.telemetry,
    clock,
    teams,
    bundle,
    platform: PLATFORM_FILES,
    onLoad: (e) => loads.push({ trigger: e.trigger, ok: e.ok }),
  });
  return { store, clock, loads, ...t };
}

async function dirStore(teams?: string[]) {
  const bundle = await tempBundle();
  cleanups.push(bundle.cleanup);
  return { bundle, ...newStore(directoryBundle(bundle.dir), teams) };
}

const INPUT: Input = {
  alert: { name: "CheckoutErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "2m" },
  team: { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" },
};

describe("PolicyStore", () => {
  test("serves nothing before the first load", () => {
    const { store } = newStore(embeddedBundle(TEAM_FILES));
    expect(store.snapshot()).toBeUndefined();
    expect(store.acquire()).toBeUndefined();
  });

  test("loads one root per team, with platform.paging required, and reports the load", async () => {
    const { store, logs, metricsText, loads } = newStore(embeddedBundle(TEAM_FILES));
    await store.initialLoad();
    const snap = store.snapshot() as Snapshot;
    expect(snap.kind).toBe("AlertRouting");
    expect(snap.kindVersion).toBe(1);
    expect(snap.source).toBe("embedded");
    expect(snap.loadedAt.toISOString()).toBe("2026-01-01T00:12:00.000Z");
    expect(snap.policyNames()).toEqual(["checkout.alerts", "payments.alerts"]);
    expect(await decide(snap.policy("checkout"), INPUT)).toBe("page");
    expect(snap.policy("checkout")?.perWorker).toHaveLength(2);
    expect(snap.policy("search")).toBeUndefined();

    expect(logs).toContainEqual(
      expect.objectContaining({ level: "info", msg: "policy bundle loaded", trigger: "startup", source: "embedded" }),
    );
    const text = await metricsText();
    expect(text).toContain('alertrouter_policy_reloads_total{result="success"} 1');
    expect(text).toContain('alertrouter_policy_reloads_total{result="failure"} 0');
    expect(text).toContain("alertrouter_policy_last_reload_successful 1");
    expect(text).toContain(
      `alertrouter_policy_loaded_info{team="checkout",policy="checkout.alerts",fingerprint="${snap.fingerprint}",source="embedded"} 1`,
    );
    expect(loads).toEqual([{ trigger: "startup", ok: true }]);
  });

  test("a first load that fails throws, without a log line of its own, and serves nothing", async () => {
    const { store, bundle, logs, metricsText } = await dirStore();
    await bundle.write("checkout/alerts.sigil", "policy checkout.alerts: AlertRouting@1\n\nthis isn't sigil\n");
    const err = (await store.initialLoad().catch((e: unknown) => e)) as Error & { advice: string[]; cause: Error };
    expect(err.message).toBe(
      `the AlertRouting policies from ${bundle.dir} don't load: checkout.alerts failed to compile, and there is no earlier bundle to fall back to`,
    );
    expect(err.advice[0]).toBe("fix the diagnostics in the policies and reload");
    expect(err.cause.message).toContain("checkout/alerts.sigil:3:");
    expect(store.snapshot()).toBeUndefined();
    expect(logs.filter((l) => l.level === "error")).toEqual([]);
    expect(await metricsText()).toContain("alertrouter_policy_last_reload_successful 0");
  });

  describe("guardrails: platform.paging comes from the platform's documents", () => {
    const BROKEN: [string, Record<string, string>, RegExp][] = [
      [
        "a team policy that doesn't invoke platform.paging",
        { "checkout/alerts.sigil": teamSource("checkout").replace("paging(page_after: 10m)\n", "") },
        /checkout\.alerts doesn't invoke platform\.paging/,
      ],
      [
        "a team policy that gates platform.paging",
        {
          "checkout/alerts.sigil": teamSource("checkout").replace(
            "paging(page_after: 10m)",
            'when alert.labels["team"] == "checkout" {\n  paging(page_after: 10m)\n}',
          ),
        },
        /platform\.paging must be invoked unconditionally/,
      ],
      [
        "a team bundle that redefines platform.paging",
        {
          "checkout/paging.sigil":
            "policy platform.paging: AlertRouting@1\n\nparam page_after: duration = 30m\n\nwhen alert.severity == critical {\n  drop(reason: muted)\n}\n",
        },
        /policy platform\.paging is defined twice/,
      ],
      [
        "a team bundle that ships the platform's own file",
        { "platform/paging.sigil": "policy platform.paging: AlertRouting@1\n" },
        /platform\/paging\.sigil is among both the files and the trusted files/,
      ],
      [
        "a team policy that pages later than the platform allows",
        { "checkout/alerts.sigil": teamSource("checkout").replace("page_after: 10m", "page_after: 2h") },
        /page_after: 2h is above the maximum 1h/,
      ],
    ];

    test.each(BROKEN)("%s doesn't load at startup", async (_name, files, diag) => {
      const { store, bundle } = await dirStore();
      for (const [path, source] of Object.entries(files)) await bundle.write(path, source);
      const err = (await store.initialLoad().catch((e: unknown) => e)) as Error;
      expect(err).toBeInstanceOf(Error);
      expect(err.message).toContain("failed to compile");
      expect(String((err.cause as Error).message)).toMatch(diag);
      expect(store.snapshot()).toBeUndefined();
    });

    test.each(BROKEN)("%s doesn't replace the last good bundle", async (_name, files) => {
      const { store, bundle, logs } = await dirStore();
      await store.initialLoad();
      const good = store.snapshot();
      for (const [path, source] of Object.entries(files)) await bundle.write(path, source);
      await expect(store.load("manual")).rejects.toThrow(", so the previous bundle keeps serving");
      expect(store.snapshot()).toBe(good as Snapshot);
      expect(good?.released).toBe(false);
      expect(store.lastFailure()?.trigger).toBe("manual");
      expect(logs).toContainEqual(
        expect.objectContaining({
          level: "error",
          msg: "policy bundle rejected, the previous bundle keeps serving",
          trigger: "manual",
        }),
      );
    });
  });

  test("a failed reload keeps the loaded-policy series and marks the last reload failed", async () => {
    const { store, bundle, metricsText } = await dirStore();
    await store.initialLoad();
    const fp = store.snapshot()?.fingerprint;
    await bundle.write("payments/alerts.sigil", "broken");
    await store.load("sighup").catch(() => undefined);
    const text = await metricsText();
    expect(text).toContain('alertrouter_policy_reloads_total{result="failure"} 1');
    expect(text).toContain("alertrouter_policy_last_reload_successful 0");
    expect(text).toContain(`fingerprint="${fp}"`);
  });

  test("a good reload after a failure clears the failure", async () => {
    const { store, bundle } = await dirStore();
    await store.initialLoad();
    await bundle.write("payments/alerts.sigil", "broken");
    await store.load("manual").catch(() => undefined);
    expect(store.lastFailure()).toBeDefined();
    await bundle.write("payments/alerts.sigil", teamSource("payments"));
    await store.load("manual");
    expect(store.lastFailure()).toBeUndefined();
  });

  test("a directory that can't be read is a rejected load", async () => {
    const { store } = newStore(directoryBundle("/nonexistent/alertrouter/policies"));
    await expect(store.initialLoad()).rejects.toThrow(
      "reading the AlertRouting policies from /nonexistent/alertrouter/policies failed",
    );
  });

  test("a team in the directory without a policy in the bundle doesn't load", async () => {
    const { store } = newStore(embeddedBundle(TEAM_FILES), ["checkout", "search"]);
    await expect(store.initialLoad()).rejects.toThrow("search.alerts failed to compile");
  });

  test("no teams at all is nothing to load", async () => {
    const { store } = newStore(embeddedBundle(TEAM_FILES), []);
    await expect(store.initialLoad()).rejects.toThrow("no AlertRouting policy is configured");
  });

  describe("releasing replaced policies", () => {
    test("a reload releases the policies it replaced", async () => {
      const { store, bundle } = await dirStore();
      await store.initialLoad();
      const old = store.snapshot() as Snapshot;
      const oldPolicy = old.policies.get("checkout");
      await bundle.write("checkout/alerts.sigil", teamSource("checkout").replace("10m", "15m"));
      await store.load("manual");
      expect(store.snapshot()).not.toBe(old);
      expect(old.released).toBe(true);
      expect(oldPolicy?.released).toBe(true);
      expect(old.policy("checkout")).toBeUndefined();
    });

    test("a request that took the old bundle keeps it until it's done", async () => {
      const { store, bundle } = await dirStore();
      await store.initialLoad();
      const lease = store.acquire();
      if (lease === undefined) throw new Error("no lease");
      await bundle.write("checkout/alerts.sigil", teamSource("checkout").replace("10m", "15m"));
      await store.load("manual");
      expect(lease.snapshot.released).toBe(false);
      expect(await decide(lease.snapshot.policy("checkout"), INPUT)).toBe("page");
      lease.release();
      lease.release(); // twice is harmless
      expect(lease.snapshot.released).toBe(true);
    });

    test("`using` releases the lease", async () => {
      const { store } = newStore(embeddedBundle(TEAM_FILES));
      await store.initialLoad();
      let snap: Snapshot | undefined;
      {
        using lease = store.acquire();
        snap = lease?.snapshot;
      }
      store.close();
      expect(snap?.released).toBe(true);
    });

    test("a hundred reloads leave exactly one bundle's policies alive", async () => {
      const { store, bundle } = await dirStore();
      await store.initialLoad();
      const seen: Snapshot[] = [];
      for (let i = 0; i < 100; i++) {
        await bundle.write("checkout/alerts.sigil", teamSource("checkout").replace("10m", `${10 + (i % 20)}m`));
        await store.load("manual");
        seen.push(store.snapshot() as Snapshot);
      }
      const alive = seen.filter((s) => !s.released);
      expect(alive).toEqual([store.snapshot() as Snapshot]);
      const live = seen.flatMap((s) => [...s.policies.values()]).filter((p) => !p.released);
      expect(live).toHaveLength(2);
    });

    test("close releases everything, and nothing loads afterwards", async () => {
      const { store } = newStore(embeddedBundle(TEAM_FILES));
      await store.initialLoad();
      const snap = store.snapshot() as Snapshot;
      store.close();
      expect(snap.released).toBe(true);
      expect([...snap.policies.values()].every((p) => p.released)).toBe(true);
      expect(store.snapshot()).toBeUndefined();
      await expect(store.load("manual")).rejects.toThrow("the policy store is shut down");
    });
  });

  describe("polling", () => {
    test("reloads only when the bundle changed, and doesn't retry a broken bundle", async () => {
      const { store, bundle, loads } = await dirStore();
      await store.initialLoad();
      await store.reloadIfChanged();
      expect(loads.map((l) => l.trigger)).toEqual(["startup"]);

      await bundle.write("payments/alerts.sigil", "broken");
      await store.reloadIfChanged();
      await store.reloadIfChanged();
      expect(loads).toEqual([
        { trigger: "startup", ok: true },
        { trigger: "poll", ok: false },
      ]);

      await bundle.write("payments/alerts.sigil", teamSource("payments"));
      await store.reloadIfChanged();
      expect(loads.at(-1)).toEqual({ trigger: "poll", ok: true });
    });

    test("an unreadable directory is polled without throwing", async () => {
      const { store, bundle, loads } = await dirStore();
      await store.initialLoad();
      await bundle.cleanup();
      await store.reloadIfChanged();
      await store.reloadIfChanged();
      expect(loads.map((l) => l.ok)).toEqual([true, false]);
      expect(store.snapshot()).toBeDefined();
    });

    test("watch polls at the interval until stopped", async () => {
      const { store, bundle, loads } = await dirStore();
      await store.initialLoad();
      const stop = store.watch(10);
      await bundle.write("checkout/alerts.sigil", teamSource("checkout").replace("10m", "20m"));
      const until = Date.now() + 2_000;
      while (loads.length < 2 && Date.now() < until) await Bun.sleep(10);
      stop();
      expect(loads.at(-1)).toEqual({ trigger: "poll", ok: true });
    });

    test("an interval of 0 doesn't poll", () => {
      const { store } = newStore(embeddedBundle(TEAM_FILES));
      const stop = store.watch(0);
      stop();
    });
  });

  test("loads run one at a time, in order", async () => {
    const { store, loads } = newStore(embeddedBundle(TEAM_FILES));
    await Promise.all([store.initialLoad(), store.load("manual"), store.load("sighup")]);
    expect(loads.map((l) => l.trigger)).toEqual(["startup", "manual", "sighup"]);
  });
});

test("a broken document no root reaches still rejects the bundle, as in the Go service", async () => {
  const bundle = await tempBundle();
  cleanups.push(bundle.cleanup);
  const { store } = newStore(directoryBundle(bundle.dir));
  await store.initialLoad();
  await bundle.write(
    "broken.sigil",
    "policy broken.alerts: AlertRouting@1\n\nwhen alert.severity == {\n  drop(reason: muted)\n}\n",
  );
  const err = (await store.load("manual").catch((e: unknown) => e)) as Error;
  expect(err.message).toContain("checkout.alerts failed to compile, so the previous bundle keeps serving");
  expect((err.cause as Error).message).toContain("broken.sigil:3:21: `==` has no right operand");
});

test("a bundle that stops an evaluation engine is rejected, and the last good bundle keeps serving", async () => {
  // A pool whose compile stops the engine from the second load on, the way
  // the package reports a module that trapped.
  let loads = 0;
  const stopping = {
    compile: (...args: Parameters<EvaluatorPool["compile"]>) =>
      ++loads > 2 ? Promise.reject(stoppedError()) : pool.compile(...args),
  } as unknown as EvaluatorPool;
  const bundle = await tempBundle();
  cleanups.push(bundle.cleanup);
  const t = fakeTelemetry();
  const store = new PolicyStore({
    pool: stopping,
    telemetry: t.telemetry,
    clock: new FakeClock(),
    teams: ["checkout", "payments"],
    bundle: directoryBundle(bundle.dir),
    platform: PLATFORM_FILES,
  });
  await store.initialLoad();
  const good = store.snapshot() as Snapshot;
  await bundle.write("checkout/alerts.sigil", teamSource("checkout").replace("10m", "20m"));
  const err = (await store.load("manual").catch((e: unknown) => e)) as Error;
  expect(err.message).toContain("checkout.alerts failed to compile, so the previous bundle keeps serving");
  expect((err.cause as Error).message).toContain(
    "the bundle made the evaluation engine fail (the Sigil module stopped",
  );
  expect(store.snapshot()).toBe(good);
});

// A document nested deeper than any policy is written. Depending on the
// engine it stops the module compiling it, or, with the parser's nesting
// limit, gets a diagnostic; either way the bundle is rejected, and the one
// that served keeps serving from workers that may have been replaced.
const DEEP = `${teamSource("checkout")}\nwhen ${"(".repeat(200_000)}true${")".repeat(200_000)} {\n  drop(reason: muted)\n}\n`;

test("a deeply nested document is rejected whichever way the engine refuses it", async () => {
  const { store, bundle } = await dirStore();
  await store.initialLoad();
  const good = store.snapshot() as Snapshot;
  await bundle.write("checkout/alerts.sigil", DEEP);
  const err = (await store.load("manual").catch((e: unknown) => e)) as Error;
  expect(err.message).toContain("checkout.alerts failed to compile, so the previous bundle keeps serving");
  expect((err.cause as Error).message).toMatch(/the bundle made the evaluation engine fail|checkout\/alerts\.sigil:/);
  expect(store.snapshot()).toBe(good);
  expect(await decide(good.policy("checkout"), INPUT)).toBe("page");
});
