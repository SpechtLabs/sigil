import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { firingCount, mixedBatch } from "../fixture/cases";
import { expectStatus } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { destination, expectBatch } from "../fixture/expect";
import {
  alertLabels,
  CHECKOUT_LATENCY,
  DEFAULT_CHANNEL,
  firing,
  MINUTE,
  newWebhook,
  SEVERITY_WARNING,
  STATUS_FAILED,
  STATUS_INVALID,
  STATUS_RESOLVED,
  STATUS_UNOWNED,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import {
  attrs,
  CHECKOUT_RULES,
  closeEnvs,
  conflictingRule,
  type Env,
  type LogLine,
  newEnv,
  releaseTelemetry,
  SPAN_ROUTE,
} from "./env";

/**
 * The log messages the service promises. The Loki queries of the dashboard
 * and of an operator chasing an alert depend on them.
 */
const LOG_ROUTED = "alert routed";
const LOG_DISPATCHED = "notification dispatched";

afterEach(closeEnvs);
afterAll(releaseTelemetry);

describe("Logs", () => {
  test("writes one alert routed line per firing alert, at a level that says how it went", async () => {
    const e = await newEnv({ logNotifier: true });
    const batch = mixedBatch(new Date());
    const a = await e.client.webhook(batch.webhook);
    expectBatch(batch, a, a.out);

    const routed = linesByFingerprint(e, LOG_ROUTED);
    expect(routed.size).toBe(firingCount(batch));
    expect(routed.has("checkout-resolved"), "a resolved alert is acknowledged, not routed").toBe(false);

    // A policy's route and an unowned alert's default went where they
    // should, so they are info; an alert the router can't read is a warning
    // to whoever wrote its rule.
    for (const want of batch.results) {
      if (want.status === STATUS_RESOLVED) continue;
      const line = routed.get(want.fingerprint);
      expect(line?.level, want.fingerprint).toBe(want.status === STATUS_INVALID ? "warn" : "info");
      expect(line?.fields, want.fingerprint).toMatchObject({
        status: want.status,
        decision: want.want?.decision,
        reason: want.want?.reason,
        destination: destination(want.want ?? { decision: "", reason: "" }),
      });
    }

    // An unowned alert names no team; the label it carried is kept apart.
    expect(routed.get("unknown-team")?.fields).toMatchObject({ team: "-", team_label: "marketing" });
  });

  test("writes a failed alert's line as an error", async () => {
    const e = await newEnv({ copyTeams: true, logNotifier: true });
    e.editCheckout(CHECKOUT_RULES, conflictingRule);
    await e.reloadOK();

    const alert = firing(
      "conflict",
      alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, SEVERITY_WARNING),
      new Date(Date.now() - MINUTE),
    );
    expectStatus(await e.client.webhook(newWebhook(alert)), 200);

    const line = linesByFingerprint(e, LOG_ROUTED).get("conflict");
    expect(line?.level).toBe("error");
    expect(line?.fields).toMatchObject({ status: STATUS_FAILED, destination: DEFAULT_CHANNEL });
  });

  test("writes one notification dispatched line per alert, in the alert's trace", async () => {
    const e = await newEnv({ logNotifier: true });
    const batch = mixedBatch(new Date());
    const a = await e.client.webhook(batch.webhook);
    expectBatch(batch, a, a.out);

    const routed = linesByFingerprint(e, LOG_ROUTED);
    const dispatched = linesByFingerprint(e, LOG_DISPATCHED);
    expect(dispatched.size).toBe(firingCount(batch));

    const spans = await eventually(() => {
      const s = e.spansNamed(SPAN_ROUTE);
      expect(s).toHaveLength(firingCount(batch));
      return new Map(s.map((span) => [attrs(span.attributes)["alert.fingerprint"] as string, span]));
    });

    for (const [fingerprint, line] of dispatched) {
      const d = line.fields;
      const r = routed.get(fingerprint)?.fields ?? {};
      const span = spans.get(fingerprint);

      // Both lines are written inside the alert's route span, so either one
      // leads to the trace that explains the decision.
      expect(d.trace_id, fingerprint).toBe(span?.spanContext().traceId);
      expect(d.span_id, fingerprint).toBe(span?.spanContext().spanId);
      expect(r.trace_id, fingerprint).toBe(d.trace_id);
      expect(r.span_id, fingerprint).toBe(d.span_id);
      expect(d.decision, fingerprint).toBe(r.decision);
      expect(d.destination, fingerprint).toBe(r.destination);
    }

    // An unowned alert's notification names no team either, and one no
    // policy ran for (unowned or invalid) names no policy; a routed one does.
    expect(dispatched.get("no-team")?.fields.team).toBe("-");
    for (const r of batch.results) {
      if (r.status === STATUS_UNOWNED || r.status === STATUS_INVALID) {
        expect(dispatched.get(r.fingerprint)?.fields, r.fingerprint).not.toHaveProperty("policy");
      }
    }
    expect(dispatched.get("checkout-critical")?.fields.policy).toBe("checkout.alerts");
  });

  test("writes every key of a line once, trace and span ids included, in the JSON Loki reads", async () => {
    const e = await newEnv({ logNotifier: true });
    const a = await e.client.webhook(mixedBatch(new Date()).webhook);
    expectStatus(a, 200);

    const lines = e.logs().filter((l) => l.msg === LOG_ROUTED || l.msg === LOG_DISPATCHED);
    expect(lines.length).toBeGreaterThan(0);
    for (const line of lines) {
      // JSON.parse keeps the last of two equal keys, so the raw text is what
      // shows a duplicate.
      for (const key of ["trace_id", "span_id", "level", "msg", "time", "fingerprint"]) {
        expect(line.raw.split(`"${key}":`).length - 1, `${key} in ${line.raw}`).toBe(1);
      }
      expect(line.fields.trace_id).toMatch(/^[0-9a-f]{32}$/);
      expect(line.fields.span_id).toMatch(/^[0-9a-f]{16}$/);
    }
  });
});

/** The lines logged with msg, keyed by the alert fingerprint they carry; fails when two carry the same one. */
function linesByFingerprint(e: Env, msg: string): Map<string, LogLine> {
  const out = new Map<string, LogLine>();
  for (const line of e.logs().filter((l) => l.msg === msg)) {
    const fingerprint = String(line.fields.fingerprint ?? "");
    expect(out.has(fingerprint), `two ${JSON.stringify(msg)} lines for alert ${fingerprint}`).toBe(false);
    out.set(fingerprint, line);
  }
  return out;
}
