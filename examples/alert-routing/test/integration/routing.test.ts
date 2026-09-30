import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import { manifestCases, manifestDescription, routeBadRequestCases, routeCases } from "../fixture/cases";
import { decode, expectStatus, routePath } from "../fixture/client";
import { expectDefaultTeams, expectManifestCase, expectRouteCase, expectServedPolicies } from "../fixture/expect";
import {
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  DECISION_NOTIFY,
  firingAlert,
  firingFor,
  REASON_ROUTINE,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import { type ErrorResponse, ROOT_SUFFIX, winners } from "../fixture/wire";
import { CLOCK_START, closeEnvs, type Env, FixedClock, newEnv, releaseTelemetry } from "./env";

// shared serves the checked-in team policies for the specs that only read.
// Specs that count notifications build an env of their own.
let shared: Env;
beforeAll(async () => {
  shared = await newEnv({ track: false, clock: new FixedClock(CLOCK_START) });
});
afterAll(async () => {
  await shared.close();
  await releaseTelemetry();
});
afterEach(closeEnvs);

describe("Routing one alert", () => {
  test.each(routeCases().map((c) => [c.name, c] as const))(
    "answers 200 with the route the team's policy chose: %s",
    async (_, c) => {
      const a = await shared.client.route(c.team, c.request);
      expectRouteCase(c, a, a.out);
    },
  );

  test("explains a sustained page with the platform rule and the call chain through the team's policy", async () => {
    const a = await shared.client.route(
      TEAM_CHECKOUT,
      firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("12m")),
    );
    expectStatus(a, 200);

    const w = winners(a.out.trace);
    expect(w).toHaveLength(1);
    expect(w[0]?.policy).toBe("platform.paging");
    expect(w[0]?.location).toBe("checkout/alerts.sigil:7:1 → platform/paging.sigil:12:3");
    // The condition shows the threshold checkout passed, not the
    // parameter's name, so the trace explains this team's page.
    expect(w[0]?.conditions).toEqual(["not pre_production and alert.severity == warning and alert.firing_for >= 10m"]);
    expect(w[0]?.payload).toEqual({ target: "checkout-primary" });

    // The warning's notification fired too and lost to the page, which is
    // what a trace is for: it shows what else the policy said.
    expect(a.out.trace).toContainEqual(
      expect.objectContaining({ decision: DECISION_NOTIFY, reason: REASON_ROUTINE, winner: false }),
    );
  });

  test("hands exactly one notification per alert to the dispatcher", async () => {
    const e = await newEnv();
    for (const c of routeCases()) expectStatus(await e.client.route(c.team, c.request), 200);

    expect(e.notifications()).toEqual(
      routeCases().map((c) => ({
        team: c.team,
        alertname: c.request.alert.name,
        fingerprint: "",
        policy: c.team + ROOT_SUFFIX,
        decision: c.want.decision,
        reason: c.want.reason,
        ...(c.want.target === undefined ? {} : { target: c.want.target }),
        ...(c.want.channel === undefined ? {} : { channel: c.want.channel }),
      })),
    );
  });

  describe("when the request can't be evaluated", () => {
    test("answers 404 naming the teams the directory lists", async () => {
      const a = await shared.client.postJSON(
        routePath("marketing"),
        firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL),
      );
      expectStatus(a, 404);
      const err = decode<ErrorResponse>(a).error;
      expect(err?.message).toContain(`"marketing"`);
      expect(err?.advice?.some((s) => s.includes("checkout, payments"))).toBe(true);
    });

    test.each(routeBadRequestCases().map((c) => [c.name, c] as const))(
      "refuses a body it won't evaluate, saying how to fix it: %s",
      async (_, c) => {
        const e = await newEnv();
        const a = await e.client.postRaw(routePath(TEAM_CHECKOUT), c.body);

        expectStatus(a, c.status);
        const err = decode<ErrorResponse>(a).error;
        expect(err).toBeDefined();
        expect(err?.advice?.length ?? 0, "a client error should say how to fix the request").toBeGreaterThan(0);

        // A refused request routes nothing, so nothing is dispatched.
        expect(e.notifications()).toEqual([]);
      },
    );

    test("answers 404 with the error model for a route that doesn't exist", async () => {
      const a = await shared.client.get("/api/v2/alerts");
      expectStatus(a, 404);
      expect(decode<ErrorResponse>(a).error).toBeDefined();
    });
  });
});

describe("The team directory", () => {
  test("lists every team with its on-call target and channel", async () => {
    expectDefaultTeams(await shared.client.listTeams());
  });
});

describe("The policy bundle", () => {
  test("lists one root per team with the directory it came from and its fingerprint", async () => {
    const k = expectServedPolicies(await shared.client.listPolicies());
    expect(k.source).toBe(shared.dir);
    expect(Date.parse(k.loaded_at)).toBe(CLOCK_START.getTime());
    expect(k.fingerprint).toMatch(/^[0-9a-f]{16,}$/);
  });
});

// The files under requests/ are what the README's curl examples and k6 send,
// and requests/cases.json says what each must answer. Running them here
// keeps all three honest: a change that alters an answer fails this table
// before it can make the walkthrough or the load test wrong.
describe("The sample requests", () => {
  test.each(manifestCases().map((c) => [manifestDescription(c), c] as const))(
    "answer what requests/cases.json expects: %s",
    async (_, c) => {
      await expectManifestCase(shared.client, c);
    },
  );
});
