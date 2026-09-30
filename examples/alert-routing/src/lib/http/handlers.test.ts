import { afterEach, beforeAll, describe, expect, spyOn, test } from "bun:test";
import { Policy, SigilStoppedError } from "@spechtlabs/sigil";

import { createService, type Service } from "@/server/service";
import { type Config, loadConfig } from "../config/config";
import type { Notification, Notifier } from "../dispatch/notifier";
import { PLATFORM_FILES, TEAM_FILES, TEAMS_YAML } from "../embedded";
import { directoryBundle, embeddedBundle } from "../store/bundle";
import { TeamDirectory } from "../teams/directory";
import { FakeClock, fakeTelemetry, type LogLine, teamSource, tempBundle, testWasm } from "../testing";
import { MAX_BODY_BYTES } from "./body";
import type { AlertResult, RouteResponse, WebhookResponse } from "./wire";

const ROOT = new URL("../../../", import.meta.url);

let wasm: WebAssembly.Module;
const cleanups: (() => Promise<unknown>)[] = [];

beforeAll(async () => {
  wasm = await testWasm();
});

afterEach(async () => {
  for (const c of cleanups.splice(0)) await c();
});

class RecordingNotifier implements Notifier {
  sent: Notification[] = [];
  failFor: ((n: Notification) => boolean) | undefined;
  async notify(n: Notification): Promise<void> {
    if (this.failFor?.(n) === true) throw new Error("the pager is down");
    this.sent.push(n);
  }
}

interface Env {
  service: Service;
  notifier: RecordingNotifier;
  clock: FakeClock;
  logs: LogLine[];
  metricsText: () => Promise<string>;
  request: (method: string, path: string, body?: unknown, init?: RequestInit) => Promise<Response>;
  bundle?: Awaited<ReturnType<typeof tempBundle>>;
}

async function newEnv(
  opts: {
    config?: Partial<Config>;
    checkout?: string;
    files?: Record<string, string>;
    start?: boolean;
    clock?: FakeClock;
  } = {},
): Promise<Env> {
  const { telemetry, logs, metricsText } = fakeTelemetry();
  const notifier = new RecordingNotifier();
  const clock = opts.clock ?? new FakeClock();
  let bundle: Env["bundle"];
  if (opts.checkout !== undefined || opts.files !== undefined) {
    bundle = await tempBundle();
    cleanups.push(bundle.cleanup);
    if (opts.checkout !== undefined) await bundle.write("checkout/alerts.sigil", opts.checkout);
    for (const [path, source] of Object.entries(opts.files ?? {})) await bundle.write(path, source);
  }
  const service = createService({
    config: { ...loadConfig({}), ...opts.config },
    telemetry,
    wasm,
    teams: TeamDirectory.parse(TEAMS_YAML, "embedded teams.yaml"),
    bundle: bundle === undefined ? embeddedBundle(TEAM_FILES) : directoryBundle(bundle.dir),
    platform: PLATFORM_FILES,
    clock,
    notifier,
  });
  cleanups.push(() => service.shutdown());
  if (opts.start !== false) await service.start();
  const request = (method: string, path: string, body?: unknown, init: RequestInit = {}) =>
    service.api.fetch(
      new Request(`http://alertrouter${path}`, {
        method,
        ...(body === undefined ? {} : { body: typeof body === "string" ? body : JSON.stringify(body) }),
        ...init,
      }),
    );
  return { service, notifier, clock, logs, metricsText, request, ...(bundle === undefined ? {} : { bundle }) };
}

async function fixture(name: string): Promise<unknown> {
  return Bun.file(new URL(`requests/${name}`, ROOT)).json();
}

/** A webhook of one firing alert per label set. */
function webhook(...alerts: Record<string, string>[]): unknown {
  return {
    version: "4",
    status: "firing",
    receiver: "alertrouter",
    alerts: alerts.map((labels, i) => ({
      status: "firing",
      labels,
      startsAt: "2026-01-01T00:00:00Z",
      fingerprint: labels.fingerprint ?? `fp-${i}`,
    })),
  };
}

const CRITICAL = { alertname: "CheckoutErrorRate", severity: "critical", team: "checkout", env: "production" };
const INFO = { alertname: "CheckoutPodRestarted", severity: "info", team: "checkout", env: "production" };

const CHECKOUT = teamSource("checkout");

// A team rule that pages someone else for the platform's reason: a conflict.
const CONFLICTING = `${CHECKOUT}\nwhen alert.severity == critical {\n  page(reason: critical_alert, target: "someone-else")\n}\n`;
// A list read past its end for every alert: a runtime error.
const FAILING = `${CHECKOUT}\nlet names = ["only"]\n\nwhen names[1] == alert.name {\n  drop(reason: muted)\n}\n`;
// Every alert must name its service: an input assert.
const ASSERTING_INPUT = `${CHECKOUT}\nassert("names_service", alert.labels["service"] != "")\n`;
// Never page: an outcome assert, which every page fails.
const ASSERTING_OUTCOME = `${CHECKOUT}\nassert("never_pages", page not in outcome)\n`;

type Case = {
  name: string;
  file: string;
  kind: "route" | "webhook";
  team?: string;
  status?: number;
  expect: Record<string, unknown> & { results?: Record<string, unknown>[] };
};

