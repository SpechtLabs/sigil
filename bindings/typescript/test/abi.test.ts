import { describe, expect, test } from "bun:test";

import { ABI_VERSION, Runtime } from "../src/abi.js";
import { SigilError, SigilStoppedError } from "../src/errors.js";
import { fakeModule, type FakeOptions } from "./fake-module.js";

async function instantiate(options: FakeOptions = {}, write?: (fd: 1 | 2, text: string) => void) {
  const module = await WebAssembly.compile(fakeModule(options));
  return Runtime.instantiate(module, { write });
}

describe("Runtime", () => {
  test("round-trips a request through sigil_call and host_call", async () => {
    const runtime = await instantiate();
    const seen: unknown[] = [];
    runtime.hostCall = (req) => {
      seen.push(req);
      return { echoed: req };
    };
    const res = runtime.call({ function: "split", args: ["a,b", ","] });
    expect(seen).toEqual([{ function: "split", args: ["a,b", ","] }]);
    expect(res).toEqual({ result: { echoed: { function: "split", args: ["a,b", ","] } } } as never);
  });

  test("encodes non-ASCII text both ways", async () => {
    const runtime = await instantiate();
    runtime.hostCall = (req) => `${String(req.args[0])} ✓`;
    expect(runtime.call({ function: "f", args: ["Grüße 🌍"] })).toEqual({ result: "Grüße 🌍 ✓" } as never);
  });

  test("turns a throwing host function into an error response", async () => {
    const runtime = await instantiate();
    runtime.hostCall = () => {
      throw new Error("no such tenant");
    };
    expect(runtime.call({ function: "lookup", args: [] })).toEqual({ error: "no such tenant" } as never);
    runtime.hostCall = () => {
      throw "a string";
    };
    expect(runtime.call({ function: "lookup", args: [] })).toEqual({ error: "a string" } as never);
  });

  test("answers an unregistered host function with an error", async () => {
    const runtime = await instantiate();
    expect(runtime.call({ function: "lookup", args: [] })).toEqual({
      error: "no host function lookup is registered",
    } as never);
  });

  test("encodes undefined as null", async () => {
    const runtime = await instantiate();
    runtime.hostCall = () => undefined;
    expect(runtime.call({ function: "f", args: [] })).toEqual({ result: null } as never);
  });

  test("frees the request and the response", async () => {
    const module = await WebAssembly.compile(fakeModule());
    let instance: WebAssembly.Instance | undefined;
    const original = WebAssembly.instantiate;
    // Capture the instance to read the fake's counters.
    const spy = (async (m: WebAssembly.Module, i: WebAssembly.Imports) => {
      instance = await original(m, i);
      return instance;
    }) as typeof WebAssembly.instantiate;
    WebAssembly.instantiate = spy;
    let runtime: Runtime;
    try {
      runtime = await Runtime.instantiate(module);
    } finally {
      WebAssembly.instantiate = original;
    }
    const globals = () => {
      const e = (instance as WebAssembly.Instance).exports as { allocs: WebAssembly.Global; frees: WebAssembly.Global };
      return { allocs: e.allocs.value as number, frees: e.frees.value as number };
    };
    runtime.hostCall = () => 1;
    runtime.call({ function: "f", args: [] });
    // The request and the host's response are allocated; the module
    // frees the host's response, which the fake doesn't do, so the host
    // freed two: the request and the module's response (which is the
    // host's response, forwarded).
    expect(globals()).toEqual({ allocs: 2, frees: 2 });
  });

  test("rejects a busy module", async () => {
    const runtime = await instantiate();
    let inner: unknown;
    runtime.hostCall = () => {
      try {
        runtime.call({ op: "version" });
      } catch (err) {
        inner = err;
      }
      return null;
    };
    runtime.call({ function: "f", args: [] });
    expect(inner).toBeInstanceOf(SigilError);
    expect((inner as Error).message).toContain("busy");
    // It recovers once the outer call is done.
    runtime.hostCall = () => 2;
    expect(runtime.call({ function: "f", args: [] })).toEqual({ result: 2 } as never);
  });

  test("reports a module that exits during initialization, with its standard error", async () => {
    const written: string[] = [];
    const err = await instantiate({ crashOnInit: true }, (fd, text) => written.push(`${fd}:${text}`)).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err).toBeInstanceOf(SigilStoppedError);
    expect(err.message).toBe("the Sigil module stopped: it exited with code 2\npanic: boom");
    expect(err.help).toContain("Sigil.load");
    expect(written).toEqual(["2:panic: boom\n"]);
  });

  test("stops for good after a trap", async () => {
    const runtime = await instantiate({ trapOnCall: true });
    const first = (() => {
      try {
        runtime.call({ op: "version" });
      } catch (e) {
        return e as SigilError;
      }
    })();
    expect(first).toBeInstanceOf(SigilError);
    expect(first).toBeInstanceOf(SigilStoppedError);
    expect(first?.message).toStartWith("the Sigil module stopped: it trapped: ");
    expect(runtime.stopped).toBe(first);
    expect(() => runtime.call({ op: "version" })).toThrow(first as SigilError);
  });

  test("stops for good when a call runs out of stack", async () => {
    // The RangeError unwinds the module mid-call, which Go's runtime can't
    // recover from any more than from a trap.
    const runtime = await instantiate({ recurseOnCall: true });
    let first: unknown;
    try {
      runtime.call({ op: "version" });
    } catch (e) {
      first = e;
    }
    expect(first).toBeInstanceOf(SigilStoppedError);
    const err = first as SigilStoppedError;
    expect(err.message).toStartWith("the Sigil module stopped: it ran out of stack: ");
    expect(err.cause).toBeInstanceOf(RangeError);
    expect(err.help).toContain("load a new one with Sigil.load");
    expect(runtime.stopped).toBe(err);
    expect(() => runtime.call({ op: "version" })).toThrow(err);
  });

  test("a request that isn't JSON is the caller's error, not a stop", async () => {
    const runtime = await instantiate();
    expect(() => runtime.call({ input: { n: 1n } })).toThrow("the request can't be encoded as JSON");
    expect(runtime.stopped).toBeUndefined();
    runtime.hostCall = () => 1;
    expect(runtime.call({ function: "f", args: [] })).toEqual({ result: 1 } as never);
  });

  test("rejects another ABI version", async () => {
    const err = await instantiate({ abi: ABI_VERSION + 1 }).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe("the module speaks ABI version 2, and this package version 1");
  });

  test("rejects a module without the exports", async () => {
    const err = await instantiate({ omitCall: true }).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe("the module doesn't export sigil_call; it isn't a sigil.wasm reactor");
  });

  test("rejects a module with imports it can't provide", async () => {
    const err = await instantiate({ extraImport: true }).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe("the module imports env.surprise, which this package doesn't provide");
  });
});
