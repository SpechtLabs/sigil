// GET /api/v1/events: the console's live feed, as Server-Sent Events. Each
// routed alert is an `alert` event whose id is its history id, so a browser
// that reconnects with Last-Event-ID (or ?after=) gets what it missed from
// the ring buffer first. Policy loads are `reload` events. A comment every
// 15 seconds keeps proxies from closing an idle stream.
//
// The hub subscribes to the history once and encodes each event once, for
// every stream. A stream whose client doesn't keep up is closed once its
// queue passes a small bound, rather than buffering without limit: the
// browser reconnects and replays from the ring buffer. The number of open
// streams is capped, and every stream ends when the service shuts down.

import type { History, HistoryEvent } from "../dispatch/history";
import { humane } from "../errors";
import type { Metrics } from "../telemetry/types";
import { errorJSON } from "./respond";

const HEARTBEAT_MS = 15_000;

/** How many events a stream may have queued before it's closed as too slow. */
export const MAX_QUEUED_EVENTS = 256;

const HEADERS = {
  "content-type": "text/event-stream; charset=utf-8",
  "cache-control": "no-cache, no-transform",
  connection: "keep-alive",
  // nginx and similar proxies buffer responses unless told not to.
  "x-accel-buffering": "no",
};

interface Stream {
  send(frame: Uint8Array): void;
  close(): void;
}

export interface EventHubOptions {
  history: History;
  metrics: Metrics;
  /** How many streams may be open at once. */
  maxStreams: number;
  /** Ends every stream when it aborts. */
  shutdown: AbortSignal;
}

export class EventHub {
  readonly #opts: EventHubOptions;
  readonly #streams = new Set<Stream>();
  readonly #encoder = new TextEncoder();
  #unsubscribe: (() => void) | undefined;

  constructor(opts: EventHubOptions) {
    this.#opts = opts;
    opts.shutdown.addEventListener("abort", () => this.close(), { once: true });
  }

  /** How many streams are open. */
  get open(): number {
    return this.#streams.size;
  }

  /** Answers one GET /api/v1/events. */
  stream(req: Request): Response {
    const { history, metrics, maxStreams, shutdown } = this.#opts;
    if (shutdown.aborted) {
      return errorJSON(
        503,
        humane("alertrouter is shutting down", "reconnect; another replica, or this one once restarted, answers"),
      );
    }
    if (this.#streams.size >= maxStreams) {
      metrics.observeStreamClosed("limit");
      return errorJSON(
        503,
        humane(
          `${maxStreams} console event streams are open already, the most alertrouter serves`,
          "close console tabs you don't need, or raise ALERTROUTER_MAX_EVENT_STREAMS",
        ),
      );
    }

    const url = new URL(req.url);
    const after = Number(req.headers.get("last-event-id") ?? url.searchParams.get("after") ?? "0");
    let stream: Stream | undefined;
    const body = new ReadableStream<Uint8Array>(
      {
        start: (controller) => {
          let closed = false;
          const heartbeat = setInterval(() => send(this.#encoder.encode(": keep-alive\n\n")), HEARTBEAT_MS);
          heartbeat.unref?.();
          const close = () => {
            if (closed) return;
            closed = true;
            clearInterval(heartbeat);
            req.signal.removeEventListener("abort", close);
            if (stream !== undefined) this.#streams.delete(stream);
            this.#idle();
            try {
              controller.close();
            } catch {
              // Already closed by the client.
            }
          };
          const send = (frame: Uint8Array) => {
            if (closed) return;
            // desiredSize goes negative by the number of events queued past
            // the high-water mark; a client that far behind reconnects and
            // replays from the ring buffer instead.
            if ((controller.desiredSize ?? 0) < -MAX_QUEUED_EVENTS) {
              metrics.observeStreamClosed("slow");
              close();
              return;
            }
            try {
              controller.enqueue(frame);
            } catch {
              close();
            }
          };
          stream = { send, close };
          this.#streams.add(stream);
          this.#subscribe();
          send(this.#encoder.encode("retry: 3000\n\n"));
          for (const entry of history.entries(Number.isFinite(after) ? after : 0)) {
            send(this.#frame({ type: "alert", entry }));
          }
          req.signal.addEventListener("abort", close);
          if (req.signal.aborted) close();
        },
        cancel: () => stream?.close(),
      },
      new CountQueuingStrategy({ highWaterMark: 1 }),
    );
    return new Response(body, { status: 200, headers: HEADERS });
  }

  /** Ends every stream. */
  close(): void {
    for (const s of [...this.#streams]) s.close();
    this.#idle();
  }

  #frame(event: HistoryEvent): Uint8Array {
    const text =
      event.type === "alert"
        ? `id: ${event.entry.id}\nevent: alert\ndata: ${JSON.stringify(event.entry)}\n\n`
        : `event: reload\ndata: ${JSON.stringify(event.reload)}\n\n`;
    return this.#encoder.encode(text);
  }

  // One subscription for every stream, taken while any is open, so an event
  // is encoded once however many consoles watch.
  #subscribe(): void {
    if (this.#unsubscribe !== undefined) return;
    this.#unsubscribe = this.#opts.history.subscribe((event) => {
      if (this.#streams.size === 0) return;
      const frame = this.#frame(event);
      for (const s of [...this.#streams]) s.send(frame);
    });
  }

  #idle(): void {
    if (this.#streams.size > 0 || this.#unsubscribe === undefined) return;
    this.#unsubscribe();
    this.#unsubscribe = undefined;
  }
}
