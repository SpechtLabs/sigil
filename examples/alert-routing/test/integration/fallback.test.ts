// A team bundle can make its evaluation fail, on purpose or by accident, and
// the kind's default is a channel post. For a critical production alert that
// would turn a page into a message nobody is paged for, so when a team's
// evaluation fails alertrouter asks the platform's own platform.paging, with
// its params at their defaults, and pages when it does. The Go service
// didn't; this is the lead's addendum, item 1.
import { afterAll, afterEach, describe, expect, test } from "bun:test";
import type { Route } from "../fixture/cases";
import { expectStatus } from "../fixture/client";
import { expectBatch, routeOf } from "../fixture/expect";
import { METRIC_ALERTS_ROUTED, METRIC_DECISIONS, METRIC_EVAL_ERRORS, METRIC_NOTIFICATIONS } from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  CHECKOUT_ONCALL,
  DECISION_PAGE,
  firing,
  firingAlert,
  firingFor,
  MINUTE,
  newWebhook,
  REASON_CRITICAL_ALERT,
  REASON_SUSTAINED,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  STATUS_FAILED,
  slowPolicy,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import { messages, ROOT_SUFFIX } from "../fixture/wire";
import {
  assertingRule,
  CHECKOUT_POLICY,
  CHECKOUT_RULES,
  closeEnvs,
  type Env,
  failingRule,
  newEnv,
  releaseTelemetry,
  SPAN_ROUTE,
} from "./env";

/** A second critical page to another target, which conflicts with the platform's page. */
const criticalConflictRule = `${CHECKOUT_RULES}\n\nwhen alert.severity == critical {\n  page(reason: critical_alert, target: "checkout-secondary")\n}\n`;

/** An outcome assert that no alert may page, which fails exactly when the platform pages. */
const neverPagesRule = `${CHECKOUT_RULES}\n\nassert("never_pages", page not in outcome)\n`;

const METRIC_GUARDRAIL_VIOLATIONS = "alertrouter_guardrail_violations_total";

/** SpanStatusCode.ERROR. */
const SPAN_ERROR = 2;

const criticalPage: Route = { decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: CHECKOUT_ONCALL };

afterEach(closeEnvs);
afterAll(releaseTelemetry);

type Break = (e: Env) => void;

const failures: [string, Break, string, number][] = [
  ["a conflict", (e) => e.editCheckout(CHECKOUT_RULES, criticalConflictRule), "conflict", 500],
  ["a failing input assert", (e) => e.editCheckout(CHECKOUT_RULES, assertingRule), "assertion", 422],
  ["a failing outcome assert", (e) => e.editCheckout(CHECKOUT_RULES, neverPagesRule), "assertion", 500],
  ["a runtime error", (e) => e.editCheckout(CHECKOUT_RULES, failingRule), "runtime", 500],
  ["a timeout", (e) => e.writeTeamFile(CHECKOUT_POLICY, slowPolicy(10_000)), "timeout", 503],
];

async function brokenEnv(breakIt: Break): Promise<Env> {
  const e = await newEnv({ copyTeams: true, evaluationTimeoutMs: 100 });
  breakIt(e);
  await e.reloadOK();
  e.resetSpans();
  return e;
}

describe("A critical alert whose team evaluation fails", () => {
  test.each(failures)(
    "still pages the team's on-call through platform.paging: %s",
    async (_, breakIt, kind, status) => {
      const e = await brokenEnv(breakIt);

      const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL));
      expectStatus(a, status);
      expect(a.out.team).toBe(TEAM_CHECKOUT);
      expect(a.out.policy).toBe(TEAM_CHECKOUT + ROOT_SUFFIX);
      expect(routeOf(a.out)).toEqual(criticalPage);
      // The failure is still the answer's news: it says what went wrong.
      expect(messages(a.out.error).length).toBeGreaterThan(0);

      // The page goes out, to the on-call target and nowhere else.
      expect(e.notifications()).toEqual([
        expect.objectContaining({ decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: CHECKOUT_ONCALL }),
      ]);

      // It's counted as the failure it is, not as the team policy's decision.
      const families = await e.families();
      expect(families.value(METRIC_EVAL_ERRORS, { team: TEAM_CHECKOUT, kind })).toBe(1);
      expect(families.count(METRIC_DECISIONS)).toBe(0);
      expect(families.value(METRIC_ALERTS_ROUTED, { team: TEAM_CHECKOUT, outcome: STATUS_FAILED })).toBe(1);
      expect(families.value(METRIC_NOTIFICATIONS, { decision: DECISION_PAGE, destination: CHECKOUT_ONCALL })).toBe(1);

      // And the route span says it failed, while recording the page it sent.
      const span = await e.waitForSpan(SPAN_ROUTE);
      expect(span.status.code).toBe(SPAN_ERROR);
      expect(span.attributes["sigil.decision"]).toBe(DECISION_PAGE);
    },
  );

  test.each(failures)("pages from a webhook as well, as a failed result: %s", async (_, breakIt) => {
    const e = await brokenEnv(breakIt);
    const alert = firing(
      "critical",
      alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL),
      new Date(Date.now() - MINUTE),
    );

    const a = await e.client.webhook(newWebhook(alert));
    expectBatch(
      {
        webhook: newWebhook(alert),
        results: [{ fingerprint: "critical", status: STATUS_FAILED, team: TEAM_CHECKOUT, want: criticalPage }],
      },
      a,
      a.out,
    );
    expect(e.notifications()).toEqual([expect.objectContaining({ decision: DECISION_PAGE, target: CHECKOUT_ONCALL })]);
  });

  test("uses the platform's default threshold, not the team's, for a sustained warning", async () => {
    // checkout pages a warning after 10m; the fallback can't bind the team's
    // argument, so it pages at platform.paging's own 30m.
    const e = await brokenEnv((env) => env.editCheckout(CHECKOUT_RULES, failingRule));

    const at12 = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("12m")));
    expectStatus(at12, 500);
    expect(at12.out.decision).not.toBe(DECISION_PAGE);

    const at31 = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("31m")));
    expectStatus(at31, 500);
    expect({ decision: at31.out.decision, reason: at31.out.reason, target: at31.out.target }).toEqual({
      decision: DECISION_PAGE,
      reason: REASON_SUSTAINED,
      target: CHECKOUT_ONCALL,
    });
  });
});

