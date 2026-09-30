// alertrouter serves the team bundle built into it when no directory is
// mounted, and the compose stack mounts policies/teams. The two must be the
// same policies, or the service would behave differently the moment someone
// mounts the directory it was built from. The platform documents, which are
// always the built-in ones, must be policies/platform's.
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { PLATFORM_FILES } from "@/lib/embedded";
import { routeCases } from "../fixture/cases";
import { expectStatus } from "../fixture/client";
import { EXAMPLES_DIR } from "../fixture/requests";
import { type Env, newEnv, releaseTelemetry } from "./env";

let embedded: Env;
let onDisk: Env;
beforeAll(async () => {
  embedded = await newEnv({ embedded: true, track: false });
  onDisk = await newEnv({ sharedTeamsDir: true, track: false });
});
afterAll(async () => {
  await embedded.close();
  await onDisk.close();
  await releaseTelemetry();
});

describe("The built-in team bundle", () => {
  test("serves the same teams, policies and content as the directory it was built from", async () => {
    const e = await embedded.served();
    const d = await onDisk.served();
    expect(e.source).toBe("embedded");
    expect(e.policies).toEqual(d.policies);
    expect(e.policies.map((p) => p.policy)).toEqual(["checkout.alerts", "payments.alerts"]);
    expect(e.fingerprint).toBe(d.fingerprint);
  });

  test.each(routeCases().map((c) => [c.name, c] as const))("routes the same way as the directory: %s", async (_, c) => {
    const got = await embedded.client.route(c.team, c.request);
    const want = await onDisk.client.route(c.team, c.request);
    expectStatus(got, 200);
    expect([got.out.decision, got.out.reason]).toEqual([c.want.decision, c.want.reason]);
    expect(got.out).toEqual(want.out);
  });
});

describe("The built-in platform documents", () => {
  test("are policies/platform's, byte for byte", () => {
    for (const f of PLATFORM_FILES) {
      expect(f.source, f.path).toBe(readFileSync(join(EXAMPLES_DIR, "policies", f.path), "utf8"));
    }
    expect(PLATFORM_FILES.map((f) => f.path).sort()).toEqual([
      "platform/alerts.sigil",
      "platform/paging.sigil",
      "platform/routing.sigil",
    ]);
  });
});
