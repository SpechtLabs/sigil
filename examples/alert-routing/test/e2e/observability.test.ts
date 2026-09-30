import { beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { firingCount, mixedBatch, uniqueTag } from "../fixture/cases";
import { decode, expectStatus } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { expectBatch } from "../fixture/expect";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  CHECKOUT_LATENCY,
  firing,
  firingAlert,
  MINUTE,
  newWebhook,
  SEVERITY_CRITICAL,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import { alertrouter, BACKEND, E2E, grafana, loki, mimir, pyroscope, SPEC_TIMEOUT, tempo, waitReady } from "./e2e";

const ROUTE_SPAN = "alertrouter.route";
/** The HTTP server span of a webhook, named after its route. */
const WEBHOOK_SERVER_SPAN = "POST /api/v1/alerts";

interface PromQueryResponse {
  status: string;
  data: { resultType: string; result: { metric: Record<string, string>; value: [number, string] }[] };
}

interface TempoSearchResponse {
  traces?: { traceID: string }[];
}

/** Tempo returns OTLP JSON, with resource batches and instrumentation scopes. */
interface TempoTraceResponse {
  batches?: {
    scopeSpans?: {
      spans?: { name: string; attributes?: { key: string; value: Record<string, unknown> }[] }[];
    }[];
  }[];
}

interface LokiResponse {
  status: string;
  data: { result: { values: [string, string][] }[] };
}

describe.skipIf(!E2E)("e2e", () => {
  beforeAll(waitReady, SPEC_TIMEOUT);

  describe("Observability backends", () => {
    beforeEach(async () => {
      expectStatus(await alertrouter.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);
    }, SPEC_TIMEOUT);

    describe("Mimir", () => {
      test(
        "receives Alloy's alertrouter scrape",
        async () => {
          await eventually(async () => {
            const res = await promQuery(`up{job="alertrouter"}`);
            expect(res.data.result.length).toBeGreaterThan(0);
            expect(res.data.result[0]?.value[1]).toBe("1");
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );

      test.each([
        ["the decisions", "sum by (team) (alertrouter_decisions_total)"],
        ["the notifications", "sum by (decision) (alertrouter_notifications_total)"],
        ["the loaded policies", `alertrouter_policy_loaded_info{team="checkout"}`],
      ])(
        "stores the routing counters: %s",
        async (_, query) => {
          await eventually(async () => {
            const res = await promQuery(query);
            expect(res.data.resultType).toBe("vector");
            expect(res.data.result.length).toBeGreaterThan(0);
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );
    });

    describe("Tempo", () => {
      test(
        "stores the route span with what the policy decided",
        async () => {
          const keys = [
            "alert.name",
            "alert.severity",
            "alertrouter.team",
            "sigil.policy",
            "sigil.decision",
            "sigil.reason",
            "sigil.candidates",
          ];
          await eventually(async () => {
            const q = new URLSearchParams({
              q: `{resource.service.name = "alertrouter" && name = "${ROUTE_SPAN}"}`,
              limit: "10",
            });
            const search = await tempo.get(`/api/search?${q}`);
            expectStatus(search, 200);
            const traceID = decode<TempoSearchResponse>(search).traces?.[0]?.traceID;
            expect(traceID).toBeDefined();
            const trace = await tempo.get(`/api/traces/${traceID}`);
            expectStatus(trace, 200);
            const spans = spanTags(decode<TempoTraceResponse>(trace), ROUTE_SPAN);
            expect(spans.some((tags) => keys.every((k) => k in tags))).toBe(true);
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );

      test(
        "stores one route span per firing alert of a webhook, under the request's span",
        async () => {
          // The request carries its own trace ID, so the spec reads that
          // request's trace and nothing else.
          const { client, traceID } = alertrouter.traced();
          const tag = uniqueTag();
          const batch = mixedBatch(new Date(), tag);
          const a = await client.webhook(batch.webhook);
          expectBatch(batch, a, a.out);

          // The server span ends last, after every route span, so once it is
          // stored the route spans are too.
          const trace = await eventually(async () => {
            const res = await tempo.get(`/api/traces/${traceID}`);
            expectStatus(res, 200);
            const t = decode<TempoTraceResponse>(res);
            expect(spanTags(t, WEBHOOK_SERVER_SPAN)).toHaveLength(1);
            return t;
          }, BACKEND);

          const routes = spanTags(trace, ROUTE_SPAN);
          expect(routes).toHaveLength(firingCount(batch));
          expect(routes.some((tags) => tags["alert.fingerprint"]?.stringValue === `checkout-critical-${tag}`)).toBe(
            true,
          );
        },
        SPEC_TIMEOUT,
      );
    });

    describe("Loki", () => {
      test(
        "logs a routed alert at info and an unreadable one at warn, each with its notification in the alert's trace",
        async () => {
          // The batch carries its own trace ID, and the fingerprints are the
          // trace's, so the spec reads its own lines and nothing else.
          const { client, traceID } = alertrouter.traced();
          const routedFP = `e2e-routed-${traceID.slice(0, 16)}`;
          const invalidFP = `e2e-invalid-${traceID.slice(0, 16)}`;
          const started = new Date(Date.now() - MINUTE);
          const a = await client.webhook(
            newWebhook(
              firing(routedFP, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), started),
              firing(invalidFP, alertLabels(TEAM_CHECKOUT, CHECKOUT_LATENCY, "urgent"), started),
            ),
          );
          expectStatus(a, 200);
          expect(a.out.routed).toBe(1);

          await eventually(async () => {
            const lines = await lokiLines(`{service_name="alertrouter"} |= "${traceID}"`);
            for (const [fingerprint, level] of [
              [routedFP, "info"],
              [invalidFP, "warn"],
            ] as const) {
              const routed = findLine(lines, "alert routed", fingerprint);
              expect(routed, `no alert routed line for ${fingerprint}`).toBeDefined();
              expect(routed?.level).toBe(level);
              expect(routed?.trace_id).toBe(traceID);

              // The notification is dispatched inside the alert's route span,
              // so its line carries the same trace and span.
              const dispatched = findLine(lines, "notification dispatched", fingerprint);
              expect(dispatched, `no notification dispatched line for ${fingerprint}`).toBeDefined();
              expect(dispatched?.trace_id).toBe(traceID);
              expect(dispatched?.span_id).toBe(routed?.span_id);
              expect(dispatched?.decision).toBe(routed?.decision);
            }
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );

      test(
        "stores one routing log line per alert whose trace ID resolves in Tempo",
        async () => {
          const traceID = await eventually(async () => {
            const q = new URLSearchParams({
              query: `{service_name="alertrouter"} | json | msg="alert routed" | team="checkout" | trace_id!=""`,
              limit: "1",
            });
            const res = await loki.get(`/loki/api/v1/query_range?${q}`);
            expectStatus(res, 200);
            const logs = decode<LokiResponse>(res);
            expect(logs.status).toBe("success");
            const entry = logs.data.result[0]?.values[0];
            expect(entry).toHaveLength(2);
            const fields = JSON.parse(entry?.[1] ?? "{}") as Record<string, unknown>;
            expect(fields.team).toBe("checkout");
            expect(fields).toHaveProperty("decision");
            expect(fields.trace_id).toMatch(/^[0-9a-f]{32}$/);
            return fields.trace_id as string;
          }, BACKEND);

          // Pin the log's trace while Tempo catches up. Repeatedly choosing the
          // newest log under load could keep outrunning trace ingestion.
          await eventually(async () => {
            const res = await tempo.get(`/api/traces/${traceID}`);
            expectStatus(res, 200);
            expect(spanTags(decode<TempoTraceResponse>(res), ROUTE_SPAN).length).toBeGreaterThan(0);
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );
    });

    describe("Pyroscope", () => {
      // The Go service pushed goroutine profiles; a Node process has none.
      // @pyroscope/nodejs pushes wall-clock profiles with CPU time
      // (wall:cpu:nanoseconds:...) and heap profiles
      // (memory:inuse_space:bytes:...), so the spec expects one of each.
      test(
        "receives alertrouter CPU and heap profiles",
        async () => {
          await eventually(async () => {
            const res = await pyroscope.postJSON("/querier.v1.QuerierService/LabelValues", {
              name: "__profile_type__",
              matchers: [`{service_name="alertrouter"}`],
              start: Date.now() - MINUTE,
              end: Date.now(),
            });
            expectStatus(res, 200);
            const names = decode<{ names?: string[] }>(res).names ?? [];
            expect(
              names.some((n) => n.startsWith("wall:cpu:")),
              names.join(", "),
            ).toBe(true);
            expect(
              names.some((n) => n.startsWith("memory:inuse_space:")),
              names.join(", "),
            ).toBe(true);
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );
    });

    describe("Grafana", () => {
      test.each(["mimir", "tempo", "loki", "pyroscope"])(
        "connects to the provisioned datasource %s",
        async (uid) => {
          await eventually(async () => {
            const res = await grafana.get(`/api/datasources/uid/${uid}/health`);
            expectStatus(res, 200);
            expect(decode<{ status: string }>(res).status).toBe("OK");
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );

      test(
        "provisions the alertrouter dashboard",
        async () => {
          await eventually(async () => {
            const res = await grafana.get("/api/dashboards/uid/alertrouter");
            expectStatus(res, 200);
            const { dashboard } = decode<{ dashboard: { uid: string; panels?: unknown[] } }>(res);
            expect(dashboard.uid).toBe("alertrouter");
            expect(dashboard.panels?.length ?? 0).toBeGreaterThan(0);
          }, BACKEND);
        },
        SPEC_TIMEOUT,
      );
    });
  });
});

async function promQuery(query: string): Promise<PromQueryResponse> {
  const res = await mimir.get(`/prometheus/api/v1/query?${new URLSearchParams({ query })}`);
  expectStatus(res, 200);
  const out = decode<PromQueryResponse>(res);
  expect(out.status).toBe("success");
  return out;
}

/** Runs a LogQL query and returns every line it found, each decoded from alertrouter's JSON. */
async function lokiLines(query: string): Promise<Record<string, unknown>[]> {
  const res = await loki.get(`/loki/api/v1/query_range?${new URLSearchParams({ query, limit: "1000" })}`);
  expectStatus(res, 200);
  const logs = decode<LokiResponse>(res);
  expect(logs.status).toBe("success");
  return logs.data.result.flatMap((stream) =>
    stream.values.map(([, line]) => JSON.parse(line) as Record<string, unknown>),
  );
}

/** The line logged with msg for the alert with fingerprint, or undefined. */
function findLine(lines: Record<string, unknown>[], msg: string, fingerprint: string) {
  return lines.find((l) => l.msg === msg && l.fingerprint === fingerprint);
}

/** The attributes of every span called name, keyed by attribute name, each an OTLP AnyValue. */
function spanTags(trace: TempoTraceResponse, name: string): Record<string, Record<string, unknown>>[] {
  const out: Record<string, Record<string, unknown>>[] = [];
  for (const batch of trace.batches ?? []) {
    for (const scope of batch.scopeSpans ?? []) {
      for (const span of scope.spans ?? []) {
        if (span.name !== name) continue;
        out.push(Object.fromEntries((span.attributes ?? []).map((a) => [a.key, a.value])));
      }
    }
  }
  return out;
}
