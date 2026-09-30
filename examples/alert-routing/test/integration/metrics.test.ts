import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { firingCount, mixedBatch } from "../fixture/cases";
import { expectStatus, PATH_METRICS, routePath } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { expectBatch, expectFallback } from "../fixture/expect";
import {
  METRIC_ALERTS_RECEIVED,
  METRIC_ALERTS_ROUTED,
  METRIC_BATCH_SIZE,
  METRIC_DECISIONS,
  METRIC_EVAL_DURATION,
  METRIC_EVAL_ERRORS,
  METRIC_LAST_RELOAD,
  METRIC_NOTIFICATIONS,
  METRIC_POLICY_INFO,
  METRIC_RELOAD_OK,
  METRIC_RELOADS,
  METRIC_REQUEST_DURATION,
  METRIC_REQUESTS,
  parseMetrics,
} from "../fixture/metrics";
import {
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  CHECKOUT_MUTED,
  CHECKOUT_ONCALL,
  DECISION_DROP,
  DECISION_NOTIFY,
  DECISION_PAGE,
  DEFAULT_CHANNEL,
  firingAlert,
  REASON_CRITICAL_ALERT,
  REASON_MUTED,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  STATUS_FAILED,
  STATUS_INVALID,
  STATUS_ROUTED,
  STATUS_UNOWNED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "../fixture/requests";
import { ROOT_SUFFIX } from "../fixture/wire";
import {
  assertingRule,
  CHECKOUT_RULES,
  CLOCK_START,
  closeEnvs,
  conflictingRule,
  FixedClock,
  failingRule,
  newEnv,
  ROUTE_TEMPLATE,
  releaseTelemetry,
} from "./env";

/** The label set of the decision a critical checkout alert gets. */
const checkoutCritical = {
  team: TEAM_CHECKOUT,
  policy: "checkout.alerts",
  decision: DECISION_PAGE,
  reason: REASON_CRITICAL_ALERT,
};

afterEach(closeEnvs);
afterAll(releaseTelemetry);

// Every spec gets an env with a registry of its own, so the values are
// exact: the startup load and the spec's own requests, nothing else.
describe("Metrics", () => {
  test("counts the startup load and exposes what it loaded", async () => {
    const e = await newEnv({ clock: new FixedClock(CLOCK_START) });
    const families = await e.families();

    expect(families.value(METRIC_RELOADS, { result: "success" })).toBe(1);
    // The failure series exists before the first failure, so an alert on the
    // failure ratio has something to divide by.
    expect(families.find(METRIC_RELOADS, { result: "failure" })).toBeDefined();
    expect(families.value(METRIC_RELOADS, { result: "failure" })).toBe(0);
    expect(families.value(METRIC_LAST_RELOAD)).toBeCloseTo(CLOCK_START.getTime() / 1000, 3);
    expect(families.value(METRIC_RELOAD_OK)).toBe(1);

    const { fingerprint } = await e.served();
    expect(families.count(METRIC_POLICY_INFO)).toBe(2);
    for (const team of [TEAM_CHECKOUT, TEAM_PAYMENTS]) {
      const labels = { team, policy: team + ROOT_SUFFIX, fingerprint, source: e.dir };
      expect(families.value(METRIC_POLICY_INFO, labels), JSON.stringify(labels)).toBe(1);
    }
  });

  test("counts each decision by team, policy, decision and reason, and times each evaluation", async () => {
    const e = await newEnv();
    for (let i = 0; i < 3; i++) {
      expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);
    }
    expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_MUTED, SEVERITY_WARNING)), 200);

    const families = await e.families();
    expect(families.value(METRIC_DECISIONS, checkoutCritical)).toBe(3);
    expect(
      families.value(METRIC_DECISIONS, { team: TEAM_CHECKOUT, decision: DECISION_DROP, reason: REASON_MUTED }),
    ).toBe(1);
    expect(families.count(METRIC_DECISIONS)).toBe(2);

    expect(families.type(METRIC_EVAL_DURATION)).toBe("histogram");
    expect(families.sampleCount(METRIC_EVAL_DURATION, { team: TEAM_CHECKOUT })).toBe(4);

    // A notification per alert, by decision and destination.
    expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_PAGE, destination: CHECKOUT_ONCALL })).toBe(3);
    expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_DROP, destination: "-" })).toBe(1);
    expect(families.value(METRIC_ALERTS_RECEIVED, { status: "firing" })).toBe(4);
    expect(families.value(METRIC_ALERTS_ROUTED, { team: TEAM_CHECKOUT, outcome: STATUS_ROUTED })).toBe(4);
  });

  test("counts a webhook's alerts by status and outcome, its notifications and its size", async () => {
    const e = await newEnv();
    const batch = mixedBatch(new Date());
    const a = await e.client.webhook(batch.webhook);
    expectBatch(batch, a, a.out);

    const families = await e.families();
    expect(families.value(METRIC_ALERTS_RECEIVED, { status: "firing" })).toBe(firingCount(batch));
    expect(families.value(METRIC_ALERTS_RECEIVED, { status: "resolved" })).toBe(1);

    const routed = (team: string, outcome: string) => families.value(METRIC_ALERTS_ROUTED, { team, outcome });
    expect(routed(TEAM_CHECKOUT, STATUS_ROUTED)).toBe(2);
    expect(routed(TEAM_PAYMENTS, STATUS_ROUTED)).toBe(2);
    expect(routed(TEAM_CHECKOUT, STATUS_INVALID)).toBe(1);
    expect(routed(TEAM_PAYMENTS, STATUS_INVALID)).toBe(1);
    // The team label of an unowned alert is whatever its rule says, so it
    // never becomes a label value: every one would be a new series.
    expect(routed("-", STATUS_UNOWNED)).toBe(2);
    expect(families.count(METRIC_ALERTS_ROUTED, { team: "marketing" })).toBe(0);
    expect(families.sum(METRIC_ALERTS_ROUTED)).toBe(firingCount(batch));

    // Every firing alert ends in exactly one notification.
    expect(families.sum(METRIC_NOTIFICATIONS)).toBe(firingCount(batch));
    expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_NOTIFY, destination: DEFAULT_CHANNEL })).toBe(3);
    // The alert without a name still paged: platform.paging reads team and severity.
    expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_PAGE, destination: "payments-primary" })).toBe(2);

    // Only the policies' own decisions are decisions; the fallback of an
    // unowned or invalid alert isn't.
    expect(families.sum(METRIC_DECISIONS)).toBe(4);

    const size = families.find(METRIC_BATCH_SIZE)?.histogram;
    expect(size?.count).toBe(1);
    expect(size?.sum).toBe(batch.webhook.alerts.length);
  });

  test.each([
    ["two notifications of one reason to different channels", conflictingRule, "conflict", 500],
    ["a list read past its end", failingRule, "runtime", 500],
    // A failed input assert is the alert's fault, not the policy's, so it
    // answers 422, but it still routes the fallback and counts.
    ["an alert that fails an input assert", assertingRule, "assertion", 422],
  ] as const)(
    "counts a failed evaluation by team and kind, and not as a decision: %s",
    async (_, rules, kind, status) => {
      const e = await newEnv({ copyTeams: true });
      e.editCheckout(CHECKOUT_RULES, rules);
      await e.reloadOK();

      const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING));
      expectFallback(TEAM_CHECKOUT, status, a, a.out);

      const families = await e.families();
      expect(families.value(METRIC_EVAL_ERRORS, { team: TEAM_CHECKOUT, kind })).toBe(1);
      expect(families.count(METRIC_EVAL_ERRORS)).toBe(1);
      expect(families.count(METRIC_DECISIONS)).toBe(0);
      expect(families.value(METRIC_ALERTS_ROUTED, { team: TEAM_CHECKOUT, outcome: STATUS_FAILED })).toBe(1);
      expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_NOTIFY, destination: DEFAULT_CHANNEL })).toBe(1);
    },
  );

  test("doesn't count requests it refused before evaluating", async () => {
    const e = await newEnv();
    expectStatus(await e.client.postRaw(routePath(TEAM_CHECKOUT), "{"), 400);
    expectStatus(
      await e.client.postJSON(routePath("marketing"), firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)),
      404,
    );
    expectStatus(await e.client.postJSON(routePath(TEAM_CHECKOUT), firingAlert(CHECKOUT_ERROR_RATE, "urgent")), 422);

    const families = await e.families();
    expect(families.count(METRIC_DECISIONS)).toBe(0);
    expect(families.count(METRIC_EVAL_DURATION)).toBe(0);
    expect(families.count(METRIC_ALERTS_RECEIVED)).toBe(0);
    expect(families.count(METRIC_NOTIFICATIONS)).toBe(0);
  });

  test("serves its metrics and the Node process metrics on /metrics", async () => {
    const e = await newEnv();
    expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);

    const a = await e.client.get(PATH_METRICS);
    expectStatus(a, 200);
    expect(a.headers.get("content-type")).toStartWith("text/plain");

    const families = parseMetrics(a.body);
    expect(families.value(METRIC_DECISIONS, checkoutCritical)).toBe(1);
    // The runtime and process collectors sit on the same registry, so one
    // scrape shows the service and the process it runs in.
    expect(families.has("process_cpu_seconds_total")).toBe(true);
    expect(families.has("nodejs_eventloop_lag_seconds")).toBe(true);
    expect(families.has("nodejs_heap_size_used_bytes")).toBe(true);
  });

  test("counts HTTP requests by status, method and route template", async () => {
    const e = await newEnv();
    expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);
    expectStatus(await e.client.route(TEAM_PAYMENTS, firingAlert("PaymentsErrorRate", SEVERITY_CRITICAL)), 200);
    expectStatus(await e.client.webhook(mixedBatch(new Date()).webhook), 200);
    expectStatus(await e.client.get("/api/v2/nothing"), 404);
    expectStatus(await e.client.get(PATH_METRICS), 200);

    // The middleware records a request after the handler returns, which can
    // be a moment after the client holds the answer.
    await eventually(async () => {
      const families = await e.families();
      expect(families.value(METRIC_REQUESTS, { code: "200", method: "POST", route: ROUTE_TEMPLATE })).toBe(2);
      expect(families.value(METRIC_REQUESTS, { code: "200", method: "POST", route: "/api/v1/alerts" })).toBe(1);
      // A path without a route shares one series, so a scanner can't create
      // series without bound.
      expect(families.value(METRIC_REQUESTS, { code: "404", method: "GET", route: "unmatched" })).toBe(1);
      expect(families.sampleCount(METRIC_REQUEST_DURATION, { method: "POST", route: ROUTE_TEMPLATE })).toBe(2);
    });

    // Every team's routes share the route template's series, and the scrape
    // itself isn't counted, or it would dominate the rate.
    const families = await e.families();
    expect(families.count(METRIC_REQUESTS, { route: routePath(TEAM_CHECKOUT) })).toBe(0);
    expect(families.count(METRIC_REQUESTS, { route: PATH_METRICS })).toBe(0);
  });
});