describe("the carried-over request cases", async () => {
  const cases = (await Bun.file(new URL("requests/cases.json", ROOT)).json()) as Case[];

  test.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const env = await newEnv();
    const body = await fixture(c.file);
    if (c.kind === "route") {
      const res = await env.request("POST", `/api/v1/teams/${c.team}/route`, body);
      expect(res.status).toBe(c.status ?? 200);
      const out = (await res.json()) as Record<string, unknown>;
      for (const [k, v] of Object.entries(c.expect)) expect(out[k]).toEqual(v);
      return;
    }
    const res = await env.request("POST", "/api/v1/alerts", body);
    expect(res.status).toBe(c.status ?? 200);
    const out = (await res.json()) as WebhookResponse;
    expect(out.received).toBe(c.expect.received as number);
    expect(out.routed).toBe(c.expect.routed as number);
    expect(out.results).toHaveLength(c.expect.results?.length ?? 0);
    for (const [i, want] of (c.expect.results ?? []).entries()) {
      const got = out.results[i] as unknown as Record<string, unknown>;
      for (const [k, v] of Object.entries(want)) expect(got[k]).toEqual(v);
    }
  });
});

describe("POST /api/v1/alerts", () => {
  test("answers in the webhook's order with the Go service's fields, and logs one line per firing alert", async () => {
    const env = await newEnv();
    const res = await env.request("POST", "/api/v1/alerts", await fixture("webhook-mixed.json"));
    const out = (await res.json()) as WebhookResponse;
    expect(Object.keys(out)).toEqual(["received", "routed", "results"]);
    expect(Object.keys(out.results[0] as object)).toEqual([
      "fingerprint",
      "alertname",
      "status",
      "team",
      "policy",
      "decision",
      "reason",
      "channel",
      "trace",
    ]);
    // No policy ran for the unowned and the invalid alert, so neither names one.
    expect(out.results.slice(2, 5).map((r) => "policy" in r)).toEqual([false, false, false]);
    expect(out.results[5]).toEqual({
      fingerprint: "07c9e4b2d5f31a68",
      alertname: "PaymentsLatencyHigh",
      status: "resolved",
    });

    // Evaluated alerts finish in the pool, after the ones decided at once,
    // so the lines come in the order the alerts finish, not the webhook's.
    const byFingerprint = (a: LogLine, b: LogLine) => String(a.fingerprint).localeCompare(String(b.fingerprint));
    const routed = env.logs.filter((l) => l.msg === "alert routed").sort(byFingerprint);
    expect(routed.map((l) => [l.fingerprint, l.status, l.level])).toEqual([
      ["1a7f4c2e9b6d3085", "routed", "info"],
      ["5d02b8e7f4a1c936", "routed", "info"],
      ["9e4c1f73a0b85d2e", "unowned", "info"],
      ["b3806e5d2c9f147a", "unowned", "info"],
      ["f8a2d61c0e7b4935", "invalid", "warn"],
    ]);
    expect(routed[2]).toMatchObject({ team: "-", team_label: "search", destination: "#alerts", policy: "" });
    expect(routed[0]).toMatchObject({ team: "payments", policy: "payments.alerts", destination: "#payments-ledger" });
    expect(typeof routed[0]?.took).toBe("string");

    const dispatched = [...env.notifier.sent].sort((a, b) => a.fingerprint.localeCompare(b.fingerprint));
    expect(dispatched).toHaveLength(5);
    expect(dispatched[2]).toMatchObject({ team: "-", decision: "notify", channel: "#alerts" });
    expect(dispatched[2]).not.toHaveProperty("policy");
    expect(dispatched[4]).not.toHaveProperty("policy");
    expect(dispatched[0]).toMatchObject({ policy: "payments.alerts" });

    const text = await env.metricsText();
    expect(text).toContain('alertrouter_alerts_received_total{status="firing"} 5');
    expect(text).toContain('alertrouter_alerts_received_total{status="resolved"} 1');
    expect(text).toContain('alertrouter_alerts_routed_total{team="-",outcome="unowned"} 2');
    expect(text).toContain('alertrouter_alerts_routed_total{team="payments",outcome="invalid"} 1');
    expect(text).toContain(
      'alertrouter_decisions_total{team="payments",policy="payments.alerts",decision="drop",reason="not_production"} 1',
    );
    expect(text).toContain("alertrouter_webhook_batch_size_count 1");
  });

  describe("a critical production alert still pages when its team's evaluation fails", () => {
    test.each([
      ["a conflict", CONFLICTING, {}, "conflict", 500],
      ["a runtime error", FAILING, {}, "runtime", 500],
      ["a failed input assert", ASSERTING_INPUT, {}, "assertion", 422],
      ["a failed outcome assert", ASSERTING_OUTCOME, {}, "assertion", 500],
    ] as const)("%s", async (_name, policy, _extra, kind, status) => {
      const env = await newEnv({ checkout: policy });

      const hook = (await (await env.request("POST", "/api/v1/alerts", webhook(CRITICAL))).json()) as WebhookResponse;
      const r = hook.results[0] as AlertResult;
      expect(r).toMatchObject({
        status: "failed",
        decision: "page",
        reason: "critical_alert",
        target: "checkout-primary",
      });
      expect(r).not.toHaveProperty("channel");
      expect(hook.routed).toBe(0);

      const res = await env.request("POST", "/api/v1/teams/checkout/route", {
        alert: { name: "CheckoutErrorRate", severity: "critical", labels: { env: "production" }, firing_for: "1m" },
      });
      expect(res.status).toBe(status);
      const one = (await res.json()) as RouteResponse;
      expect(one).toMatchObject({ decision: "page", reason: "critical_alert", target: "checkout-primary" });
      expect(one.error?.advice?.join(" ")).toContain("platform.paging's own page");

      expect(env.notifier.sent.map((n) => [n.decision, n.target])).toEqual([
        ["page", "checkout-primary"],
        ["page", "checkout-primary"],
      ]);
      expect(await env.metricsText()).toContain(
        `alertrouter_evaluation_errors_total{team="checkout",kind="${kind}"} 2`,
      );
      expect(await env.metricsText()).toContain('alertrouter_alerts_routed_total{team="checkout",outcome="failed"} 2');
    });

    test("an alert the platform doesn't page for gets the kind's default", async () => {
      const env = await newEnv({ checkout: FAILING });
      const hook = (await (await env.request("POST", "/api/v1/alerts", webhook(INFO))).json()) as WebhookResponse;
      expect(hook.results[0]).toMatchObject({
        status: "failed",
        decision: "notify",
        reason: "unrouted",
        channel: "#alerts",
      });
      expect(hook.results[0]?.error).toContain("checkout.alerts can't be evaluated against this alert");
    });

    test("the failure is explained in the result", async () => {
      const env = await newEnv({ checkout: CONFLICTING });
      const one = (await (
        await env.request("POST", "/api/v1/teams/checkout/route", {
          alert: { name: "X", severity: "critical", labels: { env: "production" }, firing_for: "1m" },
        })
      ).json()) as RouteResponse;
      expect(one.conflict?.candidates.map((c) => c.payload)).toEqual([
        { target: "checkout-primary" },
        { target: "someone-else" },
      ]);
      expect(one.error?.message).toContain("checkout.alerts produced decisions that can't stand together");
    });

    test("a failed assert lists the asserts", async () => {
      const env = await newEnv({ checkout: ASSERTING_INPUT });
      const one = (await (
        await env.request("POST", "/api/v1/teams/checkout/route", {
          alert: { name: "X", severity: "info", labels: { env: "production" }, firing_for: "1m" },
        })
      ).json()) as RouteResponse;
      expect(one.asserts).toEqual([
        {
          reason: "names_service",
          policy: "checkout.alerts",
          location: expect.stringContaining("checkout/alerts.sigil:"),
        },
      ]);
      expect(one.error?.message).toBe("the alert fails checkout.alerts's asserts: names_service");
    });
  });

  test("alerts not evaluated by the batch deadline go to the fallback, and a critical one still pages", async () => {
    // The clock jumps a second at its sixth reading, a few alerts into the
    // batch: past the evaluations' half of a 2 s batch, with the deliveries'
    // half still to go.
    const clock = new FakeClock();
    const env = await newEnv({ config: { batchTimeoutMs: 2_000 }, clock });
    let reads = 0;
    clock.now = () => {
      reads++;
      if (reads === 6) clock.t += 1_000;
      return clock.t;
    };
    const alerts = Array.from({ length: 20 }, (_, i) => ({ ...CRITICAL, fingerprint: `c${i}` }));
    const res = await env.request("POST", "/api/v1/alerts", webhook(...alerts));
    expect(res.status).toBe(200);
    const out = (await res.json()) as WebhookResponse;
    expect(out.results[0]?.status).toBe("routed");
    expect(out.results.at(-1)?.status).toBe("failed");
    expect(out.results.map((r) => r.status)).not.toContain("dispatch_failed");
    expect(env.notifier.sent).toHaveLength(20);
    const late = out.results.at(-1) as AlertResult;
    expect(late).toMatchObject({ decision: "page", reason: "critical_alert", target: "checkout-primary" });
    expect(late.error).toContain("batch deadline");
    expect(await env.metricsText()).toMatch(
      /alertrouter_evaluation_errors_total\{team="checkout",kind="timeout"\} [1-9]/,
    );
  });

  test("alerts past the webhook's limit go to the fallback, and the batch still answers 200", async () => {
    const env = await newEnv();
    const alerts = Array.from({ length: 1002 }, (_, i) => ({
      ...(i === 1001 ? CRITICAL : INFO),
      fingerprint: `i${i}`,
    }));
    const res = await env.request("POST", "/api/v1/alerts", webhook(...alerts));
    expect(res.status).toBe(200);
    const out = (await res.json()) as WebhookResponse;
    expect(out.received).toBe(1002);
    expect(out.results[999]?.status).toBe("routed");
    expect(out.results[1000]).toMatchObject({ status: "failed", decision: "notify", reason: "unrouted" });
    expect(out.results[1000]?.error).toContain("past the first 1000");
    // A critical alert past the limit still gets the platform's page.
    expect(out.results[1001]).toMatchObject({
      status: "failed",
      decision: "page",
      reason: "critical_alert",
      target: "checkout-primary",
    });
    expect(env.notifier.sent).toHaveLength(1002);
    expect(await env.metricsText()).toContain('alertrouter_alerts_truncated_total{by="alertrouter"} 2');
    // The overflow is logged once, and gets no line, span or history entry per alert.
    expect(env.logs.filter((l) => l.skipped === 2)).toHaveLength(1);
    expect(env.logs.filter((l) => l.msg === "alert routed")).toHaveLength(1000);
    expect(env.service.history.entries()).toHaveLength(500);
    expect(env.service.history.entries().every((e) => Number(String(e.result.fingerprint).slice(1)) < 1000)).toBe(true);
  });

  test("Alertmanager's own truncation is logged and counted", async () => {
    const env = await newEnv();
    const hook = { ...(webhook(INFO) as object), truncatedAlerts: 3 };
    expect((await env.request("POST", "/api/v1/alerts", hook)).status).toBe(200);
    expect(await env.metricsText()).toContain('alertrouter_alerts_truncated_total{by="alertmanager"} 3');
    expect(env.logs).toContainEqual(
      expect.objectContaining({ level: "warn", msg: "Alertmanager truncated the webhook" }),
    );
  });

  test("an alert with an unknown status is invalid instead of costing the batch", async () => {
    const env = await newEnv();
    const hook = webhook(CRITICAL, INFO) as { alerts: { status: string }[] };
    (hook.alerts[0] as { status: string }).status = "pending";
    const res = await env.request("POST", "/api/v1/alerts", hook);
    expect(res.status).toBe(200);
    const out = (await res.json()) as WebhookResponse;
    expect(out.results.map((r) => r.status)).toEqual(["invalid", "routed"]);
    expect(out.results[0]?.error).toBe(`alert 0 has the status "pending"`);
  });

  test("a redelivered webhook doesn't page twice", async () => {
    const env = await newEnv();
    const hook = webhook(CRITICAL, INFO);
    const first = (await (await env.request("POST", "/api/v1/alerts", hook)).json()) as WebhookResponse;
    const second = (await (await env.request("POST", "/api/v1/alerts", hook)).json()) as WebhookResponse;
    expect(second.results.map((r) => r.status)).toEqual(first.results.map((r) => r.status));
    expect(env.notifier.sent).toHaveLength(2);
    expect(await env.metricsText()).toContain('alertrouter_notifications_deduplicated_total{decision="page"} 1');
    expect(env.logs.filter((l) => l.msg === "alert routed" && l.deduplicated === true)).toHaveLength(2);
  });

  test("a page that fails to go out answers 503, so Alertmanager retries, and the retry goes out", async () => {
    const env = await newEnv();
    env.notifier.failFor = (n) => n.decision === "page";
    const res = await env.request("POST", "/api/v1/alerts", webhook(CRITICAL, INFO));
    expect(res.status).toBe(503);
    const out = (await res.json()) as WebhookResponse;
    expect(out.results.map((r) => r.status)).toEqual(["dispatch_failed", "routed"]);
    expect(out.routed).toBe(1);
    expect(out.results[0]?.error).toBe("dispatching the page to checkout-primary failed: the pager is down");
    expect(await env.metricsText()).toContain('alertrouter_notification_errors_total{decision="page"} 1');
    expect(env.logs).toContainEqual(
      expect.objectContaining({ msg: "alert routed", level: "error", status: "dispatch_failed" }),
    );

    env.notifier.failFor = undefined;
    const retry = await env.request("POST", "/api/v1/alerts", webhook(CRITICAL, INFO));
    expect(retry.status).toBe(200);
    expect(env.notifier.sent.map((n) => n.decision)).toEqual(["notify", "page"]);
  });

  test("a drop that fails to go out doesn't fail the batch", async () => {
    const env = await newEnv();
    env.notifier.failFor = (n) => n.decision === "drop";
    const res = await env.request("POST", "/api/v1/alerts", webhook({ ...CRITICAL, env: "staging" }));
    expect(res.status).toBe(200);
    expect(((await res.json()) as WebhookResponse).results[0]?.status).toBe("dispatch_failed");
  });

  test.each([
    ["not JSON", "{", 400, "the request body isn't a valid request"],
    ["empty", "", 400, "the request body is empty"],
    ["two values", "{}{}", 400, "the request body holds more than one JSON value"],
    ["another version", { version: "3", alerts: [] }, 400, `the webhook has version "3", not "4"`],
    [
      "over the size cap",
      "x".repeat(MAX_BODY_BYTES + 1),
      413,
      `the request body is larger than ${MAX_BODY_BYTES} bytes`,
    ],
  ])("refuses a body that is %s", async (_name, body, status, message) => {
    const env = await newEnv();
    const res = await env.request("POST", "/api/v1/alerts", body);
    expect(res.status).toBe(status);
    expect(((await res.json()) as { error: { message: string } }).error.message).toContain(message);
  });

  test("takes a body just under the cap", async () => {
    const env = await newEnv();
    const hook = webhook(INFO) as Record<string, unknown>;
    const padding = MAX_BODY_BYTES - JSON.stringify({ ...hook, pad: "" }).length;
    const res = await env.request("POST", "/api/v1/alerts", { ...hook, pad: "x".repeat(padding) });
    expect(res.status).toBe(200);
  });

  test("answers 503 until the first bundle loads", async () => {
    const env = await newEnv({ start: false });
    const res = await env.request("POST", "/api/v1/alerts", webhook(INFO));
    expect(res.status).toBe(503);
    expect(((await res.json()) as { error: { message: string } }).error.message).toBe("no policy bundle is loaded yet");
    expect(env.logs).toContainEqual(expect.objectContaining({ level: "error", msg: "request failed", status: 503 }));
  });

  test("a client that left gets 499 and nothing is routed", async () => {
    const env = await newEnv();
    const abort = new AbortController();
    abort.abort();
    const res = await env.request("POST", "/api/v1/alerts", webhook(CRITICAL), { signal: abort.signal });
    expect(res.status).toBe(499);
    expect(await res.text()).toBe("");
    expect(env.notifier.sent).toEqual([]);
  });
});

