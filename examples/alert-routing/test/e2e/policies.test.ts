import { beforeAll, describe, expect, test } from "bun:test";
import { decode, expectStatus, PATH_HEALTHZ, PATH_READYZ } from "../fixture/client";
import { expectDefaultTeams, expectServedPolicies } from "../fixture/expect";
import type { PoliciesResponse } from "../fixture/wire";
import { alertrouter, E2E, SPEC_TIMEOUT, waitReady } from "./e2e";

describe.skipIf(!E2E)("e2e", () => {
  beforeAll(waitReady, SPEC_TIMEOUT);

  describe("Health", () => {
    test(
      "reports healthy once the process serves",
      async () => {
        const a = await alertrouter.get(PATH_HEALTHZ);
        expectStatus(a, 200);
        expect(decode<Record<string, unknown>>(a)).toEqual({ status: "ok" });
      },
      SPEC_TIMEOUT,
    );

    test(
      "reports ready with the time the bundle loaded",
      async () => {
        const a = await alertrouter.get(PATH_READYZ);
        expectStatus(a, 200);
        const body = decode<Record<string, unknown>>(a);
        expect(body.status).toBe("ready");
        expect(body).toHaveProperty("loaded_at");
      },
      SPEC_TIMEOUT,
    );
  });

  describe("The team directory", () => {
    test(
      "lists every team with its on-call target and channel",
      async () => {
        expectDefaultTeams(await alertrouter.listTeams());
      },
      SPEC_TIMEOUT,
    );
  });

  describe("The policy bundle", () => {
    test(
      "lists one root per team, with the bundle's source and fingerprint",
      async () => {
        expectServedPolicies(await alertrouter.listPolicies());
      },
      SPEC_TIMEOUT,
    );

    test(
      "reloads on request and moves loaded_at forward",
      async () => {
        const before = await alertrouter.served();

        const a = await alertrouter.reload();
        expectStatus(a, 200);

        // The reload answers with the same body as GET /api/v1/policies, so a
        // client learns what is now loaded without a second call.
        const after = expectServedPolicies(decode<PoliciesResponse>(a));
        expect(Date.parse(after.loaded_at)).toBeGreaterThanOrEqual(Date.parse(before.loaded_at));
      },
      SPEC_TIMEOUT,
    );
  });
});
