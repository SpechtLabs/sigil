import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { SpanStatusCode } from "@opentelemetry/api";
import { InMemorySpanExporter, type ReadableSpan } from "@opentelemetry/sdk-trace-base";
import type { EvalResult } from "@spechtlabs/sigil";

import { createService, type Service } from "@/server/service";
import { loadConfig } from "../config/config";
import { Dispatcher } from "../dispatch/dispatcher";
import { History } from "../dispatch/history";
import type { Notifier } from "../dispatch/notifier";
import { PLATFORM_FILES, TEAM_FILES, TEAMS_YAML } from "../embedded";
import { type EvaluatorPool, PooledPolicy } from "../engine/pool";
import type { Input } from "../routing/kind";
import { embeddedBundle } from "../store/bundle";
import { Snapshot } from "../store/store";
import { TeamDirectory } from "../teams/directory";
import { type ServiceTelemetry, setupTelemetry } from "../telemetry/telemetry";
import { FakeClock, testWasm } from "../testing";
import { AlertRouter } from "./route";
import type { WebhookResponse } from "./wire";

// These tests run with the real telemetry: spans go to an in-memory exporter
// and JSON log lines to a capture, so they check what Tempo and Loki get.

const exporter = new InMemorySpanExporter();
const raw: string[] = [];
let telemetry: ServiceTelemetry;
let service: Service;
const clock = new FakeClock();

beforeAll(async () => {
  telemetry = await setupTelemetry({
    env: {},
    version: "test",
    logFormat: "json",
    debug: false,
    logDestination: { write: (line: string) => raw.push(line) },
    spanExporter: exporter,
  });
  service = createService({
    config: loadConfig({}),
    telemetry,
    wasm: await testWasm(),
    teams: TeamDirectory.parse(TEAMS_YAML, "embedded teams.yaml"),
    bundle: embeddedBundle(TEAM_FILES),
    platform: PLATFORM_FILES,
    clock,
  });
  await service.start();
});

afterAll(async () => {
  await service.shutdown();
  await telemetry.shutdown();
});

beforeEach(() => {
  exporter.reset();
  raw.length = 0;
});

const lines = () => raw.map((l) => JSON.parse(l) as Record<string, unknown>);
const spans = (name: string) => exporter.getFinishedSpans().filter((s) => s.name === name);

// Each call gets fingerprints of its own, so the dispatcher never takes one
// test's alert for a redelivery of another's.
let batches = 0;
function hook(...alerts: Record<string, string>[]) {
  batches++;
  return JSON.stringify({
    version: "4",
    alerts: alerts.map((labels, i) => ({
      status: "firing",
      labels,
      startsAt: "2026-01-01T00:00:00Z",
      fingerprint: `b${batches}-${i}`,
    })),
  });
}