describe("POST /api/v1/teams/:team/route", () => {
  const OK = {
    alert: { name: "CheckoutLatencyHigh", severity: "warning", labels: { env: "production" }, firing_for: "12m" },
  };

  test("routes one alert and answers with the decision and its trace", async () => {
    const env = await newEnv();
    const res = await env.request("POST", "/api/v1/teams/checkout/route", OK);
    expect(res.status).toBe(200);
    const out = (await res.json()) as RouteResponse;
    expect(Object.keys(out)).toEqual(["team", "policy", "decision", "reason", "target", "trace"]);
    expect(out).toMatchObject({ team: "checkout", policy: "checkout.alerts", decision: "page", reason: "sustained" });
    expect(out.trace.filter((c) => c.winner)).toHaveLength(1);
    expect(out.trace[0]?.location).toBe("checkout/alerts.sigil:7:1 → platform/paging.sigil:12:3");
    expect(env.notifier.sent[0]).toMatchObject({ fingerprint: "", target: "checkout-primary" });
  });

  test.each([
    ["an unknown team", "/api/v1/teams/search/route", OK, 404, `team "search" isn't in the team directory`],
    [
      "an unknown field",
      "/api/v1/teams/checkout/route",
      { alert: { ...OK.alert, sevrity: "x" } },
      400,
      `json: unknown field "sevrity"`,
    ],
    [
      "an unknown top-level field",
      "/api/v1/teams/checkout/route",
      { ...OK, team: "x" },
      400,
      `json: unknown field "team"`,
    ],
    [
      "a numeric duration",
      "/api/v1/teams/checkout/route",
      { alert: { ...OK.alert, firing_for: 720 } },
      400,
      "the request body isn't a valid request",
    ],
    [
      "a duration that isn't one",
      "/api/v1/teams/checkout/route",
      { alert: { ...OK.alert, firing_for: "soon" } },
      400,
      "the request body isn't a valid request",
    ],
    [
      "a name that isn't a string",
      "/api/v1/teams/checkout/route",
      { alert: { ...OK.alert, name: 1 } },
      400,
      "alert.name isn't a string",
    ],
    ["no name", "/api/v1/teams/checkout/route", { alert: { ...OK.alert, name: "" } }, 422, "alert.name is empty"],
    [
      "an unknown severity",
      "/api/v1/teams/checkout/route",
      { alert: { ...OK.alert, severity: "urgent" } },
      422,
      `alert.severity "urgent" isn't a severity`,
    ],
    [
      "a negative firing time",
      "/api/v1/teams/checkout/route",
      { alert: { ...OK.alert, firing_for: "-5m" } },
      422,
      "alert.firing_for is negative: -5m",
    ],
  ])("refuses %s", async (_name, path, body, status, message) => {
    const env = await newEnv();
    const res = await env.request("POST", path, body);
    expect(res.status).toBe(status);
    expect(((await res.json()) as { error: { message: string } }).error.message).toContain(message);
    expect(env.notifier.sent).toEqual([]);
  });

  test("a duration error keeps its own advice as the cause", async () => {
    const env = await newEnv();
    const res = await env.request("POST", "/api/v1/teams/checkout/route", { alert: { ...OK.alert, firing_for: 720 } });
    const body = (await res.json()) as { error: { cause: { message: string } } };
    expect(body.error.cause.message).toBe("durations are strings, found 720");
  });

  test("takes Go's and Sigil's duration syntax", async () => {
    const env = await newEnv();
    for (const ff of ["1.5h", "2d", "600000ms", "0"]) {
      const res = await env.request("POST", "/api/v1/teams/checkout/route", { alert: { ...OK.alert, firing_for: ff } });
      expect(res.status).toBe(200);
    }
  });
});

