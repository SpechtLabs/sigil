// The worker's channel to the client (channel.ts) against a fake worker
// scope and parentPort, settled by hand: messages that arrive before the
// runtime is known must reach the handler, on whichever side they belong.

import { describe, expect, test } from "bun:test";

import { connect, type ParentPort, type WorkerScope } from "../src/channel.js";
import type { Reply, Request } from "../src/protocol.js";

/** A Web Worker's global scope: it delivers messages only to the listeners it has. */
class FakeScope implements WorkerScope {
  readonly posted: unknown[] = [];
  readonly #listeners: ((ev: { data: Request }) => void)[] = [];

  addEventListener(_type: "message", listener: (ev: { data: Request }) => void): void {
    this.#listeners.push(listener);
  }

  postMessage(msg: unknown): void {
    expect(this).toBeInstanceOf(FakeScope);
    this.posted.push(msg);
  }

  deliver(data: Request): void {
    for (const l of this.#listeners) l({ data });
  }
}

class FakePort implements ParentPort {
  readonly posted: unknown[] = [];
  #listener: ((data: Request) => void) | undefined;

  on(_type: "message", listener: (data: Request) => void): void {
    this.#listener = listener;
  }

  postMessage(msg: unknown): void {
    this.posted.push(msg);
  }

  deliver(data: Request): void {
    this.#listener?.(data);
  }
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

const init: Request = { id: 1, method: "init", wasm: "https://example.com/sigil.wasm" };
const version: Request = { id: 2, method: "version", args: [] };
const reply: Reply = { id: 1, ok: true, value: null };

describe("connect", () => {
  test("in a browser, hands over the messages that arrived before node:worker_threads failed to load", async () => {
    const scope = new FakeScope();
    const probe = deferred<ParentPort | null>();
    const got: Request[] = [];
    const channel = connect(scope, probe.promise, (r) => got.push(r));
    // The browser delivers init as soon as the module's top level has run,
    // long before the import of node:worker_threads has failed.
    scope.deliver(init);
    expect(got).toEqual([]);
    probe.resolve(null);
    await channel.ready;
    scope.deliver(version);
    expect(got).toEqual([init, version]);
    channel.post(reply);
    expect(scope.posted).toEqual([reply]);
  });

  test("with a parentPort, listens there and drops what the web scope heard", async () => {
    const scope = new FakeScope();
    const port = new FakePort();
    const probe = deferred<ParentPort | null>();
    const got: Request[] = [];
    const channel = connect(scope, probe.promise, (r) => got.push(r));
    scope.deliver(version);
    probe.resolve(port);
    await channel.ready;
    port.deliver(init);
    scope.deliver(version);
    expect(got).toEqual([init]);
    channel.post(reply);
    expect(port.posted).toEqual([reply]);
    expect(scope.posted).toEqual([]);
  });

  test("with a parentPort and no web scope, as in a Node worker", async () => {
    const port = new FakePort();
    const got: Request[] = [];
    const channel = connect({}, Promise.resolve(port), (r) => got.push(r));
    await channel.ready;
    port.deliver(init);
    expect(got).toEqual([init]);
  });

  test("outside a worker, fails", async () => {
    const channel = connect({}, Promise.resolve(null), () => {});
    await expect(channel.ready).rejects.toThrow("worker-entry runs only inside a worker");
  });
});