describe("the alertrouter.route span", () => {
  test("one per firing alert, under the request's server span, with the decision and one event per candidate", async () => {
    const res = await service.api.fetch(
      new Request("http://alertrouter/api/v1/alerts", {
        method: "POST",
        body: hook(
          { alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" },
          { alertname: "Unowned", severity: "info", team: "marketing" },
        ),
      }),
    );
    expect(res.status).toBe(200);

    const [server] = spans("POST /api/v1/alerts") as [ReadableSpan];
    const routes = spans("alertrouter.route");
    expect(routes).toHaveLength(2);
    for (const r of routes) {
      expect(r.spanContext().traceId).toBe(server.spanContext().traceId);
      expect(r.parentSpanContext?.spanId).toBe(server.spanContext().spanId);
    }

    const byName = (name: string) => routes.find((r) => r.attributes["alert.name"] === name) as ReadableSpan;
    const [paged, unowned] = [byName("CheckoutErrorRate"), byName("Unowned")];
    expect(paged.attributes).toMatchObject({
      "alert.name": "CheckoutErrorRate",
      "alert.severity": "critical",
      "alert.fingerprint": `b${batches}-0`,
      "alertrouter.team": "checkout",
      "alertrouter.outcome": "routed",
      "sigil.policy": "checkout.alerts",
      "sigil.decision": "page",
      "sigil.reason": "critical_alert",
      "sigil.candidates": 1,
    });
    expect(paged.attributes).not.toHaveProperty("alertrouter.team_label");
    expect(paged.events.map((e) => [e.name, e.attributes?.winner, e.attributes?.location])).toEqual([
      ["sigil.candidate", true, "checkout/alerts.sigil:7:1 → platform/paging.sigil:8:3"],
    ]);
    expect(paged.status.code).not.toBe(SpanStatusCode.ERROR);

    expect(unowned.attributes).toMatchObject({
      "alertrouter.team": "-",
      "alertrouter.team_label": "marketing",
      "alertrouter.outcome": "unowned",
    });
    expect(unowned.status.code).not.toBe(SpanStatusCode.ERROR);
  });

  test("the alert's log line carries its span's ids exactly once", async () => {
    await service.api.fetch(
      new Request("http://alertrouter/api/v1/alerts", {
        method: "POST",
        body: hook({ alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" }),
      }),
    );
    const [route] = spans("alertrouter.route") as [ReadableSpan];
    const routedRaw = raw.filter((l) => l.includes('"msg":"alert routed"'));
    expect(routedRaw).toHaveLength(1);
    const line = routedRaw[0] as string;
    expect(line.split('"trace_id":').length - 1).toBe(1);
    expect(line.split('"span_id":').length - 1).toBe(1);
    expect(JSON.parse(line)).toMatchObject({
      level: "info",
      msg: "alert routed",
      team: "checkout",
      alertname: "CheckoutErrorRate",
      fingerprint: `b${batches}-0`,
      status: "routed",
      policy: "checkout.alerts",
      decision: "page",
      reason: "critical_alert",
      destination: "checkout-primary",
      trace_id: route.spanContext().traceId,
      span_id: route.spanContext().spanId,
    });
    const dispatched = lines().find((l) => l.msg === "notification dispatched");
    expect(dispatched).toMatchObject({ span_id: route.spanContext().spanId, destination: "checkout-primary" });
    expect(route.attributes["alertrouter.destination"]).toBe("checkout-primary");
  });

  test("the access log line carries the server span's ids; probes get neither span nor line", async () => {
    await service.api.fetch(new Request("http://alertrouter/api/v1/teams"));
    await service.api.fetch(new Request("http://alertrouter/readyz"));
    const [server] = spans("GET /api/v1/teams") as [ReadableSpan];
    expect(lines()).toEqual([
      expect.objectContaining({
        msg: "/api/v1/teams",
        status: 200,
        method: "GET",
        trace_id: server.spanContext().traceId,
      }),
    ]);
    expect(spans("GET /readyz")).toEqual([]);
  });
});

describe("the alertrouter.policy.load span", () => {
  test("names the trigger, the source and the policies", async () => {
    await service.reload("manual");
    const [load] = spans("alertrouter.policy.load") as [ReadableSpan];
    expect(load.attributes).toMatchObject({
      "sigil.source": "embedded",
      "sigil.kind": "AlertRouting",
      "alertrouter.reload.trigger": "manual",
      "sigil.policies": ["checkout.alerts", "payments.alerts"],
      "sigil.fingerprint": service.store.snapshot()?.fingerprint,
    });
    expect(load.status.code).toBe(SpanStatusCode.OK);
    expect(lines()).toContainEqual(
      expect.objectContaining({ msg: "policy bundle loaded", trace_id: load.spanContext().traceId }),
    );
  });
});

describe("an evaluation past its timeout", () => {
  // Evaluations take microseconds, so a timeout is simulated: a stand-in
  // for checkout.alerts answers the way the module does when timeoutMs runs
  // out, with the kind's default and a canceled failure.
  const timedOut: EvalResult = {
    policy: "checkout.alerts",
    decision: "notify",
    reason: "unrouted",
    payload: { channel: "#alerts" },
    outcome: [{ decision: "notify", reason: "unrouted", payload: { channel: "#alerts" } }],
    trace: [],
    error: { kind: "canceled", message: "the evaluation was stopped: context deadline exceeded", help: "" },
  };
  const router = () => standIn(async () => timedOut);

  test.each([
    [
      "a critical production alert still pages",
      "critical",
      { decision: "page", reason: "critical_alert", target: "checkout-primary" },
    ],
    ["an info alert gets the kind's default", "info", { decision: "notify", reason: "unrouted", channel: "#alerts" }],
  ])("%s, with 503's classification", async (_name, severity, want) => {
    const { r, lease } = router();
    const team = service.teams.lookup("checkout");
    const routed = await r.route(lease, {
      name: "X",
      severity,
      fingerprint: "",
      teamLabel: "checkout",
      team,
      alert: { name: "X", severity: severity as "critical", labels: { env: "production" }, firing_for: "1m" },
      invalid: undefined,
      source: "route",
    });
    expect(routed.status).toBe("failed");
    expect(routed.failure?.status).toBe(503);
    expect(routed.failure?.kind).toBe("timeout");
    expect(routed.resp).toMatchObject(want);
    expect(routed.resp.error?.message).toBe("checkout.alerts wasn't decided within alertrouter's evaluation timeout");
    const [span] = spans("alertrouter.route") as [ReadableSpan];
    expect(span.status.code).toBe(SpanStatusCode.ERROR);
    expect(lines()).toContainEqual(
      expect.objectContaining({ msg: "alert routed", level: "error", error_kind: "timeout", http_status: 503 }),
    );
  });
});

test("the webhook response is the Go service's for the carried-over batch", async () => {
  const res = await service.api.fetch(
    new Request("http://alertrouter/api/v1/alerts", {
      method: "POST",
      body: await Bun.file(new URL("../../../requests/webhook-checkout.json", import.meta.url)).text(),
    }),
  );
  const out = (await res.json()) as WebhookResponse;
  expect(out.results.map((r) => [r.status, r.decision, r.reason])).toEqual([
    ["routed", "page", "critical_alert"],
    ["routed", "page", "sustained"],
    ["routed", "notify", "unrouted"],
    ["resolved", undefined, undefined],
  ]);
});

describe("a client that leaves during the evaluation", () => {
  test("gets nothing routed, and nothing counts as a decision or a failure", async () => {
    const counted = async () =>
      (
        (await telemetry.metrics.render()).body.match(/^alertrouter_(decisions|evaluation_errors)_total\{.*$/gm) ?? []
      ).join("\n");
    const before = await counted();
    const abort = new AbortController();
    // A stand-in for checkout.alerts whose evaluation outlasts the client.
    const sent: unknown[] = [];
    const { r, lease } = standIn(
      async (input) => {
        abort.abort();
        return service.pool.evaluate("checkout", service.store.snapshot()?.policy("checkout") as PooledPolicy, input, {
          timeoutMs: 1_000,
        });
      },
      { notify: async (n) => void sent.push(n) },
    );
    const routed = await r.route(
      lease,
      {
        name: "Leaving",
        severity: "critical",
        fingerprint: "",
        teamLabel: "checkout",
        team: service.teams.lookup("checkout"),
        alert: { name: "Leaving", severity: "critical", labels: { env: "production" }, firing_for: "1m" },
        invalid: undefined,
        source: "route",
      },
      { signal: abort.signal },
    );
    expect(routed.canceled).toBe(true);
    expect(routed.failure?.status).toBe(499);
    expect(sent).toEqual([]);
    const [span] = spans("alertrouter.route") as [ReadableSpan];
    expect(span.status.code).not.toBe(SpanStatusCode.ERROR);
    expect(span.events.map((e) => e.name)).toEqual(["exception"]);
    expect(lines()).toContainEqual(
      expect.objectContaining({
        level: "info",
        msg: "the client closed the request during the evaluation",
        http_status: 499,
      }),
    );
    expect(await counted()).toBe(before);
  });
});

test("the span of an alert no policy ran for names no policy", async () => {
  await service.api.fetch(
    new Request("http://alertrouter/api/v1/alerts", {
      method: "POST",
      body: hook({ alertname: "Unowned", severity: "info", team: "marketing" }),
    }),
  );
  const [span] = spans("alertrouter.route") as [ReadableSpan];
  expect(span.attributes).not.toHaveProperty("sigil.policy");
  expect(span.attributes["sigil.decision"]).toBe("notify");
});

/**
 * A router over a stand-in pool, whose evaluation of checkout.alerts the
 * test decides, and the service's platform engine.
 */
function standIn(evaluate: (input: Input) => Promise<EvalResult>, notifier: Notifier = { notify: async () => {} }) {
  const snap = new Snapshot(
    "embedded",
    "fp",
    new Date(clock.now()),
    [{ team: "checkout", policy: "checkout.alerts" }],
    [],
    new Map([["checkout", new PooledPolicy("checkout.alerts", [])]]),
  );
  const lease = { snapshot: snap, release() {}, [Symbol.dispose]() {} };
  const pool = {
    evaluate: (_team: string, _p: PooledPolicy, input: Input) => evaluate(input),
  } as unknown as EvaluatorPool;
  const r = new AlertRouter({
    store: service.store,
    teams: service.teams,
    pool,
    platform: service.platform,
    dispatcher: new Dispatcher({
      notifier,
      metrics: telemetry.metrics,
      logger: telemetry.logger,
      clock,
      dedupTtlMs: 0,
      dedupMaxEntries: 10,
      concurrency: 1,
      isKnownDestination: () => true,
    }),
    history: new History(10),
    telemetry,
    clock,
    evaluationTimeoutMs: 1_000,
  });
  return { r, lease };
}
