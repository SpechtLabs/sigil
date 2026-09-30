import { afterEach, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { join } from "node:path";
import { UNROUTED } from "../fixture/cases";
import { routePath } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { expectBatch, expectFallback } from "../fixture/expect";
import {
  type Families,
  METRIC_ALERTS_ROUTED,
  METRIC_DECISIONS,
  METRIC_EVAL_ERRORS,
  METRIC_REQUESTS,
} from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_LATENCY,
  firing,
  firingAlert,
  MINUTE,
  newWebhook,
  SEVERITY_WARNING,
  STATUS_FAILED,
  slowPolicy,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import {
  alertrouter,
  E2E,
  policiesDir,
  reloadUntilServed,
  replaceFile,
  restore,
  SPEC_TIMEOUT,
  scrapeMetrics,
  unwritableReason,
  waitReady,
} from "./e2e";

/**
 * When the 499 spec's client gives up: after its request reached the
 * service, and well before the stack's 50ms evaluation timeout would answer.
 */
const ABANDON_AFTER_MS = 20;

/** The route template the request metrics label a route with. */
const ROUTE_TEMPLATE = "/api/v1/teams/:team/route";

const unwritable = E2E ? await unwritableReason() : "the e2e suite is off";

// The shipped policies decide in microseconds whatever the alert, so these
// specs swap checkout's policy for one that runs for many seconds, far past
// the stack's evaluation timeout (ALERTROUTER_EVALUATION_TIMEOUT, 50ms by
// default). Every other checkout spec would time out meanwhile, which bun's
// one-file-at-a-time order rules out.
describe.skipIf(unwritable !== undefined)("e2e", () => {
  beforeAll(waitReady, SPEC_TIMEOUT);

  describe("An evaluation that doesn't finish", () => {
    beforeEach(async () => {
      replaceFile(join(policiesDir, "checkout/alerts.sigil"), slowPolicy(30_000));
      await reloadUntilServed();
    }, SPEC_TIMEOUT);
    afterEach(restore, SPEC_TIMEOUT);

    test(
      "answers 503 with the fallback once the evaluation timeout passes, and counts a timeout",
      async () => {
        const timeouts = { team: TEAM_CHECKOUT, kind: "timeout" };
        const before = await scrapeMetrics();

        const start = performance.now();
        const a = await alertrouter.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING));
        expect(performance.now() - start).toBeLessThan(5_000);
        expectFallback(TEAM_CHECKOUT, 503, a, a.out, "evaluation timeout");

        const after = await scrapeMetrics();
        expect(after.value(METRIC_EVAL_ERRORS, timeouts) - before.value(METRIC_EVAL_ERRORS, timeouts)).toBe(1);
        expect(after.sum(METRIC_DECISIONS)).toBe(before.sum(METRIC_DECISIONS));
      },
      SPEC_TIMEOUT,
    );

    test(
      "marks the alert of a webhook as failed and still notifies #alerts",
      async () => {
        const batch = {
          webhook: newWebhook(
            firing(
              "slow",
              alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING),
              new Date(Date.now() - MINUTE),
            ),
          ),
          results: [{ fingerprint: "slow", status: STATUS_FAILED, team: TEAM_CHECKOUT, want: UNROUTED }],
        };
        const failed = { team: TEAM_CHECKOUT, outcome: STATUS_FAILED };
        const before = (await scrapeMetrics()).value(METRIC_ALERTS_ROUTED, failed);

        const a = await alertrouter.webhook(batch.webhook);
        expectBatch(batch, a, a.out);

        expect((await scrapeMetrics()).value(METRIC_ALERTS_ROUTED, failed) - before).toBe(1);
      },
      SPEC_TIMEOUT,
    );

    test(
      "answers 499 and counts no error when the client leaves during the evaluation",
      async () => {
        const closed = { code: "499", method: "POST", route: ROUTE_TEMPLATE };
        const before = await scrapeMetrics();

        await alertrouter.abandon(
          routePath(TEAM_CHECKOUT),
          firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING),
          ABANDON_AFTER_MS,
        );

        // The server notices the closed connection and stops the evaluation a
        // moment after the client gave up.
        const after: Families = await eventually(
          async () => {
            const f = await scrapeMetrics();
            expect(f.value(METRIC_REQUESTS, closed) - before.value(METRIC_REQUESTS, closed)).toBe(1);
            return f;
          },
          { timeoutMs: 5_000, intervalMs: 100 },
        );

        // Nothing failed: the client left on its own.
        expect(after.sum(METRIC_EVAL_ERRORS)).toBe(before.sum(METRIC_EVAL_ERRORS));
        expect(after.sum(METRIC_DECISIONS)).toBe(before.sum(METRIC_DECISIONS));
      },
      SPEC_TIMEOUT,
    );
  });
});
