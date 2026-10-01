// The worker's channel to the client, for worker-entry.ts. A dedicated
// Web Worker delivers the messages queued for it as soon as its module's
// top level has run, so the channel listens on the worker scope at once
// and holds what arrives until it knows whether the messages come there
// or on node:worker_threads' parentPort, which takes an await to find out.

import type { Reply, Request } from "./protocol.js";

/** The part of a Web Worker's global scope the channel uses. */
export interface WorkerScope {
  postMessage?: (msg: unknown) => void;
  addEventListener?: (type: "message", listener: (ev: { data: Request }) => void) => void;
}

/** The part of node:worker_threads' parentPort the channel uses. */
export interface ParentPort {
  postMessage(msg: unknown): void;
  on(type: "message", listener: (data: Request) => void): void;
}

/** An open channel. */
export interface Channel {
  /** Sends a reply to the client. Only a request the channel delivered is answered, so it's settled by then. */
  post(reply: Reply): void;
  /** Settles once the channel knows where messages come from; rejects outside a worker. */
  ready: Promise<void>;
}

/**
 * Opens the channel and hands each request to handler, in order. It
 * listens on scope synchronously, so call it at the module's top level,
 * and holds those messages until parentPort settles. A parentPort comes
 * first where there is one: a worker_threads worker on Bun also has the
 * web scope's postMessage, but hears its parent only on parentPort, and
 * what the scope held is dropped. Without one (a browser, which has no
 * node:worker_threads) the scope is the channel, and what it held is
 * handed over first.
 */
export function connect(scope: WorkerScope, parentPort: Promise<ParentPort | null>, handler: (request: Request) => void): Channel {
  let source: "pending" | "scope" | "port" = "pending";
  let port: ParentPort | null = null;
  const held: Request[] = [];
  const { postMessage, addEventListener } = scope;
  const web = typeof postMessage === "function" && typeof addEventListener === "function";
  if (web) {
    addEventListener.call(scope, "message", (ev) => {
      if (source === "pending") held.push(ev.data);
      else if (source === "scope") handler(ev.data);
    });
  }
  const ready = parentPort.then((p) => {
    if (p !== null) {
      port = p;
      source = "port";
      held.length = 0;
      p.on("message", handler);
      return;
    }
    if (!web) throw new Error("worker-entry runs only inside a worker");
    source = "scope";
    for (const request of held.splice(0)) handler(request);
  });
  return {
    ready,
    post(reply) {
      if (port !== null) port.postMessage(reply);
      else postMessage?.call(scope, reply);
    },
  };
}
