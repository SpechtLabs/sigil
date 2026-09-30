// The script the worker helper runs in a Web Worker (or a Node
// worker_threads worker): one Sigil instance, driven by the messages in
// protocol.ts. Importing it anywhere but a worker does nothing useful.

import { SigilError } from "./errors.js";
import type { CallMessage, InitMessage, Reply, Request, WireError } from "./protocol.js";
import { type Policy, Sigil } from "./sigil.js";
import type { CompileOptions, HostFunction, JsonValue } from "./types.js";

interface Port {
  post(reply: Reply): void;
  listen(handler: (request: Request) => void): void;
}

let sigil: Sigil | undefined;
let functions: Record<string, unknown> = {};
const policies = new Map<number, Policy>();

void connect().then((port) =>
  port.listen((request) => {
    void handle(request).then(
      (value) => port.post({ id: request.id, ok: true, value }),
      (err: unknown) => port.post({ id: request.id, ok: false, error: toWire(err) }),
    );
  }),
);

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

/** The channel to the client: the worker global scope, or Node's parentPort. */
async function connect(): Promise<Port> {
  const scope = globalThis as unknown as {
    postMessage?: (msg: unknown) => void;
    addEventListener?: (type: "message", listener: (ev: { data: Request }) => void) => void;
  };
  if (typeof scope.postMessage === "function" && typeof scope.addEventListener === "function") {
    const { postMessage, addEventListener } = scope as Required<typeof scope>;
    return {
      post: (reply) => postMessage.call(globalThis, reply),
      listen: (handler) => addEventListener.call(globalThis, "message", (ev) => handler(ev.data)),
    };
  }
  const specifier = "node:worker_threads";
  const { parentPort } = (await import(/* @vite-ignore */ specifier)) as {
    parentPort: { postMessage(msg: unknown): void; on(type: "message", listener: (data: Request) => void): void } | null;
  };
  if (parentPort === null) throw new Error("worker-entry runs only inside a worker");
  return { post: (reply) => parentPort.postMessage(reply), listen: (handler) => parentPort.on("message", handler) };
}
