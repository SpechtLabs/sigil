// alertrouter runs two kinds of engine. Team policies compile and evaluate
// in a pool of workers; platform.paging runs in an instance of its own that
// no team file ever reaches. A team bundle that breaks its engine is
// rejected and can't touch the platform's page, and when the platform
// engine itself fails, alertrouter says so (503 on /readyz and on the
// answers it can't vouch for), replaces it, and gives up for boot to exit
// only when no replacement loads. The Go service had one engine; this is the
// lead's review, item 1.
import { afterAll, afterEach, describe, expect, spyOn, test } from "bun:test";
import { Policy, SigilStoppedError } from "@spechtlabs/sigil";
import { loadSigil } from "@/lib/sigil";
import { testWasm } from "@/lib/testing";
import { expectStatus, PATH_READYZ } from "../fixture/client";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_ONCALL,
  DECISION_NOTIFY,
  DECISION_PAGE,
  DEFAULT_CHANNEL,
  firing,
  firingAlert,
  MINUTE,
  newWebhook,
  PAYMENTS_ONCALL,
  SEVERITY_CRITICAL,
  STATUS_FAILED,
  STATUS_ROUTED,
  TEAM_CHECKOUT,
  TEAM_PAYMENTS,
} from "../fixture/requests";
import { type ErrorResponse, messages } from "../fixture/wire";
import { CHECKOUT_RULES, closeEnvs, type Env, failingRule, newEnv, releaseTelemetry } from "./env";

const METRIC_ENGINE_RESTARTS = "alertrouter_engine_restarts_total";
const METRIC_ENGINE_UP = "alertrouter_engine_up";

/**
 * A condition nested two hundred thousand parentheses deep: the Go parser in the
 * module recurses once per level and runs out of stack, which kills the
 * WebAssembly instance compiling it.
 */
const DEEP = `${"(".repeat(200_000)}alert.name${")".repeat(200_000)}`;
const crashingRule = `${CHECKOUT_RULES}\n\nwhen ${DEEP} == "x" {\n  drop(reason: muted)\n}\n`;

afterEach(closeEnvs);
afterAll(releaseTelemetry);

/** A critical production alert of team, which every version of platform.paging pages for. */
function critical(team: string) {
  return firingAlert(team === TEAM_CHECKOUT ? CHECKOUT_ERROR_RATE : "PaymentsErrorRate", SEVERITY_CRITICAL);
}

/** The platform engine's loader, with every instance after the first held until release() or failing. */
function gatedLoader(mode: "hold" | "fail") {
  let calls = 0;
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  return {
    get calls() {
      return calls;
    },
    release,
    load: async () => {
      calls++;
      if (calls > 1) {
        if (mode === "fail") throw new Error("the replacement instance ran out of memory");
        await gate;
      }
      return loadSigil(() => {}, await testWasm());
    },
  };
}

/**
 * Makes the platform engine's next evaluation fail the way it does when its
 * module stops, say because Go code ran out of stack: team policies run in
 * workers, so only the in-process platform engine calls this Policy.
 */
function breakPlatformOnce() {
  return spyOn(Policy.prototype, "eval").mockImplementationOnce(() => {
    throw new SigilStoppedError("the Sigil module stopped: Maximum call stack size exceeded");
  });
}

async function expectReady(e: Env, status: number): Promise<void> {
  expectStatus(await e.client.get(PATH_READYZ), status);
}

describe("A team bundle that crashes the engine compiling it", () => {
  test("is rejected, and the last good bundle keeps routing every team in fresh workers", async () => {
    const e = await newEnv({ copyTeams: true });
    const good = await e.served();
    e.editCheckout(CHECKOUT_RULES, crashingRule);

    const rejected = await e.client.reload();
    expectStatus(rejected, 500);
    expect(messages((JSON.parse(rejected.body) as ErrorResponse).error).join("\n")).toContain(
      "the bundle made the evaluation engine fail",
    );
    expect((await e.families()).value(METRIC_ENGINE_RESTARTS, { engine: "worker" })).toBeGreaterThanOrEqual(1);
    expect((await e.served()).fingerprint).toBe(good.fingerprint);

    // The platform engine never saw the file, so readiness holds.
    await expectReady(e, 200);
    for (const [team, oncall] of [
      [TEAM_CHECKOUT, CHECKOUT_ONCALL],
      [TEAM_PAYMENTS, PAYMENTS_ONCALL],
    ] as const) {
      const a = await e.client.route(team, critical(team));
      expectStatus(a, 200);
      expect([a.out.decision, a.out.target]).toEqual([DECISION_PAGE, oncall]);
    }

    // Once the file is fixed, the next reload loads it.
    e.editCheckout(crashingRule, CHECKOUT_RULES);
    await e.reloadOK();
  }, 30_000);

  test("fails startup, so the pod never becomes ready", async () => {
    const e = await newEnv({ copyTeams: true, unloaded: true });
    e.editCheckout(CHECKOUT_RULES, crashingRule);

    const err = await e.initialLoad().then(
      () => undefined,
      (err: unknown) => err,
    );
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toContain("there is no earlier bundle to fall back to");
    await expectReady(e, 503);
  }, 30_000);
});