describe("the administration endpoints", () => {
  test("GET /api/v1/policies describes the bundle that serves", async () => {
    const env = await newEnv();
    const res = await env.request("GET", "/api/v1/policies");
    expect(await res.json()).toEqual({
      kinds: [
        {
          kind: "AlertRouting",
          version: 1,
          loaded_at: "2026-01-01T00:12:00Z",
          source: "embedded",
          fingerprint: env.service.store.snapshot()?.fingerprint,
          policies: [
            { team: "checkout", policy: "checkout.alerts" },
            { team: "payments", policy: "payments.alerts" },
          ],
        },
      ],
    });
  });

  test("POST /api/v1/policies/reload loads the directory, or answers 500 and keeps the last good bundle", async () => {
    const env = await newEnv({ checkout: CHECKOUT });
    const before = env.service.store.snapshot()?.fingerprint;
    await env.bundle?.write("checkout/alerts.sigil", CHECKOUT.replace("10m", "20m"));
    const ok = await env.request("POST", "/api/v1/policies/reload");
    expect(ok.status).toBe(200);
    const after = ((await ok.json()) as { kinds: { fingerprint: string; source: string }[] }).kinds[0];
    expect(after?.fingerprint).not.toBe(before);
    expect(after?.source).toBe(env.bundle?.dir as string);

    await env.bundle?.write("checkout/alerts.sigil", CHECKOUT.replace("10m", "2h"));
    const bad = await env.request("POST", "/api/v1/policies/reload");
    expect(bad.status).toBe(500);
    const err = ((await bad.json()) as { error: { message: string; cause: { message: string } } }).error;
    expect(err.message).toContain("checkout.alerts failed to compile, so the previous bundle keeps serving");
    expect(err.cause.message).toContain("page_after: 2h is above the maximum 1h");
    expect(env.service.store.snapshot()?.fingerprint).toBe(after?.fingerprint as string);
    expect(env.logs).toContainEqual(expect.objectContaining({ msg: "request failed", status: 500 }));
  });

  test("GET /api/v1/policies/files serves what the server compiles, and the last reload error", async () => {
    const env = await newEnv({ checkout: CHECKOUT });
    await env.bundle?.write("payments/alerts.sigil", "broken");
    await env.request("POST", "/api/v1/policies/reload");
    const files = (await (await env.request("GET", "/api/v1/policies/files")).json()) as Record<string, unknown>;
    expect(files.kind).toEqual({
      path: "alert_routing.sigil",
      source: await Bun.file(new URL("policies/alert_routing.sigil", ROOT)).text(),
    });
    expect((files.platform as { path: string }[]).map((f) => f.path)).toEqual(PLATFORM_FILES.map((f) => f.path));
    expect((files.teams as { path: string }[]).map((f) => f.path)).toEqual([
      "checkout/alerts.sigil",
      "payments/alerts.sigil",
    ]);
    expect(files.required).toBe("platform.paging");
    expect(files.last_error).toMatchObject({
      trigger: "manual",
      error: { cause: { message: expect.stringContaining("payments/alerts.sigil:1:1") } },
    });
  });

  test("GET /api/v1/policies/:team/explain flattens the team's policy", async () => {
    const env = await newEnv();
    const res = await env.request("GET", "/api/v1/policies/checkout/explain");
    expect(res.status).toBe(200);
    const out = (await res.json()) as { policy: string; rules: { decision?: string }[] };
    expect(out.policy).toBe("checkout.alerts");
    expect(out.rules.map((r) => r.decision)).toContain("page");
    expect((await env.request("GET", "/api/v1/policies/search/explain")).status).toBe(404);
  });

  test("GET /api/v1/teams lists the directory", async () => {
    const env = await newEnv();
    expect(await (await env.request("GET", "/api/v1/teams")).json()).toEqual({
      teams: [
        { name: "checkout", oncall: "checkout-primary", channel: "#checkout-alerts" },
        { name: "payments", oncall: "payments-primary", channel: "#payments-alerts" },
      ],
    });
  });
});