// platform.paging is required of every team, but a team's own page with a
// reason the kind ranks higher would win over it and send the platform's
// page somewhere else. alertrouter evaluates platform.paging on its own for
// every alert, and when it pages and the team's decision isn't that page,
// the platform's page goes out. The lead's review, item 4.
describe("A team page that would redirect the platform's page", () => {
  /** Pages every warning to a target of the team's choosing, with the kind's highest page reason. */
  const redirectRule = `${CHECKOUT_RULES}\n\nwhen alert.severity == warning {\n  page(reason: critical_alert, target: "nobody")\n}\n`;

  async function redirectEnv(): Promise<Env> {
    const e = await newEnv({ copyTeams: true });
    e.editCheckout(CHECKOUT_RULES, redirectRule);
    await e.reloadOK();
    e.resetSpans();
    return e;
  }

  test("is replaced by the platform's page, and counted and logged as a guardrail violation", async () => {
    const e = await redirectEnv();

    const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("45m")));
    expectStatus(a, 500);
    expect(routeOf(a.out)).toEqual({ decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: CHECKOUT_ONCALL });
    expect(a.out.error?.message).toContain(
      "checkout.alerts decided page(reason: critical_alert, target: nobody) for an alert platform.paging pages checkout-primary for (reason: sustained)",
    );

    // Only the platform's page went out.
    expect(e.notifications()).toEqual([
      expect.objectContaining({ decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: CHECKOUT_ONCALL }),
    ]);

    const families = await e.families();
    expect(families.value(METRIC_GUARDRAIL_VIOLATIONS, { team: TEAM_CHECKOUT })).toBe(1);
    expect(families.count(METRIC_DECISIONS)).toBe(0);

    const line = e.logs().find((l) => l.msg === "guardrail violated: the platform's page replaced the team's decision");
    expect(line?.level).toBe("error");
    expect(line?.fields).toMatchObject({
      policy: "checkout.alerts",
      decision: DECISION_PAGE,
      reason: REASON_CRITICAL_ALERT,
      target: "nobody",
      platform_reason: REASON_SUSTAINED,
      platform_target: CHECKOUT_ONCALL,
    });

    const span = await e.waitForSpan(SPAN_ROUTE);
    expect(span.events.map((ev) => ev.name)).toContain("alertrouter.guardrail_violation");
  });

  test("is replaced in a webhook too, as a failed result", async () => {
    const e = await redirectEnv();
    const alert = firing(
      "redirected",
      alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING),
      new Date(Date.now() - 45 * MINUTE),
    );

    const a = await e.client.webhook(newWebhook(alert));
    expectBatch(
      {
        webhook: newWebhook(alert),
        results: [
          {
            fingerprint: "redirected",
            status: STATUS_FAILED,
            team: TEAM_CHECKOUT,
            want: { decision: DECISION_PAGE, reason: REASON_SUSTAINED, target: CHECKOUT_ONCALL },
          },
        ],
      },
      a,
      a.out,
    );
  });

  test("stands when the platform doesn't page: the team may page more than the platform, not less", async () => {
    const e = await redirectEnv();

    // A fresh warning isn't one platform.paging pages for, so the team's
    // extra page is the team's business.
    const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("1m")));
    expectStatus(a, 200);
    expect(routeOf(a.out)).toEqual({ decision: DECISION_PAGE, reason: REASON_CRITICAL_ALERT, target: "nobody" });
    expect((await e.families()).count(METRIC_GUARDRAIL_VIOLATIONS)).toBe(0);
  });
});
