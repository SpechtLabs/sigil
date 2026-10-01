// A workaround for a bug in @spechtlabs/sigil 0.6.2: in a browser,
// SigilWorker never starts, and its first call fails with "the Sigil worker
// didn't answer init within 30000 ms". worker-entry starts listening for
// messages only after `import("node:worker_threads")` has failed, and a
// browser drops a worker's messages that arrive while nothing listens, the
// helper's init among them.
//
// This module holds those early messages back, then loads the package's
// worker entry and replays them to its listener. It loads the entry as a
// worker chunk of its own, by URL: the package declares "sideEffects":
// false, so a plain import of it would be tree-shaken out of the build,
// and the worker would never start the engine.
//
// To delete it once the docs pin a release with the fix, remove this file
// and import "@spechtlabs/sigil/worker-entry?worker" in engine.ts instead of
// "./worker.ts?worker".

import entry from "@spechtlabs/sigil/worker-entry?worker&url";

const held: MessageEvent[] = [];
const hold = (e: MessageEvent) => held.push(e);
const add = self.addEventListener.bind(self);
self.addEventListener("message", hold);

self.addEventListener = ((type: string, listener: EventListenerOrEventListenerObject, options?: AddEventListenerOptions) => {
  add(type, listener, options);
  if (type !== "message") return;
  self.addEventListener = add;
  self.removeEventListener("message", hold);
  for (const e of held.splice(0)) {
    if (typeof listener === "function") listener(e);
    else listener.handleEvent(e);
  }
}) as typeof self.addEventListener;

await import(/* @vite-ignore */ entry);
