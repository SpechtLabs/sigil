import { describe, expect, test } from "bun:test";
import { BATCH_BUCKETS, EVALUATION_BUCKETS, METRIC_LABELS, PromMetrics, REQUEST_BUCKETS } from "./metrics";
import { NO_TEAM } from "./types";

/** The value of one series in the registry, or undefined when it doesn't exist. */
async function value(m: PromMetrics, name: string, labels: Record<string, string> = {}): Promise<number | undefined> {
  const metric = m.registry.getSingleMetric(name);
  if (!metric) throw new Error(`${name} isn't registered`);
  const { values } = await metric.get();
  const found = values.find(
    (v) =>
      Object.keys(labels).length === Object.keys(v.labels).length &&
      Object.entries(labels).every(([k, want]) => v.labels[k] === want),
  );
  return found?.value;
}

/** The label sets of every series of name, sorted, as strings. */
async function series(m: PromMetrics, name: string): Promise<string[]> {
  const metric = m.registry.getSingleMetric(name);
  if (!metric) throw new Error(`${name} isn't registered`);
  const { values } = await metric.get();
  return values.map((v) => JSON.stringify(Object.entries(v.labels).sort())).sort();
}

describe("the metric contract", () => {
  test("registers every alertrouter_* metric with exactly its contract labels, and nothing else under the prefix", async () => {
    const m = new PromMetrics();
    const registered = (await m.registry.getMetricsAsJSON())
      .map((j) => j.name)
      .filter((n) => n.startsWith("alertrouter_"));
    expect(registered.sort()).toEqual(Object.keys(METRIC_LABELS).sort());

    for (const [name, labels] of Object.entries(METRIC_LABELS)) {
      const metric = m.registry.getSingleMetric(name) as unknown as { labelNames: readonly string[] };
      expect([...metric.labelNames].sort()).toEqual([...labels].sort());
    }
  });

  test.each([
    ["alertrouter_evaluation_duration_seconds", EVALUATION_BUCKETS],
    ["alertrouter_webhook_batch_size", BATCH_BUCKETS],
    ["alertrouter_request_duration_seconds", REQUEST_BUCKETS],
  ])("%s has the Go service's buckets", async (name, buckets) => {
    const m = new PromMetrics();
    m.observeBatch(1);
    m.evaluationTimer("checkout")();
    m.requestTimer()("200", "GET", "/api/v1/teams");
    const text = (await m.render()).body;
    const les = [...text.matchAll(new RegExp(`^${name}_bucket\\{[^}]*le="([^"]+)"`, "gm"))].map((x) => x[1]);
    expect(les).toEqual([...buckets.map(String), "+Inf"]);
  });

  test("serves Node's runtime and process metrics next to the service's", async () => {
    const { contentType, body } = await new PromMetrics().render();
    expect(contentType).toStartWith("text/plain");
    for (const name of ["process_cpu_seconds_total", "process_resident_memory_bytes", "nodejs_heap_size_used_bytes"]) {
      expect(body).toContain(`# TYPE ${name} `);
    }
  });
});

// Follows the reload series through loads that succeed and fail. The time
// moves only on a success, the health gauge on every attempt, and the
// loaded-policy series only on a success, which replaces them.
describe("reload metrics", () => {
  const first = new Date(1_790_000_000_000);
  const later = new Date(first.getTime() + 3_600_000);
  const both = [
    { team: "checkout", policy: "checkout.alerts" },
    { team: "payments", policy: "payments.alerts" },
  ];
  const success =
    (at: Date, fingerprint: string, loaded: typeof both) =>
    (m: PromMetrics): void =>
      m.observeReloadSuccess(at, "embedded", fingerprint, loaded);
  const failure = (m: PromMetrics): void => m.observeReloadFailure();
  const loaded = (fingerprint: string, teams: typeof both) =>
    teams
      .map((t) =>
        JSON.stringify(Object.entries({ fingerprint, policy: t.policy, source: "embedded", team: t.team }).sort()),
      )
      .sort();

  test.each([
    ["prepared, nothing loaded yet", [], 0, 0, [], [0, 0]],
    [
      "a success stamps the time, marks the bundle healthy and lists its policies",
      [success(first, "aaa", both)],
      first.getTime() / 1000,
      1,
      loaded("aaa", both),
      [0, 1],
    ],
    ["a failure before any success", [failure], 0, 0, [], [1, 0]],
    [
      "a failure keeps the time and the policies of the last good load",
      [success(first, "aaa", both), failure],
      first.getTime() / 1000,
      0,
      loaded("aaa", both),
      [1, 1],
    ],
    [
      "a success replaces the policies, dropping a team and moving the fingerprint",
      [success(first, "aaa", both), failure, success(later, "bbb", both.slice(0, 1))],
      later.getTime() / 1000,
      1,
      loaded("bbb", both.slice(0, 1)),
      [1, 2],
    ],
  ] as const)("%s", async (_name, steps, wantAt, wantHealth, wantLoaded, [wantFailures, wantSuccesses]) => {
    const m = new PromMetrics();
    m.prepareReloads();
    for (const step of steps) step(m);

    expect(await value(m, "alertrouter_policy_last_reload_timestamp_seconds")).toBe(wantAt);
    expect(await value(m, "alertrouter_policy_last_reload_successful")).toBe(wantHealth);
    expect(await value(m, "alertrouter_policy_reloads_total", { result: "failure" })).toBe(wantFailures);
    expect(await value(m, "alertrouter_policy_reloads_total", { result: "success" })).toBe(wantSuccesses);
    expect(await series(m, "alertrouter_policy_loaded_info")).toEqual([...wantLoaded]);
  });

  test("prepareReloads keeps the counts of series that exist", async () => {
    const m = new PromMetrics();
    m.observeReloadFailure();
    m.prepareReloads();
    expect(await value(m, "alertrouter_policy_reloads_total", { result: "failure" })).toBe(1);
    expect(await value(m, "alertrouter_policy_reloads_total", { result: "success" })).toBe(0);
  });
});

