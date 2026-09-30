import { afterAll, afterEach, beforeEach, describe, expect, test } from "bun:test";
import { rmSync } from "node:fs";
import { join } from "node:path";
import { decode, expectStatus } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { METRIC_LAST_RELOAD, METRIC_POLICY_INFO, METRIC_RELOAD_OK, METRIC_RELOADS } from "../fixture/metrics";
import {
  CHECKOUT_CHANNEL,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  CHECKOUT_ONCALL,
  DECISION_NOTIFY,
  DECISION_PAGE,
  firingAlert,
  firingFor,
  REASON_SUSTAINED,
  SEVERITY_CRITICAL,
  SEVERITY_WARNING,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import { type ErrorResponse, messages, type PoliciesResponse, routing } from "../fixture/wire";
import { attrs, CHECKOUT_POLICY, closeEnvs, type Env, newEnv, releaseTelemetry, SPAN_LOAD, TRIGGER_KEY } from "./env";

// What the reload specs write into their copies of the bundle.

// Where checkout sets when a warning pages, and an edit that doubles it.
const checkoutThreshold = "paging(page_after: 10m)";
const checkoutEdited = "paging(page_after: 20m)";

// A valid header, so the loader indexes it, and an unfinished condition, so
// it fails to parse and takes the whole bundle down with it.
const brokenDocument = "policy broken.alerts: AlertRouting@1\n\nwhen alert.severity == {\n  drop(reason: muted)\n}\n";

// A checkout policy that leaves out the platform's paging, which the
// guardrail rejects no matter what else it says.
const unpaged = "policy checkout.alerts: AlertRouting@1\n\nuse platform.routing\n\nrouting()\n";

// Invokes the paging, but only outside the team's quiet hours label, which
// the guardrail rejects as well: the platform's guarantee holds for every
// alert or it isn't one.
const conditionallyPaged =
  'policy checkout.alerts: AlertRouting@1\n\nuse platform.paging\nuse platform.routing\n\nwhen alert.labels["quiet"] != "true" {\n  paging(page_after: 10m)\n}\n\nrouting()\n';

// Claims the platform policy's name from the team bundle, to turn critical
// pages into drops.
const shadowPaging =
  "policy platform.paging: AlertRouting@1\n\nwhen alert.severity == critical {\n  drop(reason: muted)\n}\n";

afterEach(closeEnvs);
afterAll(releaseTelemetry);

describe("Hot reload", () => {
  // Each spec edits a private copy of the bundle, so nothing needs restoring
  // and no other spec sees the edits.
  let e: Env;
  beforeEach(async () => {
    e = await newEnv();
    e.resetSpans();
  });

  test("serves an edited threshold after a reload, with a new fingerprint", async () => {
    const before = await e.served();
    await expectSustainedAfter12m(e, true);

    e.editCheckout(checkoutThreshold, checkoutEdited);
    const a = await e.client.reload();
    expectStatus(a, 200);

    const after = routing(decode<PoliciesResponse>(a));
    expect(after).toBeDefined();
    expect(Date.parse(after?.loaded_at ?? "")).toBeGreaterThan(Date.parse(before.loaded_at));
    expect(after?.fingerprint).not.toBe(before.fingerprint);
    await expectSustainedAfter12m(e, false);

    // Labeling the loaded policies with the new fingerprint.
    const families = await e.families();
    expect(families.count(METRIC_POLICY_INFO, { fingerprint: after?.fingerprint ?? "" })).toBe(2);
    expect(families.count(METRIC_POLICY_INFO, { fingerprint: before.fingerprint })).toBe(0);
  });

  test("releases the replaced policies, so reloads don't leak WebAssembly memory", async () => {
    const replaced = [e.snapshot()];
    for (let i = 0; i < 3; i++) {
      await e.reloadOK();
      replaced.push(e.snapshot());
    }
    const live = replaced.pop();

    // Every snapshot a reload replaced gave its policies back to the module;
    // the one that serves still has them.
    expect(new Set(replaced).size).toBe(3);
    for (const snap of replaced) expect(snap?.released, `snapshot ${snap?.fingerprint} leaked`).toBe(true);
    expect(live?.released).toBe(false);
    await expectSustainedAfter12m(e, true);
  });

  describe("when the new bundle doesn't load", () => {
    test.each([
      [
        "a team document that doesn't parse",
        (e: Env) => e.writeTeamFile("broken.sigil", brokenDocument),
        ["broken.sigil:3:"],
      ],
      [
        "a team policy that leaves out the platform's paging",
        (e: Env) => e.writeTeamFile(CHECKOUT_POLICY, unpaged),
        ["checkout.alerts doesn't invoke platform.paging"],
      ],
      [
        "a team policy that pages only under a condition",
        (e: Env) => e.writeTeamFile(CHECKOUT_POLICY, conditionallyPaged),
        ["platform.paging must be invoked unconditionally"],
      ],
      // The platform bounds how long a team may let a warning fire before it
      // pages, so no team can page never or on every blip.
      [
        "a threshold above the platform's maximum",
        (e: Env) => e.editCheckout(checkoutThreshold, "paging(page_after: 2h)"),
        ["page_after: 2h is above the maximum 1h"],
      ],
      [
        "a threshold below the platform's minimum",
        (e: Env) => e.editCheckout(checkoutThreshold, "paging(page_after: 1m)"),
        ["page_after: 1m is below the minimum 5m"],
      ],
      [
        "a team document that claims the platform policy's name",
        (e: Env) => e.writeTeamFile("shadow.sigil", shadowPaging),
        ["platform.paging", "shadow.sigil"],
      ],
      [
        "a team in the directory without a policy",
        (e: Env) => rmSync(join(e.dir, "payments", "alerts.sigil")),
        ["payments.alerts"],
      ],
    ] as const)("rejects it and keeps serving the last good bundle: %s", async (_, write, diagnostics) => {
      const good = await e.served();
      write(e);

      const a = await e.client.reload();
      expectStatus(a, 500);

      const err = decode<ErrorResponse>(a).error;
      expect(err).toBeDefined();
      expect(err?.message).toContain("the previous bundle keeps serving");
      // The message says what happened; the compiler's diagnostics sit in
      // the cause below it.
      const all = messages(err).join("\n");
      for (const d of diagnostics) expect(all).toContain(d);
      expect(err?.advice?.length ?? 0).toBeGreaterThan(0);

      // Still routing with the last good bundle.
      await expectSustainedAfter12m(e, true);
      const critical = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL));
      expectStatus(critical, 200);
      expect(critical.out.target).toBe(CHECKOUT_ONCALL);
      expect(await e.served()).toEqual(good);

      // Counting the failure, marking the latest load as failed and keeping
      // the time and policies of the load that serves.
      const families = await e.families();
      expect(families.value(METRIC_RELOADS, { result: "failure" })).toBe(1);
      expect(families.value(METRIC_RELOAD_OK)).toBe(0);
      expect(families.value(METRIC_LAST_RELOAD)).toBeCloseTo(Date.parse(good.loaded_at) / 1000, 3);
      expect(families.count(METRIC_POLICY_INFO, { fingerprint: good.fingerprint })).toBe(2);

      // Marking the load span failed.
      const span = await e.waitForSpan(SPAN_LOAD);
      expect(span.status.code).toBe(SPAN_ERROR);
      expect(span.events.map((ev) => ev.name)).toContain("exception");
      expect(attrs(span.attributes)[TRIGGER_KEY]).toBe("manual");
    });

    test("loads again once the broken document is gone", async () => {
      e.writeTeamFile("broken.sigil", brokenDocument);
      expectStatus(await e.client.reload(), 500);
      expect((await e.families()).value(METRIC_RELOAD_OK)).toBe(0);

      rmSync(join(e.dir, "broken.sigil"));
      const a = await e.client.reload();
      expectStatus(a, 200);

      const families = await e.families();
      expect(families.value(METRIC_RELOADS, { result: "success" })).toBe(2);
      expect(families.value(METRIC_RELOAD_OK)).toBe(1);
      const reloaded = routing(decode<PoliciesResponse>(a));
      expect(families.value(METRIC_LAST_RELOAD)).toBeCloseTo(Date.parse(reloaded?.loaded_at ?? "") / 1000, 3);
    });
  });

  describe("when the store watches its directory", () => {
    test("reloads on the next poll after the content changed", async () => {
      e.editCheckout(checkoutThreshold, checkoutEdited);
      await e.tick();

      await eventually(async () => {
        const a = await e.client.route(
          TEAM_CHECKOUT,
          firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("12m")),
        );
        expectStatus(a, 200);
        expect(a.out.decision).toBe(DECISION_NOTIFY);
      });

      const span = await e.waitForSpan(SPAN_LOAD);
      expect(attrs(span.attributes)[TRIGGER_KEY]).toBe("poll");
    });

    test("leaves an unchanged directory alone", async () => {
      await e.tick();
      await e.tick();

      expect((await e.families()).value(METRIC_RELOADS, { result: "success" })).toBe(1);
      expect(e.spansNamed(SPAN_LOAD)).toEqual([]);
    });

    test("reports a broken bundle once, not at every poll, while the health gauge stays down", async () => {
      e.writeTeamFile("broken.sigil", brokenDocument);
      await e.tick();
      await e.tick();
      await e.tick();

      // The counter moved once and stays put, so an alert on its rate would
      // resolve while the stale bundle keeps serving; the gauge stays 0
      // until a load succeeds.
      const families = await e.families();
      expect(families.value(METRIC_RELOADS, { result: "failure" })).toBe(1);
      expect(families.value(METRIC_RELOAD_OK)).toBe(0);
    });

    test("reloads on SIGHUP whether or not anything changed", async () => {
      await e.sighup();

      const span = await e.waitForSpan(SPAN_LOAD);
      expect(attrs(span.attributes)[TRIGGER_KEY]).toBe("sighup");
      expect((await e.families()).value(METRIC_RELOADS, { result: "success" })).toBe(2);
    });
  });
});

