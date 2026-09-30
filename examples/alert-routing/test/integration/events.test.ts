// GET /api/v1/events streams the console's live feed. It isn't part of the
// Go contract, but it holds a connection and a queue per viewer, so the
// number of streams is capped and a viewer that stops reading is dropped
// instead of buffered for without bound. The lead's review, item 5.
import { afterAll, afterEach, describe, expect, test } from "bun:test";
import { expectStatus, PATH_EVENTS } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import {
  alertLabels,
  CHECKOUT_ERROR_RATE,
  firing,
  firingAlert,
  MINUTE,
  newWebhook,
  SEVERITY_CRITICAL,
  TEAM_CHECKOUT,
} from "../fixture/requests";
import type { ErrorResponse } from "../fixture/wire";
import { closeEnvs, newEnv, releaseTelemetry } from "./env";

const METRIC_STREAMS_CLOSED = "alertrouter_event_streams_closed_total";

/** How many events a stream may fall behind before it's closed. */
const MAX_QUEUED_EVENTS = 256;

afterEach(closeEnvs);
afterAll(releaseTelemetry);

/** Opens a stream over HTTP and returns the response with its body unread. */
async function openStream(baseURL: string, signal: AbortSignal): Promise<Response> {
  return fetch(baseURL + PATH_EVENTS, { signal });
}

describe("The console's event stream", () => {
  test("sends each routed alert as an alert event with its history id", async () => {
    const e = await newEnv();
    const abort = new AbortController();
    const res = await openStream(e.client.baseURL, abort.signal);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toStartWith("text/event-stream");

    expectStatus(await e.client.route(TEAM_CHECKOUT, firingAlert(CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL)), 200);

    const reader = res.body?.getReader();
    if (reader === undefined) throw new Error("the stream has no body");
    let text = "";
    await eventually(
      async () => {
        const { value } = await reader.read();
        text += new TextDecoder().decode(value);
        expect(text).toMatch(/id: \d+\nevent: alert\ndata: \{.*"alertname":"CheckoutErrorRate"/);
      },
      { timeoutMs: 3_000 },
    );
    abort.abort();
  });

  test("refuses a stream past the cap with 503, and counts it", async () => {
    const e = await newEnv({ maxEventStreams: 2 });
    const abort = new AbortController();
    const open = [await openStream(e.client.baseURL, abort.signal), await openStream(e.client.baseURL, abort.signal)];
    expect(open.map((r) => r.status)).toEqual([200, 200]);

    const refused = await e.client.get(PATH_EVENTS);
    expectStatus(refused, 503);
    expect((JSON.parse(refused.body) as ErrorResponse).error?.advice?.join(" ")).toContain(
      "ALERTROUTER_MAX_EVENT_STREAMS",
    );
    expect((await e.families()).value(METRIC_STREAMS_CLOSED, { reason: "limit" })).toBe(1);

    // A viewer that leaves frees its place.
    abort.abort();
    await eventually(() => expect(e.service.events.open).toBe(0));
    const again = new AbortController();
    expect((await openStream(e.client.baseURL, again.signal)).status).toBe(200);
    again.abort();
  });

  test("closes a stream whose viewer stops reading, instead of queueing for it without bound", async () => {
    const e = await newEnv();
    // Asked of the hub directly and never read, so nothing drains the
    // stream's queue, whatever the socket buffers would have absorbed.
    const stalled = e.service.events.stream(new Request(`${e.client.baseURL}${PATH_EVENTS}`));
    expect(stalled.status).toBe(200);
    expect(e.service.events.open).toBe(1);

    const minuteAgo = new Date(Date.now() - MINUTE);
    const alerts = Array.from({ length: MAX_QUEUED_EVENTS + 44 }, (_, i) =>
      firing(`feed-${i}`, alertLabels(TEAM_CHECKOUT, CHECKOUT_ERROR_RATE, SEVERITY_CRITICAL), minuteAgo),
    );
    expectStatus(await e.client.webhook(newWebhook(...alerts)), 200);

    await eventually(() => expect(e.service.events.open).toBe(0));
    expect((await e.families()).value(METRIC_STREAMS_CLOSED, { reason: "slow" })).toBe(1);
  });
});
