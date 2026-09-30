import { afterEach, beforeAll, describe, expect, test } from "bun:test";
import { join } from "node:path";
import { decode, expectStatus } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { METRIC_LAST_RELOAD, METRIC_RELOAD_OK, METRIC_RELOADS } from "../fixture/metrics";
import {
  CHECKOUT_CHANNEL,
  CHECKOUT_LATENCY,
  DECISION_NOTIFY,
  DECISION_PAGE,
  firingAlert,
  firingFor,
  REASON_SUSTAINED,
  SEVERITY_WARNING,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import { type ErrorResponse, messages } from "../fixture/wire";
import {
  alertrouter,
  createFile,
  E2E,
  editFile,
  policiesDir,
  reloadUntilRejected,
  reloadUntilServed,
  replaceFile,
  restore,
  SPEC_TIMEOUT,
  scrapeMetrics,
  unwritableReason,
  waitReady,
} from "./e2e";

// What the hot reload specs change in the bind-mounted bundle.
const checkoutPolicy = "checkout/alerts.sigil";
const checkoutRules = "paging(page_after: 10m)";
const checkoutEdited = "paging(page_after: 20m)";
const brokenFile = "broken.sigil";

// A valid header, so the loader indexes it, and an unfinished condition, so
// it fails to parse and takes the whole bundle down with it.
const brokenDocument = "policy broken.alerts: AlertRouting@1\n\nwhen alert.severity == {\n  drop(reason: muted)\n}\n";

// A checkout policy that leaves out the platform's paging, which the
// guardrail rejects no matter what else it says.
const unpaged = "policy checkout.alerts: AlertRouting@1\n\nuse platform.routing\n\nrouting()\n";

const unwritable = E2E ? await unwritableReason() : "the e2e suite is off";
if (E2E && unwritable !== undefined) console.warn(`skipping the hot reload specs: ${unwritable}`);

/**
 * Whether a checkout warning that has fired for twelve minutes pages, the
 * one outcome the threshold edit changes: past the shipped ten minutes it
 * pages, short of the edited twenty it posts to the team's channel.
 */
async function expectCheckoutPagesAfter12m(pages: boolean): Promise<void> {
  const a = await alertrouter.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("12m")));
  expectStatus(a, 200);
  if (pages) {
    expect([a.out.decision, a.out.reason]).toEqual([DECISION_PAGE, REASON_SUSTAINED]);
    return;
  }
  expect([a.out.decision, a.out.channel]).toEqual([DECISION_NOTIFY, CHECKOUT_CHANNEL]);
}

// Serial by construction (bun runs one file's tests in order): these specs
// change what every other spec is evaluated against. Each one registers its
// restore before it touches a file, so a failed assertion still puts the
// bundle back.
describe.skipIf(unwritable !== undefined)("e2e", () => {
  beforeAll(waitReady, SPEC_TIMEOUT);
  afterEach(restore, SPEC_TIMEOUT);

  describe("Hot reload", () => {
    describe("when a team edits its policy", () => {
      test(
        "serves the edited threshold after a reload and the original after restoring it",
        async () => {
          editFile(join(policiesDir, checkoutPolicy), checkoutRules, checkoutEdited, () =>
            expectCheckoutPagesAfter12m(true),
          );

          await reloadUntilServed();
          await expectCheckoutPagesAfter12m(false);
        },
        SPEC_TIMEOUT,
      );
    });

    describe("when the new bundle doesn't load", () => {
      test.each([
        [
          "a document that doesn't parse",
          () =>
            createFile(join(policiesDir, brokenFile), brokenDocument, async () => {
              // The bundle loaded again, so its latest load succeeded.
              expect((await scrapeMetrics()).value(METRIC_RELOAD_OK)).toBe(1);
            }),
          `${brokenFile}:3:`,
        ],
        [
          "a team policy that leaves out the platform's paging",
          () => replaceFile(join(policiesDir, checkoutPolicy), unpaged),
          "checkout.alerts doesn't invoke platform.paging",
        ],
      ] as const)(
        "rejects the reload and keeps serving the last good bundle: %s",
        async (_, write, diagnostic) => {
          const good = await alertrouter.served();
          const failure = { result: "failure" };
          const failuresBefore = (await scrapeMetrics()).value(METRIC_RELOADS, failure);

          write();

          // The broken bytes may take a moment to reach the container.
          const a = await reloadUntilRejected();
          // The diagnostics name what is wrong, so whoever broke the bundle
          // sees it from the reload response alone.
          const err = decode<ErrorResponse>(a).error;
          expect(err).toBeDefined();
          expect(messages(err).join("\n")).toContain(diagnostic);

          // Still routing with the last good bundle.
          await expectCheckoutPagesAfter12m(true);
          // A reload that ran before the broken bytes reached the container
          // loaded the good bundle again, so the fingerprint says which bundle
          // serves, and loaded_at is that of the latest good load.
          const now = await alertrouter.served();
          expect(now.fingerprint).toBe(good.fingerprint);
          expect(Date.parse(now.loaded_at)).toBeGreaterThanOrEqual(Date.parse(good.loaded_at));

          // Counting the failed reload and marking the latest load as failed.
          // The poller may see the broken file first, so the counter moved
          // at least once.
          await eventually(async () => {
            const families = await scrapeMetrics();
            expect(families.value(METRIC_RELOADS, failure) - failuresBefore).toBeGreaterThanOrEqual(1);
            expect(families.value(METRIC_RELOAD_OK)).toBe(0);
            // The time is that of the bundle that still serves.
            expect(families.value(METRIC_LAST_RELOAD)).toBeCloseTo(Date.parse(now.loaded_at) / 1000, 3);
          });
        },
        SPEC_TIMEOUT,
      );
    });
  });
});