describe("Startup", () => {
  test("fails when a team policy leaves out the platform's paging, and never becomes ready", async () => {
    const e = await newEnv({ unloaded: true });
    e.writeTeamFile(CHECKOUT_POLICY, unpaged);

    const err = await e.initialLoad().then(
      () => undefined,
      (err: unknown) => err,
    );
    expect(err).toBeInstanceOf(Error);
    expect(String((err as Error).message)).toContain("there is no earlier bundle to fall back to");
    expect(errorChain(err)).toContain("checkout.alerts doesn't invoke platform.paging");

    expectStatus(await e.client.get("/readyz"), 503);

    const span = await e.waitForSpan(SPAN_LOAD);
    expect(span.status.code).toBe(SPAN_ERROR);
    expect(attrs(span.attributes)[TRIGGER_KEY]).toBe("startup");
  });
});

/** SpanStatusCode.ERROR. */
const SPAN_ERROR = 2;

/**
 * Whether a checkout warning that has fired for twelve minutes pages, the
 * one outcome the threshold edit changes: past the shipped ten minutes it
 * pages, short of the edited twenty it posts to the team's channel.
 */
async function expectSustainedAfter12m(e: Env, pages: boolean): Promise<void> {
  const a = await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_LATENCY, SEVERITY_WARNING, firingFor("12m")));
  expectStatus(a, 200);
  if (pages) expect([a.out.decision, a.out.reason]).toEqual([DECISION_PAGE, REASON_SUSTAINED]);
  else expect([a.out.decision, a.out.channel]).toEqual([DECISION_NOTIFY, CHECKOUT_CHANNEL]);
}

/** The messages of err and every cause below it, joined, for a spec that calls the store directly. */
function errorChain(err: unknown): string {
  const out: string[] = [];
  for (let cur = err; cur instanceof Error; cur = cur.cause) out.push(cur.message);
  return out.join("\n");
}
