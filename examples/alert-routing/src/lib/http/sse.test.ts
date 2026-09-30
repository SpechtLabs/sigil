import { describe, expect, spyOn, test } from "bun:test";

import { History, type HistoryEntry } from "../dispatch/history";
import { fakeTelemetry } from "../testing";
import { EventHub, MAX_QUEUED_EVENTS } from "./sse";

function entry(n: number): Omit<HistoryEntry, "id"> {
  return {
    at: "2026-01-01T00:00:00.000Z",
    source: "webhook",
    severity: "info",
    result: { fingerprint: `f${n}`, alertname: "X", status: "routed" },
    notification: { destination: "#alerts", status: "sent" },
  };
}

function setup(maxStreams = 4) {
  const history = new History(10);
  const shutdown = new AbortController();
  const t = fakeTelemetry();
  const hub = new EventHub({ history, metrics: t.telemetry.metrics, maxStreams, shutdown: shutdown.signal });
  const open = (headers: Record<string, string> = {}) => {
    const abort = new AbortController();
    const res = hub.stream(new Request("http://x/api/v1/events", { headers, signal: abort.signal }));
    return { res, abort };
  };
  return { history, shutdown, hub, open, ...t };
}

async function readUntil(reader: ReadableStreamDefaultReader<Uint8Array>, needle: string): Promise<string> {
  const decoder = new TextDecoder();
  let text = "";
  while (!text.includes(needle)) {
    const { value, done } = await reader.read();
    if (done) break;
    text += decoder.decode(value);
  }
  return text;
}

describe("EventHub", () => {
  test("replays the ring buffer after Last-Event-ID, then streams new entries", async () => {
    const { history, open } = setup();
    history.add(entry(1));
    history.add(entry(2));
    const { res } = open({ "last-event-id": "1" });
    const reader = (res.body as ReadableStream<Uint8Array>).getReader();
    const replay = await readUntil(reader, "id: 2\n");
    expect(replay).not.toContain("id: 1\n");
    history.add(entry(3));
    expect(await readUntil(reader, "id: 3\n")).toContain("event: alert");
  });

  test("encodes an event once for every stream", async () => {
    const { history, open } = setup();
    const readers = [open(), open(), open()].map(({ res }) => (res.body as ReadableStream<Uint8Array>).getReader());
    const stringify = spyOn(JSON, "stringify");
    history.add(entry(1));
    expect(stringify.mock.calls.length).toBe(1);
    stringify.mockRestore();
    for (const r of readers) expect(await readUntil(r, "id: 1\n")).toContain('"fingerprint":"f1"');
  });

  test("refuses a stream past the limit with 503", async () => {
    const { open, metricsText } = setup(2);
    open();
    open();
    const third = open().res;
    expect(third.status).toBe(503);
    expect(((await third.json()) as { error: { message: string } }).error.message).toContain(
      "2 console event streams are open already",
    );
    expect(await metricsText()).toContain('alertrouter_event_streams_closed_total{reason="limit"} 1');
  });

  test("closes a stream whose client doesn't read, instead of buffering without limit", async () => {
    const { history, open, hub, metricsText } = setup();
    open(); // never read
    expect(hub.open).toBe(1);
    for (let i = 0; i < MAX_QUEUED_EVENTS + 10; i++) history.add(entry(i));
    expect(hub.open).toBe(0);
    expect(await metricsText()).toContain('alertrouter_event_streams_closed_total{reason="slow"} 1');
    expect(history.subscribers).toBe(0);
  });

  test("a client that leaves frees its place", async () => {
    const { open, hub, history } = setup(1);
    const { abort } = open();
    expect(hub.open).toBe(1);
    abort.abort();
    expect(hub.open).toBe(0);
    expect(history.subscribers).toBe(0);
    expect(open().res.status).toBe(200);
  });

  test("shutdown ends every stream and refuses new ones", async () => {
    const { open, hub, shutdown } = setup();
    const reader = (open().res.body as ReadableStream<Uint8Array>).getReader();
    shutdown.abort();
    expect(hub.open).toBe(0);
    for (;;) {
      const { done } = await reader.read();
      if (done) break;
    }
    expect(open().res.status).toBe(503);
  });
});
