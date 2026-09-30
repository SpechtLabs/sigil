// The parts of the Sigil class that don't need the real module: loading
// from every source kind, output handling and envelope unwrapping. The
// fake module (fake-module.ts) stands in for sigil.wasm.

import { afterAll, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import { SigilError, SigilStoppedError } from "../src/errors.js";
import { compileModule, Sigil, unwrap } from "../src/sigil.js";
import { fakeModule } from "./fake-module.js";

const dir = mkdtempSync(join(tmpdir(), "sigil-ts-"));
afterAll(() => rmSync(dir, { recursive: true, force: true }));

describe("compileModule", () => {
  const bytes = fakeModule();
  const file = join(dir, "fake.wasm");
  writeFileSync(file, bytes);
  const server = Bun.serve({
    port: 0,
    fetch(req) {
      const path = new URL(req.url).pathname;
      if (path === "/typed.wasm") return new Response(bytes, { headers: { "content-type": "application/wasm" } });
      if (path === "/untyped.wasm") return new Response(bytes, { headers: { "content-type": "application/octet-stream" } });
      return new Response("not found", { status: 404, statusText: "Not Found" });
    },
  });
  afterAll(() => server.stop(true));

  const sources: [string, () => Parameters<typeof compileModule>[0]][] = [
    ["an ArrayBuffer", () => bytes.slice().buffer],
    ["a Uint8Array", () => bytes],
    ["a Module", () => new WebAssembly.Module(bytes)],
    ["a file URL", () => pathToFileURL(file)],
    ["a file URL string", () => pathToFileURL(file).href],
    ["an http URL served as application/wasm", () => new URL("/typed.wasm", server.url)],
    ["an http URL served with another type", () => new URL("/untyped.wasm", server.url).href],
    ["a Response", () => new Response(bytes)],
    ["a promised Response", () => fetch(new URL("/typed.wasm", server.url))],
  ];
  for (const [name, source] of sources) {
    test(`from ${name}`, async () => {
      const module = await compileModule(source());
      expect(WebAssembly.Module.exports(module).map((e) => e.name)).toContain("sigil_call");
    });
  }

  test("fails for a relative path outside a browser", async () => {
    const err = await compileModule("./sigil.wasm").catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe("./sigil.wasm isn't a URL");
  });

  test("fails for an unsuccessful response", async () => {
    const err = await compileModule(new URL("/missing.wasm", server.url)).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(err.message).toBe(`fetching sigil.wasm from ${new URL("/missing.wasm", server.url).href}: 404 Not Found`);
  });
});

describe("Sigil.load", () => {
  test("an instance that stopped says so, on the call and after it", async () => {
    const sigil = await Sigil.load(fakeModule({ recurseOnCall: true }), { output: () => {} });
    expect(sigil.stopped).toBeUndefined();
    const err = (() => {
      try {
        sigil.version();
      } catch (e) {
        return e;
      }
    })();
    expect(err).toBeInstanceOf(SigilStoppedError);
    expect(sigil.stopped).toBe(err as SigilStoppedError);
    expect(() => sigil.format("x")).toThrow(err as SigilStoppedError);
  });

  test("passes the module's output on a line at a time", async () => {
    const lines: string[] = [];
    const err = await Sigil.load(fakeModule({ crashOnInit: true }), {
      output: (stream, line) => lines.push(`${stream}: ${line}`),
    }).catch((e) => e);
    expect(err).toBeInstanceOf(SigilError);
    expect(lines).toEqual(["stderr: panic: boom"]);
  });

  test("sends standard error to console.error by default", async () => {
    const original = console.error;
    const logged: unknown[][] = [];
    console.error = (...args: unknown[]) => logged.push(args);
    try {
      await Sigil.load(fakeModule({ crashOnInit: true })).catch(() => {});
    } finally {
      console.error = original;
    }
    expect(logged).toEqual([["sigil.wasm: panic: boom"]]);
  });
});

describe("unwrap", () => {
  test("strips ok and id from a successful response", () => {
    expect(unwrap({ ok: true, id: 3, source: "x" })).toEqual({ source: "x" });
  });

  test("throws a SigilError with help and diagnostics for a failed one", () => {
    const diagnostics = [{ severity: "error", file: "a.sigil", message: "unknown decision deny", line: 3, column: 5 }];
    try {
      unwrap({ ok: false, error: { message: "a.sigil doesn't compile", help: "Fix the errors." }, diagnostics });
      throw new Error("unwrap didn't throw");
    } catch (err) {
      expect(err).toBeInstanceOf(SigilError);
      const e = err as SigilError;
      expect(e.name).toBe("SigilError");
      expect(e.message).toBe("a.sigil doesn't compile");
      expect(e.help).toBe("Fix the errors.");
      expect(e.diagnostics).toEqual(diagnostics as never);
    }
  });

  test("copes with a failure without an error object", () => {
    expect(() => unwrap({ ok: false })).toThrow("the module returned an error without a message");
  });
});