describe("health, metrics and routing", () => {
  test("/healthz is ok at once; /readyz only once a bundle loaded", async () => {
    const env = await newEnv({ start: false });
    expect(await (await env.request("GET", "/healthz")).json()).toEqual({ status: "ok" });
    const notReady = await env.request("GET", "/readyz");
    expect(notReady.status).toBe(503);
    expect(await notReady.json()).toEqual({ status: "not ready" });
    await env.service.start();
    expect(await (await env.request("GET", "/readyz")).json()).toEqual({
      status: "ready",
      loaded_at: "2026-01-01T00:12:00Z",
    });
  });

  test("probes and scrapes aren't logged", async () => {
    const env = await newEnv();
    await env.request("GET", "/healthz");
    await env.request("GET", "/readyz");
    await env.request("GET", "/metrics");
    expect(env.logs.filter((l) => l.msg.startsWith("/"))).toEqual([]);
  });

  test("/metrics serves the registry, with request metrics by route template", async () => {
    const env = await newEnv();
    await env.request("POST", "/api/v1/teams/checkout/route", {
      alert: { name: "X", severity: "info", labels: {}, firing_for: "1m" },
    });
    await env.request("GET", "/nope");
    const res = await env.request("GET", "/metrics");
    expect(res.headers.get("content-type")).toContain("text/plain");
    const text = await res.text();
    expect(text).toContain('alertrouter_requests_total{code="200",method="POST",route="/api/v1/teams/:team/route"} 1');
    expect(text).toContain('alertrouter_requests_total{code="404",method="GET",route="unmatched"} 1');
    expect(text).not.toContain('route="/metrics"');
  });

  test.each([
    ["an unknown path", "GET", "/nope"],
    ["a method the route doesn't serve", "GET", "/api/v1/alerts"],
    ["another unknown API path", "DELETE", "/api/v1/teams"],
  ])("%s is the Go service's 404", async (_name, method, path) => {
    const env = await newEnv();
    const res = await env.request(method, path);
    expect(res.status).toBe(404);
    expect(await res.json()).toEqual({
      error: {
        message: `no route for ${method} ${path}`,
        advice: ["the API lives under /api/v1; see the README for the routes"],
      },
    });
  });
});

