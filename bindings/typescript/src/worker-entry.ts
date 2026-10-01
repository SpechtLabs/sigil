// The script the worker helper runs in a Web Worker (or a Node
// worker_threads worker): one Sigil instance, driven by the messages in
// protocol.ts. Importing it anywhere but a worker does nothing useful.

import { connect, type ParentPort, type WorkerScope } from "./channel.js";
import { SigilError } from "./errors.js";
import type { CallMessage, InitMessage, Request, WireError } from "./protocol.js";
import { type Policy, Sigil } from "./sigil.js";
import type { CompileOptions, HostFunction, JsonValue } from "./types.js";

let sigil: Sigil | undefined;
let functions: Record<string, unknown> = {};
const policies = new Map<number, Policy>();

// Listens before the top level ends: a browser delivers the init message
// then, and drops it if nothing listens.
const channel = connect(globalThis as unknown as WorkerScope, parentPort(), (request) => {
  void handle(request).then(
    (value) => channel.post({ id: request.id, ok: true, value }),
    (err: unknown) => channel.post({ id: request.id, ok: false, error: toWire(err) }),
  );
});
void channel.ready;

async function handle(request: Request): Promise<unknown> {
  if (request.method === "init") return init(request);
  if (sigil === undefined) throw new SigilError("the worker received a call before init");
  return call(sigil, request);
}

async function init(request: InitMessage): Promise<null> {
  if (request.functions !== undefined) {
    functions = { ...((await import(/* @vite-ignore */ request.functions)) as Record<string, unknown>) };
  }
  sigil = await Sigil.load(request.wasm);
  return null;
}

function call(s: Sigil, { method, args }: CallMessage): unknown {
  switch (method) {
    case "version":
      return s.version();
    case "check":
      return s.check(...(args as Parameters<Sigil["check"]>));
    case "explain":
      return s.explain(...(args as Parameters<Sigil["explain"]>));
    case "format":
      return s.format(...(args as Parameters<Sigil["format"]>));
    case "compile": {
      const [files, options] = args as [Parameters<Sigil["compile"]>[0], WireCompileOptions | undefined];
      const policy = s.compile(files, { ...options, functions: pick(options?.functions ?? []) });
      const handle = policy.handle as number;
      policies.set(handle, policy);
      return { handle, name: policy.name, diagnostics: policy.diagnostics };
    }
    case "eval": {
      const [handle, input, options] = args as [number, Record<string, JsonValue>, Parameters<Policy["eval"]>[1]];
      return policy(handle).eval(input, options);
    }
    case "explainPolicy":
      return policy(args[0] as number).explain();
    case "release": {
      const handle = args[0] as number;
      policies.get(handle)?.release();
      policies.delete(handle);
      return null;
    }
  }
}

type WireCompileOptions = Omit<CompileOptions, "functions"> & { functions?: string[] };

function policy(handle: number): Policy {
  const p = policies.get(handle);
  if (p === undefined) throw new SigilError(`the worker has no policy with handle ${handle}`);
  return p;
}

/** The named host functions from the worker's functions module. */
function pick(names: string[]): Record<string, HostFunction> {
  const picked: Record<string, HostFunction> = {};
  for (const name of names) {
    const fn = functions[name];
    if (typeof fn !== "function") {
      throw new SigilError(`the worker's functions module doesn't export a function ${name}`, {
        help: "Export it from the module the worker's functions option names.",
      });
    }
    picked[name] = fn as HostFunction;
  }
  return picked;
}

function toWire(err: unknown): WireError {
  if (err instanceof SigilError) {
    return { name: err.name, message: err.message, help: err.help, diagnostics: err.diagnostics };
  }
  return { name: "Error", message: err instanceof Error ? err.message : String(err), diagnostics: [] };
}

/**
 * node:worker_threads' parentPort, or null where there's no such module or
 * this isn't one of its workers. Only a runtime that reports a Node version
 * (Node, Bun, Deno) is asked: a browser would fetch the specifier as a URL,
 * fail, and log a CORS error for it.
 */
async function parentPort(): Promise<ParentPort | null> {
  const runtime = globalThis as { process?: { versions?: { node?: unknown } } };
  if (typeof runtime.process?.versions?.node !== "string") return null;
  try {
    const specifier = "node:worker_threads";
    return ((await import(/* @vite-ignore */ specifier)) as { parentPort: ParentPort | null }).parentPort;
  } catch {
    return null; // Not a server-side runtime.
  }
}