// Each recorder lands on its own series with the labels the dashboard and
// the alerts query.
describe("routing metrics", () => {
  const m = new PromMetrics();
  m.observeBatch(3);
  m.observeReceived("firing");
  m.observeReceived("firing");
  m.observeReceived("resolved");
  m.observeRouted("checkout", "routed");
  m.observeRouted(NO_TEAM, "unowned");
  m.observeDecision("checkout", "checkout.alerts", "page", "sustained");
  m.observeEvaluationError("payments", "timeout");
  m.observeNotification("page", "checkout-primary");
  m.observeNotification("drop", "-");
  m.observeNotificationError("notify");
  m.observeDeduplicated("page");
  m.observeTruncated("alertrouter", 0);
  m.observeTruncated("alertrouter", 3);
  m.observeTruncated("alertmanager", 2);
  m.observeDedupEvicted(0);
  m.observeDedupEvicted(4);
  m.observeGuardrailViolation("checkout");
  m.observeEngineRestart("worker");
  m.observeEngineRestart("worker");
  m.setEngineUp("platform", false);
  m.setEngineUp("platform", true);
  m.observeStreamClosed("slow");
  const seconds = m.evaluationTimer("checkout")();
  m.requestTimer()("200", "POST", "/api/v1/alerts");

  test.each([
    ["firing received", "alertrouter_alerts_received_total", { status: "firing" }, 2],
    ["resolved received", "alertrouter_alerts_received_total", { status: "resolved" }, 1],
    ["routed", "alertrouter_alerts_routed_total", { team: "checkout", outcome: "routed" }, 1],
    ["unowned", "alertrouter_alerts_routed_total", { team: "-", outcome: "unowned" }, 1],
    [
      "decision",
      "alertrouter_decisions_total",
      { team: "checkout", policy: "checkout.alerts", decision: "page", reason: "sustained" },
      1,
    ],
    ["timeout", "alertrouter_evaluation_errors_total", { team: "payments", kind: "timeout" }, 1],
    ["page notification", "alertrouter_notifications_total", { decision: "page", destination: "checkout-primary" }, 1],
    ["drop notification", "alertrouter_notifications_total", { decision: "drop", destination: "-" }, 1],
    ["notification error", "alertrouter_notification_errors_total", { decision: "notify" }, 1],
    ["deduplicated", "alertrouter_notifications_deduplicated_total", { decision: "page" }, 1],
    ["truncated by alertrouter, zero ignored", "alertrouter_alerts_truncated_total", { by: "alertrouter" }, 3],
    ["truncated by alertmanager", "alertrouter_alerts_truncated_total", { by: "alertmanager" }, 2],
    ["dedup evictions, zero ignored", "alertrouter_notifications_dedup_evicted_total", {}, 4],
    ["guardrail violation", "alertrouter_guardrail_violations_total", { team: "checkout" }, 1],
    ["worker restarts", "alertrouter_engine_restarts_total", { engine: "worker" }, 2],
    ["platform engine back up", "alertrouter_engine_up", { engine: "platform" }, 1],
    ["slow stream closed", "alertrouter_event_streams_closed_total", { reason: "slow" }, 1],
    ["requests", "alertrouter_requests_total", { code: "200", method: "POST", route: "/api/v1/alerts" }, 1],
    ["batch count", "alertrouter_webhook_batch_size", {}, undefined],
  ])("%s", async (_name, name, labels, want) => {
    if (want === undefined) {
      // A histogram's series: one count, whatever the buckets say.
      expect((await m.render()).body).toContain(`${name}_count 1\n`);
      return;
    }
    expect(await value(m, name, labels)).toBe(want);
  });

  test("the evaluation timer records the seconds it returns", async () => {
    expect(seconds).toBeGreaterThanOrEqual(0);
    const body = (await m.render()).body;
    expect(body).toContain('alertrouter_evaluation_duration_seconds_count{team="checkout"} 1\n');
    expect(body).toContain('alertrouter_request_duration_seconds_count{method="POST",route="/api/v1/alerts"} 1\n');
  });
});
