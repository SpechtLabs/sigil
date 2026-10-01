// Loading the engine: sigil.wasm, compiled while it downloads, then handed
// to a worker that runs every call, so a slow policy can't freeze the page.
// Only the playground imports this module, so no other page downloads the
// engine or even this code.

import { SigilWorker } from "@spechtlabs/sigil/worker";
// Vite bundles the package's worker entry as an ES module worker of its own
// (worker.format in config.ts) and gives sigil.wasm a content-hashed URL.
import SigilEntryWorker from "@spechtlabs/sigil/worker-entry?worker";
import wasmUrl from "@spechtlabs/sigil/sigil.wasm?url";

// The module's size in bytes, uncompressed, which is what a download
// counts however the server compressed it. config.ts defines it.
declare const __SIGIL_WASM_SIZE__: number;

export interface Progress {
  loaded: number;
  total: number;
}

let engine: Promise<SigilWorker> | undefined;

/**
 * The engine, loaded once per page load: a later visit to the playground
 * reuses the worker. A failed load is forgotten, so retrying starts over.
 */
export function loadEngine(onProgress: (p: Progress) => void): Promise<SigilWorker> {
  engine ??= start(onProgress).catch((err: unknown) => {
    engine = undefined;
    throw err;
  });
  return engine;
}

async function start(onProgress: (p: Progress) => void): Promise<SigilWorker> {
  const module = await compileWithProgress(onProgress);
  const sigil = new SigilWorker({ wasm: module, worker: () => new SigilEntryWorker() });
  // The first call starts the worker and instantiates the module in it.
  await sigil.version();
  return sigil;
}

async function compileWithProgress(onProgress: (p: Progress) => void): Promise<WebAssembly.Module> {
  const res = await fetch(wasmUrl);
  if (!res.ok || res.body === null) throw new Error(`downloading the engine failed: HTTP ${res.status}`);
  const total = __SIGIL_WASM_SIZE__;
  let loaded = 0;
  onProgress({ loaded, total });
  const counted = res.body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        loaded += chunk.byteLength;
        onProgress({ loaded: Math.min(loaded, total), total });
        controller.enqueue(chunk);
      },
    }),
  );
  // compileStreaming compiles while the bytes arrive; it needs the wasm
  // content type, which the counting stream's Response sets itself.
  return WebAssembly.compileStreaming(new Response(counted, { headers: { "Content-Type": "application/wasm" } }));
}
