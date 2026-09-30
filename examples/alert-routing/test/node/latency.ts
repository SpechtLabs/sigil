// Runs on Node (`node --test test/node/`), where the server runs: Bun and
// Node schedule the event loop differently, and this is the regression
// test for the loop staying free while team policies evaluate. A webhook of
// alerts for a team whose policy takes tens of milliseconds per alert used
// to block the thread that answers /readyz for the whole batch; with
// evaluations in the worker pool, probes keep answering within milliseconds.

import assert from "node:assert/strict";
import { test } from "node:test";

import type * as Harness from "./lib/harness";

// Node runs TypeScript only with the file's own extension in the specifier;
// a computed specifier keeps tsc from asking for allowImportingTsExtensions.
const { buildHarness } = (await import(
  new URL("./lib/build.ts", import.meta.url).href
)) as typeof import("./lib/build");
const h = (await import(buildHarness())) as typeof Harness;

/** A checkout policy that spends about 30 ms on every alert. */
function slowCheckout(): string {
  const names = Array.from({ length: 600 }, (_, i) => `"n${i}"`).join(", ");
  return `${h.teamSource("checkout")}\nlet names = [${names}]\n\nwhen all x in names: all y in names: x != alert.name {\n  notify(reason: routine, channel: "#slow")\n}\n`;
}

test("probes answer promptly while a batch of slow evaluations runs", async () => {
  const { telemetry } = h.fakeTelemetry();
  const files = h.TEAM_FILES.map((f) => (f.path === "checkout/alerts.sigil" ? { ...f, source: slowCheckout() } : f));
  const service = h.createService({
    config: { ...h.loadConfig({}), workers: 2, evaluationTimeoutMs: 5_000, batchTimeoutMs: 60_000 },
    telemetry,
    wasm: await h.testWasm(),
    teams: h.TeamDirectory.parse(h.TEAMS_YAML, "teams.yaml"),
    bundle: h.embeddedBundle(files),
    platform: h.PLATFORM_FILES,
  });
  try {
    await service.start();
    const alerts = Array.from({ length: 40 }, (_, i) => ({
      status: "firing",
      labels: { alertname: `Slow${i}`, severity: "info", team: "checkout", env: "production" },
      startsAt: new Date().toISOString(),
      fingerprint: `slow-${i}`,
    }));
    const started = performance.now();
    let done = false;
    const batch = service.api
      .fetch(new Request("http://x/api/v1/alerts", { method: "POST", body: JSON.stringify({ version: "4", alerts }) }))
      .finally(() => {
        done = true;
      });

    const latencies: number[] = [];
    while (!done) {
      const t = performance.now();
      const res = await service.api.fetch(new Request("http://x/readyz"));
      latencies.push(performance.now() - t);
      assert.equal(res.status, 200);
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    const res = await batch;
    const took = performance.now() - started;
    assert.equal(res.status, 200);
    const out = (await res.json()) as { results: { status: string; channel?: string }[] };
    assert.ok(out.results.every((r) => r.status === "routed" && r.channel === "#slow"));

    // The batch kept the workers busy for a while, and probes ran all along.
    assert.ok(took > 200, `the batch took ${Math.round(took)} ms; the policy isn't slow enough to tell`);
    assert.ok(latencies.length >= 5, `only ${latencies.length} probes ran during the batch`);
    const worst = Math.max(...latencies);
    assert.ok(worst < 100, `a probe took ${Math.round(worst)} ms during the batch`);
  } finally {
    await service.shutdown();
  }
});
