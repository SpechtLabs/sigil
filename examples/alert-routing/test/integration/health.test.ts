import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { mixedBatch } from "../fixture/cases";
import {
  decode,
  expectStatus,
  PATH_ALERTS,
  PATH_HEALTHZ,
  PATH_POLICIES,
  PATH_READYZ,
  routePath,
} from "../fixture/client";
import { expectDefaultTeams } from "../fixture/expect";
import { CHECKOUT_ERROR_RATE, firingAlert, SEVERITY_CRITICAL, TEAM_CHECKOUT } from "../fixture/requests";
import type { ErrorResponse } from "../fixture/wire";
import { CLOCK_START, closeEnvs, FixedClock, newEnv, releaseTelemetry } from "./env";

afterEach(closeEnvs);
afterAll(releaseTelemetry);

describe("Health and readiness", () => {
  describe("before the first load", () => {
    test("is healthy, so the orchestrator doesn't restart it, but not ready", async () => {
      const e = await newEnv({ unloaded: true });

      let a = await e.client.get(PATH_HEALTHZ);
      expectStatus(a, 200);
      expect(decode<unknown>(a)).toEqual({ status: "ok" });

      a = await e.client.get(PATH_READYZ);
      expectStatus(a, 503);
      expect(decode<unknown>(a)).toEqual({ status: "not ready" });
    });

    test("answers 503 instead of guessing a route, so Alertmanager retries the batch", async () => {
      const e = await newEnv({ unloaded: true });

      let a = await e.client.postJSON(routePath(TEAM_CHECKOUT), firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL));
      expectStatus(a, 503);
      expect(decode<ErrorResponse>(a).error).toBeDefined();

      a = await e.client.postJSON(PATH_ALERTS, mixedBatch(new Date()).webhook);
      expectStatus(a, 503);
      expect(decode<ErrorResponse>(a).error).toBeDefined();

      expectStatus(await e.client.get(PATH_POLICIES), 503);

      // Nothing was routed, so nothing may have been dispatched.
      expect(e.notifications()).toEqual([]);
    });

    test("still lists the team directory, which doesn't depend on the bundle", async () => {
      const e = await newEnv({ unloaded: true });
      expectDefaultTeams(await e.client.listTeams());
    });

    test("becomes ready once a reload succeeds", async () => {
      const e = await newEnv({ unloaded: true, clock: new FixedClock(CLOCK_START) });
      await e.reloadOK();

      const a = await e.client.get(PATH_READYZ);
      expectStatus(a, 200);
      expect(decode<unknown>(a)).toEqual({ status: "ready", loaded_at: "2026-09-28T12:00:00Z" });

      expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);
    });
  });

  test("reports ready with the time the bundle loaded", async () => {
    const e = await newEnv({ clock: new FixedClock(CLOCK_START) });
    const a = await e.client.get(PATH_READYZ);
    expectStatus(a, 200);
    expect(decode<unknown>(a)).toEqual({ status: "ready", loaded_at: "2026-09-28T12:00:00Z" });
  });
});
