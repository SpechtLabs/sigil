import { afterEach, describe, expect, spyOn, test } from "bun:test";
import { Policy, type Sigil, SigilStoppedError } from "@spechtlabs/sigil";

import { PLATFORM_FILES } from "../embedded";
import type { HumaneError } from "../errors";
import type { Input } from "../routing/kind";
import { loadSigil } from "../sigil";
import { fakeTelemetry, stopSigil, testWasm } from "../testing";
import { PlatformEngine } from "./platform";

const TEAM = { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" };

function input(severity: "critical" | "warning" | "info", firingFor: string, env = "production"): Input {
  return { alert: { name: "X", severity, labels: { env }, firing_for: firingFor }, team: TEAM };
}

const engines: PlatformEngine[] = [];
const restores: (() => void)[] = [];

afterEach(() => {
  for (const r of restores.splice(0)) r();
  for (const e of engines.splice(0)) e.close();
});

async function newEngine(load?: () => Promise<Sigil>) {
  const t = fakeTelemetry();
  const fatal: HumaneError[] = [];
  let loads = 0;
  const engine = new PlatformEngine({
    platform: PLATFORM_FILES,
    telemetry: t.telemetry,
    timeoutMs: 1_000,
    load: async () => {
      loads++;
      return (load ?? (async () => loadSigil(undefined, await testWasm())))();
    },
    onFatal: (err) => fatal.push(err),
  });
  engines.push(engine);
  await engine.start();
  return { engine, fatal, loads: () => loads, ...t };
}

// Makes every evaluation fail the way an engine does when a call runs out of
// stack inside Go code, until the returned function is called.
function breakEngines(): () => void {
  const spy = spyOn(Policy.prototype, "eval").mockImplementation(() => {
    throw new SigilStoppedError("the Sigil module stopped: Maximum call stack size exceeded");
  });
  const restore = () => spy.mockRestore();
  restores.push(restore);
  return restore;
}

describe("PlatformEngine", () => {
  test.each([
    [
      "a critical production alert",
      input("critical", "1m"),
      { kind: "page", reason: "critical_alert", target: "checkout-primary" },
    ],
    [
      "a critical alert without a known env",
      input("critical", "1m", "prod"),
      { kind: "page", reason: "critical_alert", target: "checkout-primary" },
    ],
    [
      "a warning past the platform's 30m",
      input("warning", "30m"),
      { kind: "page", reason: "sustained", target: "checkout-primary" },
    ],
    ["a warning before it, whatever the team's page_after", input("warning", "12m"), { kind: "none" }],
    ["an info alert", input("info", "5h"), { kind: "none" }],
    ["a staging alert", input("critical", "1m", "staging"), { kind: "none" }],
  ])("%s", async (_name, alert, want) => {
    const { engine } = await newEngine();
    expect(engine.page(alert)).toEqual(want as never);
  });

  test("an engine failure makes it unavailable and not up, then it replaces the instance", async () => {
    const { engine, loads, logs, metricsText } = await newEngine();
    expect(engine.up).toBe(true);
    const restore = breakEngines();

    const verdict = engine.page(input("critical", "1m"));
    expect(verdict.kind).toBe("unavailable");
    expect(verdict.kind === "unavailable" && verdict.error.message).toContain("Maximum call stack size exceeded");
    expect(engine.up).toBe(false);
    expect(engine.page(input("critical", "1m")).kind).toBe("unavailable");
    expect(logs).toContainEqual(
      expect.objectContaining({ level: "error", msg: "the platform engine failed; loading a new one" }),
    );
    expect(await metricsText()).toContain('alertrouter_engine_restarts_total{engine="platform"} 1');

    restore();
    await engine.recovered();
    expect(engine.up).toBe(true);
    expect(loads()).toBe(2);
    expect(engine.page(input("critical", "1m")).kind).toBe("page");
    expect(await metricsText()).toContain('alertrouter_engine_up{engine="platform"} 1');
  });

  test("gives up after three failed replacements and calls onFatal", async () => {
    let first = true;
    const { engine, fatal } = await newEngine(async () => {
      if (first) {
        first = false;
        return loadSigil(undefined, await testWasm());
      }
      throw new Error("no memory for another instance");
    });
    breakEngines();
    engine.page(input("critical", "1m"));
    await engine.recovered();
    expect(fatal).toHaveLength(1);
    expect(fatal[0]?.message).toContain("couldn't be replaced after 3 attempts: no memory for another instance");
    expect(engine.up).toBe(false);
  });

  test("an input the kind rejects is unavailable, but the engine stays up", async () => {
    const { engine } = await newEngine();
    const bad = {
      alert: { name: "X", severity: "urgent", labels: {}, firing_for: "1m" },
      team: TEAM,
    } as unknown as Input;
    expect(engine.page(bad).kind).toBe("unavailable");
    expect(engine.up).toBe(true);
  });

  test("closed, it answers unavailable", async () => {
    const { engine } = await newEngine();
    engine.close();
    expect(engine.up).toBe(false);
    expect(engine.page(input("critical", "1m")).kind).toBe("unavailable");
  });
});

test("probe finds an engine that broke and nothing noticed yet", async () => {
  const { engine } = await newEngine();
  expect(engine.probe()).toBe(true);
  const restore = breakEngines();
  expect(engine.probe()).toBe(false);
  restore();
  await engine.recovered();
  expect(engine.probe()).toBe(true);
});

test("an instance that stopped is found by the probe and replaced", async () => {
  let instance: Sigil | undefined;
  const { engine, metricsText } = await newEngine(async () => {
    instance = await loadSigil(undefined, await testWasm());
    return instance;
  });
  const first = instance as Sigil;
  stopSigil(first);
  expect(engine.probe()).toBe(false);
  await engine.recovered();
  expect(instance).not.toBe(first);
  expect(engine.probe()).toBe(true);
  expect(await metricsText()).toContain('alertrouter_engine_restarts_total{engine="platform"} 1');
});