describe("the console's history", () => {
  test("GET /api/v1/history lists routed alerts with their notifications", async () => {
    const env = await newEnv();
    await env.request("POST", "/api/v1/alerts", webhook(CRITICAL, INFO));
    const { entries } = (await (await env.request("GET", "/api/v1/history")).json()) as {
      entries: {
        id: number;
        source: string;
        result: AlertResult;
        notification: { destination: string; status: string };
      }[];
    };
    // Entries are numbered in the order the alerts finished routing.
    expect(entries.map((e) => e.id)).toEqual([1, 2]);
    expect(entries.map((e) => [e.result.decision, e.notification.destination, e.notification.status]).sort()).toEqual([
      ["notify", "#alerts", "sent"],
      ["page", "checkout-primary", "sent"],
    ]);
    const after = (await (await env.request("GET", "/api/v1/history?after=1")).json()) as { entries: unknown[] };
    expect(after.entries).toHaveLength(1);
  });

  test("GET /api/v1/events replays what the client missed, streams what comes, and ends at shutdown", async () => {
    const env = await newEnv();
    await env.request("POST", "/api/v1/alerts", webhook(CRITICAL));
    const res = await env.request("GET", "/api/v1/events", undefined, { headers: { "last-event-id": "0" } });
    expect(res.headers.get("content-type")).toBe("text/event-stream; charset=utf-8");
    const reader = (res.body as ReadableStream<Uint8Array>).getReader();
    const decoder = new TextDecoder();
    let text = "";
    const readUntil = async (needle: string) => {
      while (!text.includes(needle)) {
        const { value, done } = await reader.read();
        if (done) return;
        text += decoder.decode(value);
      }
    };
    await readUntil("id: 1\n");
    await env.request("POST", "/api/v1/alerts", webhook(INFO));
    await readUntil("id: 2\n");
    await env.request("POST", "/api/v1/policies/reload");
    await readUntil("event: reload");
    expect(text).toContain("event: alert\ndata: ");
    expect(env.service.history.subscribers).toBe(1);

    await env.service.shutdown();
    for (;;) {
      const { done } = await reader.read();
      if (done) break;
    }
    expect(env.service.history.subscribers).toBe(0);
  });
});

