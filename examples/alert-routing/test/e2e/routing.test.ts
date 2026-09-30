import { beforeAll, describe, expect, test } from "bun:test";
import {
  firingCount,
  manifestCases,
  manifestDescription,
  mixedBatch,
  routeBadRequestCases,
  routeCases,
  uniqueTag,
  webhookBadRequestCases,
} from "../fixture/cases";
import { type Decoded, decode, expectStatus, PATH_ALERTS, routePath } from "../fixture/client";
import { expectBatch, expectManifestCase, expectRouteCase } from "../fixture/expect";
import { METRIC_NOTIFICATIONS } from "../fixture/metrics";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  DECISION_NOTIFY,
  DECISION_PAGE,
  firing,
  firingAlert,
  MINUTE,
  newWebhook,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  STATUS_INVALID,
  STATUS_ROUTED,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import type { ErrorResponse } from "../fixture/wire";
import { alertrouter, E2E, SPEC_TIMEOUT, scrapeMetrics, waitReady } from "./e2e";

const METRIC_DEDUPLICATED = "alertrouter_notifications_deduplicated_total";

// The answer and its decoded body, as expectBatch takes them.
function answer<T>(a: Decoded<T>): [Decoded<T>, T] {
  return [a, a.out];
}

describe.skipIf(!E2E)("e2e", () => {
  beforeAll(waitReady, SPEC_TIMEOUT);

  describe("Routing one alert", () => {
    test.each(routeCases().map((c) => [c.name, c] as const))(
      "answers 200 with the route the team's policy chose: %s",
      async (_, c) => {
        const a = await alertrouter.route(c.team, c.request);
        expectRouteCase(c, a, a.out);
      },
      SPEC_TIMEOUT,
    );

    describe("when the request can't be evaluated", () => {
      test(
        "answers 404 for a team the directory doesn't list",
        async () => {
          const a = await alertrouter.postJSON(
            routePath("marketing"),
            firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL),
          );
          expectStatus(a, 404);
          expect(decode<ErrorResponse>(a).error).toBeDefined();
        },
        SPEC_TIMEOUT,
      );

      test.each(routeBadRequestCases().map((c) => [c.name, c] as const))(
        "refuses a body it won't evaluate: %s",
        async (_, c) => {
          const a = await alertrouter.postRaw(routePath(TEAM_CHECKOUT), c.body);
          expectStatus(a, c.status);
          expect(decode<ErrorResponse>(a).error).toBeDefined();
        },
        SPEC_TIMEOUT,
      );
    });
  });

  describe("Receiving an Alertmanager webhook", () => {
    test(
      "routes every firing alert, acknowledges the resolved one and loses none",
      async () => {
        const batch = mixedBatch(new Date(), uniqueTag());
        const a = await alertrouter.webhook(batch.webhook);
        expectBatch(batch, a, a.out);
      },
      SPEC_TIMEOUT,
    );

    // Alertmanager redelivers a group it isn't sure arrived; a redelivery
    // must not page twice.
    test(
      "notifies once when Alertmanager delivers the same alerts again",
      async () => {
        const batch = mixedBatch(new Date(), uniqueTag());
        expectBatch(batch, ...answer(await alertrouter.webhook(batch.webhook)));
        const before = await scrapeMetrics();

        expectBatch(batch, ...answer(await alertrouter.webhook(batch.webhook)));

        const after = await scrapeMetrics();
        expect(after.sum(METRIC_NOTIFICATIONS) - before.sum(METRIC_NOTIFICATIONS)).toBe(0);
        expect(after.sum(METRIC_DEDUPLICATED) - before.sum(METRIC_DEDUPLICATED)).toBe(firingCount(batch));
      },
      SPEC_TIMEOUT,
    );

    // A 4xx drops the whole group for good, so one alert alertrouter can't
    // read makes that alert invalid, not the batch.
    test(
      "routes an alert with a status it doesn't know as invalid, and the rest as usual",
      async () => {
        const tag = uniqueTag();
        const minuteAgo = new Date(Date.now() - MINUTE);
        const pending = {
          ...firing(`pending-${tag}`, alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING), minuteAgo),
          status: "pending",
        };
        const critical = firing(
          `critical-${tag}`,
          alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL),
          minuteAgo,
        );

        const a = await alertrouter.webhook(newWebhook(pending, critical));
        expectStatus(a, 200);
        expect(a.out.results.map((r) => [r.status, r.decision])).toEqual([
          [STATUS_INVALID, DECISION_NOTIFY],
          [STATUS_ROUTED, DECISION_PAGE],
        ]);
      },
      SPEC_TIMEOUT,
    );

    test.each(webhookBadRequestCases().map((c) => [c.name, c] as const))(
      "answers 400 for a payload it can't read: %s",
      async (_, c) => {
        const a = await alertrouter.postRaw(PATH_ALERTS, c.body);
        expectStatus(a, c.status);
        expect(decode<ErrorResponse>(a).error).toBeDefined();
      },
      SPEC_TIMEOUT,
    );
  });

  // The files under requests/ are what the README's curl examples and k6
  // send, and requests/cases.json says what each must answer. Running them
  // against the container keeps the three honest.
  describe("The sample requests", () => {
    test.each(manifestCases().map((c) => [manifestDescription(c), c] as const))(
      "answer what requests/cases.json expects: %s",
      async (_, c) => {
        await expectManifestCase(alertrouter, c);
      },
      SPEC_TIMEOUT,
    );
  });
});
