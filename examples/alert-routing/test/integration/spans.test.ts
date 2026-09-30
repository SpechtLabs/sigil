import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { firingCount, mixedBatch } from "../fixture/cases";
import { expectStatus, PATH_HEALTHZ, PATH_METRICS, PATH_READYZ } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { expectBatch } from "../fixture/expect";
import {
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  DECISION_PAGE,
  firingAlert,
  firingFor,
  REASON_SUSTAINED,
  REASON_UNROUTED,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import { KIND_ROUTING } from "../fixture/wire";
import {
  attrs,
  CHECKOUT_RULES,
  closeEnvs,
  conflictingRule,
  EVENT_CANDIDATE,
  events,
  newEnv,
  ROUTE_TEMPLATE,
  releaseTelemetry,
  SPAN_LOAD,
  SPAN_ROUTE,
  TRIGGER_KEY,
} from "./env";

/** SpanStatusCode. */
const SPAN_OK = 1;
const SPAN_ERROR = 2;

afterEach(closeEnvs);
afterAll(releaseTelemetry);

describe("Traces", () => {
  describe("when one alert is routed", () => {
    async function routed() {
      const e = await newEnv();
      e.resetSpans(); // drop the startup load's span
      const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("12m")));
      expectStatus(a, 200);
      return { e, out: a.out, route: await e.waitForSpan(SPAN_ROUTE), server: await e.serverSpan() };
    }

    test("records the alert and the decision on an alertrouter.route span", async () => {
      const { out, route } = await routed();
      expect(attrs(route.attributes)).toMatchObject({
        "alert.name": CHECKOUT_LATENCY,
        "alert.severity": SEVERITY_WARNING,
        "alert.fingerprint": "",
        "alertrouter.team": TEAM_CHECKOUT,
        "sigil.policy": "checkout.alerts",
        "sigil.decision": DECISION_PAGE,
        "sigil.reason": REASON_SUSTAINED,
        "sigil.candidates": out.trace?.length,
      });
      expect(route.status.code).not.toBe(SPAN_ERROR);
    });

    test("adds one sigil.candidate event per trace entry, in trace order", async () => {
      const { out, route } = await routed();
      expect(out.trace).toHaveLength(2);
      expect(events(route, EVENT_CANDIDATE)).toEqual(
        (out.trace ?? []).map((c) => ({
          decision: c.decision,
          reason: c.reason,
          policy: c.policy,
          location: c.location,
          winner: c.winner,
        })),
      );
    });

    test("runs under the HTTP server span, named after the route template", async () => {
      const { route, server } = await routed();
      expect(server.name).toBe(`POST ${ROUTE_TEMPLATE}`);
      expect(route.parentSpanContext?.spanId).toBe(server.spanContext().spanId);
      expect(route.spanContext().traceId).toBe(server.spanContext().traceId);
    });
  });

  test("records one route span per firing alert of a webhook, under the request's span", async () => {
    const e = await newEnv();
    e.resetSpans();
    const batch = mixedBatch(new Date());
    const a = await e.client.webhook(batch.webhook);
    expectBatch(batch, a, a.out);

    const server = await e.serverSpan();
    expect(server.name).toBe("POST /api/v1/alerts");

    const routes = await eventually(() => {
      const spans = e.spansNamed(SPAN_ROUTE);
      expect(spans).toHaveLength(firingCount(batch));
      return spans;
    });

    const byFingerprint = new Map<string, Record<string, unknown>>();
    for (const s of routes) {
      expect(s.parentSpanContext?.spanId).toBe(server.spanContext().spanId);
      const a = attrs(s.attributes);
      byFingerprint.set(a["alert.fingerprint"] as string, a);
      // An unowned or invalid alert was routed as designed, so its span
      // isn't marked as failed.
      expect(s.status.code, `span of ${a["alert.fingerprint"]}`).not.toBe(SPAN_ERROR);
    }
    expect(byFingerprint.has("checkout-resolved")).toBe(false);
    expect(byFingerprint.get("checkout-critical")).toMatchObject({
      "sigil.decision": DECISION_PAGE,
      "alertrouter.team": TEAM_CHECKOUT,
    });
    // No team owns the alert, whatever its label says, and the label is the
    // alert rule's choice, so the span names no team either: a search by
    // team finds only alerts that team owns. The label it carried is kept
    // apart.
    expect(byFingerprint.get("unknown-team")).toMatchObject({
      "alertrouter.team": "-",
      "alertrouter.team_label": "marketing",
      "sigil.reason": REASON_UNROUTED,
    });
    expect(byFingerprint.get("bad-severity")).toMatchObject({ "alert.severity": "urgent" });
    // No policy ran for an unowned or invalid alert, so its span names none;
    // one that did run names it.
    for (const fp of ["no-team", "unknown-team", "bad-severity", "no-name"]) {
      expect(byFingerprint.get(fp), fp).not.toHaveProperty(["sigil.policy"]);
    }
    expect(byFingerprint.get("checkout-critical")?.["sigil.policy"]).toBe("checkout.alerts");
  });

  test("marks the route span of a failed evaluation as an error", async () => {
    const e = await newEnv();
    e.editCheckout(CHECKOUT_RULES, conflictingRule);
    await e.reloadOK();
    e.resetSpans();

    expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING)), 500);

    const span = await e.waitForSpan(SPAN_ROUTE);
    expect(span.status.code).toBe(SPAN_ERROR);
    expect(span.events.map((ev) => ev.name)).toContain("exception");
    expect(attrs(span.attributes)["sigil.reason"]).toBe(REASON_UNROUTED);
    expect((await e.serverSpan()).status.code).toBe(SPAN_ERROR);
  });

  test("doesn't trace probes and scrapes", async () => {
    const e = await newEnv();
    e.resetSpans();
    for (const path of [PATH_HEALTHZ, PATH_READYZ, PATH_METRICS]) expectStatus(await e.client.get(path), 200);

    // A traced request afterwards gives the probes' spans, if there were
    // any, time to arrive: once its two spans are in, the exporter holds
    // nothing else.
    expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);
    await e.waitForSpan(SPAN_ROUTE);
    await e.serverSpan();
    expect(e.spans().map((s) => s.name)).toEqual([SPAN_ROUTE, `POST ${ROUTE_TEMPLATE}`]);
  });

  test("continues the trace a client sends in traceparent", async () => {
    const e = await newEnv();
    e.resetSpans();
    const { client, traceID } = e.client.traced();
    expectStatus(await client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);

    expect((await e.waitForSpan(SPAN_ROUTE)).spanContext().traceId).toBe(traceID);
    expect((await e.serverSpan()).spanContext().traceId).toBe(traceID);
  });

  test("records the startup load with its own trigger", async () => {
    const cold = await newEnv({ unloaded: true });
    await cold.initialLoad();

    const span = await cold.waitForSpan(SPAN_LOAD);
    expect(span.status.code).toBe(SPAN_OK);
    expect(attrs(span.attributes)[TRIGGER_KEY]).toBe("startup");
  });

  test("records a reload in an alertrouter.policy.load span with what it loaded", async () => {
    const e = await newEnv();
    e.resetSpans();
    expectStatus(await e.client.reload(), 200);

    const span = await e.waitForSpan(SPAN_LOAD);
    expect(span.status.code).toBe(SPAN_OK);
    expect(attrs(span.attributes)).toMatchObject({
      "sigil.source": e.dir,
      "sigil.kind": KIND_ROUTING,
      [TRIGGER_KEY]: "manual",
      "sigil.policies": ["checkout.alerts", "payments.alerts"],
      "sigil.fingerprint": (await e.served()).fingerprint,
    });
  });
});
