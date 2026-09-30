// Runs on Node (`node --test test/node/`), where the server runs, like
// latency.ts. One team whose policy is slow must not hold up another team:
// a team may occupy at most half of the evaluation pool, so while a batch of
// slow checkout evaluations runs, payments' alerts are decided by the free
// workers within milliseconds rather than queueing behind checkout's.

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

test("a slow team's batch doesn't hold up another team's alerts", async () => {
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
    let done = false;
    const batch = service.api
      .fetch(new Request("http://x/api/v1/alerts", { method: "POST", body: JSON.stringify({ version: "4", alerts }) }))
      .finally(() => {
        done = true;
      });

    // payments' critical alert, routed over and over while checkout's batch runs.
    const latencies: number[] = [];
    while (!done) {
      const t = performance.now();
      const res = await service.api.fetch(
        new Request("http://x/api/v1/teams/payments/route", {
          method: "POST",
          body: JSON.stringify({
            alert: { name: "PaymentsErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "1m" },
          }),
        }),
      );
      latencies.push(performance.now() - t);
      assert.equal(res.status, 200);
      const out = (await res.json()) as { decision: string; target?: string };
      assert.deepEqual([out.decision, out.target], ["page", "payments-primary"]);
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.equal((await batch).status, 200);

    assert.ok(latencies.length >= 5, `only ${latencies.length} payments alerts ran during checkout's batch`);
    const worst = Math.max(...latencies);
    // One checkout evaluation takes about 30 ms; payments waiting behind
    // checkout's queue would take seconds.
    assert.ok(worst < 100, `a payments alert took ${Math.round(worst)} ms during checkout's batch`);
  } finally {
    await service.shutdown();
  }
});