describe("shutdown", () => {
  test("stops taking API requests, reports not ready, and releases the policies", async () => {
    const env = await newEnv();
    const snap = env.service.store.snapshot();
    expect(env.service.ready()).toBe(true);
    expect(await env.service.shutdown()).toBeUndefined();
    expect(env.service.ready()).toBe(false);
    expect(snap?.released).toBe(true);
    const res = await env.request("POST", "/api/v1/alerts", webhook(INFO));
    expect(res.status).toBe(503);
    expect(((await res.json()) as { error: { message: string } }).error.message).toBe("alertrouter is shutting down");
    expect((await env.request("GET", "/readyz")).status).toBe(503);
    expect(await env.service.shutdown()).toBeUndefined();
  });

  test("waits for requests in flight, up to the shutdown timeout", async () => {
    const env = await newEnv({ config: { shutdownTimeoutMs: 50 } });
    let release = () => {};
    const held = new Promise<void>((r) => {
      release = r;
    });
    env.notifier.failFor = () => false;
    const original = env.notifier.notify.bind(env.notifier);
    env.notifier.notify = async (n) => {
      await held;
      return original(n);
    };
    const inflight = env.request("POST", "/api/v1/alerts", webhook(INFO));
    await Bun.sleep(5);
    const err = await env.service.shutdown();
    expect(err?.message).toBe("in-flight requests didn't finish within 50ms");
    release();
    expect((await inflight).status).toBe(200);
  });
});