// The platform engine answers for an alert only when the team's evaluation
// left no trace to read platform.paging's pages from (it failed, or never
// ran), and for the readiness probe's canary. Its failures show there.
describe("The platform engine", () => {
  test("that the readiness probe finds broken is replaced, and /readyz recovers", async () => {
    const e = await newEnv();
    const spy = breakPlatformOnce();
    try {
      await expectReady(e, 503);
    } finally {
      spy.mockRestore();
    }

    await e.service.platform.recovered();
    await expectReady(e, 200);
    const families = await e.families();
    expect(families.value(METRIC_ENGINE_RESTARTS, { engine: "platform" })).toBe(1);
    expect(families.value(METRIC_ENGINE_UP, { engine: "platform" })).toBe(1);
    expect(e.fatal).toEqual([]);
  }, 30_000);

  test("while it's replaced, alerts whose team decided route as usual, and one that needs it goes out answered 503", async () => {
    const loader = gatedLoader("hold");
    const e = await newEnv({ copyTeams: true, loadPlatformSigil: loader.load });
    // payments decides by itself; checkout's policy fails at run time, so
    // its alerts need platform.paging to know whether they page.
    e.editCheckout(CHECKOUT_RULES, failingRule);
    await e.reloadOK();

    const spy = breakPlatformOnce();
    try {
      await expectReady(e, 503);
    } finally {
      spy.mockRestore();
    }
    expect(loader.calls).toBe(2);
    expect((await e.families()).value(METRIC_ENGINE_UP, { engine: "platform" })).toBe(0);

    const payments = await e.client.route(TEAM_PAYMENTS, critical(TEAM_PAYMENTS));
    expectStatus(payments, 200);
    expect([payments.out.decision, payments.out.target]).toEqual([DECISION_PAGE, PAYMENTS_ONCALL]);

    // Alertmanager retries a 503, so a batch whose fallback couldn't be
    // vouched for is delivered again once the engine is back.
    const minuteAgo = new Date(Date.now() - MINUTE);
    const webhook = newWebhook(
      firing("down-checkout", alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
      firing("down-payments", alertLabels(TEAM_PAYMENTS, "PaymentsErrorRate", SEVERITY_CRITICAL), minuteAgo),
    );
    const during = await e.client.webhook(webhook);
    expectStatus(during, 503);
    const [checkout, paymentsResult] = during.out.results;
    expect(checkout?.status).toBe(STATUS_FAILED);
    // Without platform.paging's verdict only the kind's default is known.
    expect([checkout?.decision, checkout?.channel]).toEqual([DECISION_NOTIFY, DEFAULT_CHANNEL]);
    expect([paymentsResult?.status, paymentsResult?.decision]).toEqual([STATUS_ROUTED, DECISION_PAGE]);
    // Nothing is held back: the fallback went out for checkout's alert.
    expect(e.notifications().filter((n) => n.fingerprint.startsWith("down-"))).toHaveLength(2);

    loader.release();
    await e.service.platform.recovered();
    await expectReady(e, 200);

    // The retry pages checkout's on-call through platform.paging.
    const after = await e.client.webhook(webhook);
    expectStatus(after, 200);
    expect([after.out.results[0]?.decision, after.out.results[0]?.target]).toEqual([DECISION_PAGE, CHECKOUT_ONCALL]);
    expect(e.fatal).toEqual([]);
  }, 30_000);

  test("that can't be replaced gives up, for boot to exit, and stays unready", async () => {
    const loader = gatedLoader("fail");
    const e = await newEnv({ loadPlatformSigil: loader.load });
    const spy = breakPlatformOnce();
    try {
      await expectReady(e, 503);
    } finally {
      spy.mockRestore();
    }

    await e.service.platform.recovered();
    expect(loader.calls).toBe(1 + 3);
    expect(e.fatal).toHaveLength(1);
    expect(e.fatal[0]?.message).toContain("couldn't be replaced after 3 attempts");
    await expectReady(e, 503);
  }, 30_000);
});