describe("the platform's page", () => {
  // A team rule that pages "nobody" for every warning with a reason that
  // outranks the platform's sustained page: it doesn't conflict, it wins.
  const OVERRIDING = `${CHECKOUT}\nwhen alert.severity == warning {\n  page(reason: critical_alert, target: "nobody")\n}\n`;
  const SUSTAINED_WARNING = {
    alert: { name: "CheckoutLatencyHigh", severity: "warning", labels: { env: "production" }, firing_for: "45m" },
  };

  test("replaces a team decision that doesn't page where the platform pages, as a guardrail violation", async () => {
    const env = await newEnv({ checkout: OVERRIDING });
    const res = await env.request("POST", "/api/v1/teams/checkout/route", SUSTAINED_WARNING);
    expect(res.status).toBe(500);
    const out = (await res.json()) as RouteResponse;
    expect(out).toMatchObject({
      decision: "page",
      reason: "sustained",
      target: "checkout-primary",
      policy: "checkout.alerts",
    });
    expect(out.error?.message).toBe(
      "checkout.alerts decided page(reason: critical_alert, target: nobody) for an alert platform.paging pages checkout-primary for (reason: sustained)",
    );
    expect(env.notifier.sent.map((n) => n.target)).toEqual(["checkout-primary"]);
    expect(await env.metricsText()).toContain('alertrouter_guardrail_violations_total{team="checkout"} 1');
    expect(env.logs).toContainEqual(
      expect.objectContaining({
        level: "error",
        msg: "guardrail violated: the platform's page replaced the team's decision",
        target: "nobody",
        platform_target: "checkout-primary",
      }),
    );
    expect(await env.metricsText()).not.toContain(
      'alertrouter_decisions_total{team="checkout",policy="checkout.alerts",decision="page",reason="critical_alert"}',
    );
  });

  test("catches a replaced sustained page between the team's page_after and the platform's default", async () => {
    // checkout pages warnings after 10m; the platform's default is 30m.
    const env = await newEnv({ checkout: OVERRIDING });
    const res = await env.request("POST", "/api/v1/teams/checkout/route", {
      alert: { ...SUSTAINED_WARNING.alert, firing_for: "12m" },
    });
    expect(res.status).toBe(500);
    expect(await res.json()).toMatchObject({ decision: "page", reason: "sustained", target: "checkout-primary" });
    expect(await env.metricsText()).toContain('alertrouter_guardrail_violations_total{team="checkout"} 1');
  });

  test("a team page to someone else for a warning platform.paging doesn't page yet isn't a violation", async () => {
    const env = await newEnv({ checkout: OVERRIDING });
    const res = await env.request("POST", "/api/v1/teams/checkout/route", {
      alert: { ...SUSTAINED_WARNING.alert, firing_for: "5m" },
    });
    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({ decision: "page", reason: "critical_alert", target: "nobody" });
  });

  test("leaves a team decision alone that pages the platform's target", async () => {
    const env = await newEnv();
    const res = await env.request("POST", "/api/v1/teams/checkout/route", SUSTAINED_WARNING);
    expect(res.status).toBe(200);
    expect(await env.metricsText()).not.toContain("alertrouter_guardrail_violations_total{");
  });

  test("an evaluation that succeeds doesn't need the platform engine", async () => {
    const env = await newEnv();
    const spy = spyOn(Policy.prototype, "eval");
    try {
      expect((await env.request("POST", "/api/v1/alerts", webhook(CRITICAL))).status).toBe(200);
      expect(spy).not.toHaveBeenCalled();
    } finally {
      spy.mockRestore();
    }
  });

  test("while the platform engine is down, an alert that needs it goes out with the fallback and the webhook answers 503", async () => {
    // A team policy that fails for every alert, so the platform engine decides.
    const env = await newEnv({ checkout: FAILING });
    // Breaks the in-process platform engine; team policies run in workers.
    const spy = spyOn(Policy.prototype, "eval").mockImplementation(() => {
      throw new SigilStoppedError("the Sigil module stopped: Maximum call stack size exceeded");
    });
    try {
      const res = await env.request("POST", "/api/v1/alerts", webhook(CRITICAL));
      expect(res.status).toBe(503);
      const out = (await res.json()) as WebhookResponse;
      expect(out.results[0]).toMatchObject({ status: "failed", decision: "notify", reason: "unrouted" });
      expect(out.results[0]?.error).toContain("checkout.alerts can't be evaluated against this alert");
      expect(out.results[0]?.error).not.toBeUndefined();
      expect(env.logs).toContainEqual(
        expect.objectContaining({
          msg: "alert routed",
          platform_error: expect.stringContaining("platform.paging couldn't be evaluated"),
        }),
      );
      expect(env.notifier.sent).toHaveLength(1);
      expect((await env.request("GET", "/readyz")).status).toBe(503);

      const one = await env.request("POST", "/api/v1/teams/checkout/route", {
        alert: { name: "X", severity: "critical", labels: { env: "production" }, firing_for: "1m" },
      });
      expect(one.status).toBe(503);
    } finally {
      spy.mockRestore();
    }
    await env.service.platform.recovered();
    expect((await env.request("GET", "/readyz")).status).toBe(200);
    expect((await env.request("POST", "/api/v1/alerts", webhook({ ...CRITICAL, fingerprint: "after" }))).status).toBe(
      200,
    );
  });

  test("an alert that can't be read, but whose team and severity can, still pages", async () => {
    const env = await newEnv();
    const hook = {
      version: "4",
      alerts: [{ status: "firing", labels: CRITICAL, startsAt: "sometime yesterday", fingerprint: "unreadable" }],
    };
    const out = (await (await env.request("POST", "/api/v1/alerts", hook)).json()) as WebhookResponse;
    expect(out.results[0]).toMatchObject({
      status: "invalid",
      decision: "page",
      reason: "critical_alert",
      target: "checkout-primary",
    });
    expect(out.results[0]?.error).toContain("isn't an RFC 3339 time");
  });

  test("an alert that started centuries ago fires for the longest duration Sigil writes", async () => {
    const env = await newEnv();
    const hook = {
      version: "4",
      alerts: [
        {
          status: "firing",
          labels: { ...CRITICAL, severity: "warning" },
          startsAt: "1700-01-01T00:00:00Z",
          fingerprint: "ancient",
        },
      ],
    };
    const out = (await (await env.request("POST", "/api/v1/alerts", hook)).json()) as WebhookResponse;
    expect(out.results[0]).toMatchObject({ status: "routed", decision: "page", reason: "sustained" });
  });
});

describe("delivery", () => {
  test("a delivery that outlasts the batch goes on; the answer is 503, and the retry is deduplicated", async () => {
    const env = await newEnv({ config: { batchTimeoutMs: 50 } });
    let release = () => {};
    const held = new Promise<void>((r) => {
      release = r;
    });
    const deliver = env.notifier.notify.bind(env.notifier);
    env.notifier.notify = async (n) => {
      await held;
      return deliver(n);
    };
    // The wall clock drives the timer the answer waits on.
    env.clock.now = () => Date.now();
    const res = await env.request("POST", "/api/v1/alerts", webhook(CRITICAL));
    expect(res.status).toBe(503);
    const out = (await res.json()) as WebhookResponse;
    expect(out.results[0]?.status).toBe("dispatch_failed");
    expect(out.results[0]?.error).toContain("hadn't finished when the batch's time ran out");

    release();
    await Bun.sleep(10);
    expect(env.notifier.sent).toHaveLength(1);
    const retry = await env.request("POST", "/api/v1/alerts", webhook(CRITICAL));
    expect(retry.status).toBe(200);
    expect(env.notifier.sent).toHaveLength(1);
  });
});
