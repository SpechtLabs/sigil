// Runs on Node, not Bun (`node --test test/node/`): Pyroscope's native
// profiler needs V8. It checks the SDK's real uploads to a fake Pyroscope,
// rather than the options passed to it, like the Go service's profile test.

import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { test } from "node:test";
import { gunzipSync } from "node:zlib";
import type * as ProfilerModule from "../../src/lib/telemetry/profiler";
import type { Logger } from "../../src/lib/telemetry/types";

// Node runs TypeScript only with the file's own extension in the specifier,
// which tsc allows in a literal import only with allowImportingTsExtensions;
// a computed one it leaves alone, and the type import above types it.
const { startProfiler } = (await import(
  new URL("../../src/lib/telemetry/profiler.ts", import.meta.url).href
)) as typeof ProfilerModule;

/** The upload interval here, so the test doesn't wait the 15 seconds the service does. */
const UPLOAD_MS = 300;

interface Upload {
  name: string;
  profile: string;
}

test("uploads a wall profile with CPU time and a heap profile, named after the service", async () => {
  const uploads: Upload[] = [];
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const url = new URL(req.url ?? "/", "http://pyroscope");
      const body = Buffer.concat(chunks);
      // The pprof protobuf, gzipped; its string table names the sample types.
      const raw = body[0] === 0x1f && body[1] === 0x8b ? gunzipSync(body) : body;
      uploads.push({ name: url.searchParams.get("name") ?? "", profile: raw.toString("latin1") });
      res.writeHead(200).end();
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;

  const warnings: string[] = [];
  const logger: Logger = {
    log: (level, msg, fields) => {
      if (level === "warn" || level === "error") warnings.push(`${msg} ${JSON.stringify(fields)}`);
    },
    debug() {},
    info() {},
    warn: (msg) => warnings.push(msg),
    error: (msg) => warnings.push(msg),
  };

  try {
    const profiler = await startProfiler(
      { PYROSCOPE_SERVER_ADDRESS: `http://127.0.0.1:${port}`, OTEL_SERVICE_NAME: "profile-test" },
      "test",
      logger,
      UPLOAD_MS,
    );
    assert.ok(profiler, "profiling is on with a backend");

    // Something to sample, CPU work and live allocations, for long enough
    // that the heap is uploaded on the interval: the SDK doesn't upload it
    // on stop. The loop yields, so the upload timers get to run.
    const keep: number[][] = [];
    const until = Date.now() + 4 * UPLOAD_MS;
    while (Date.now() < until) {
      for (let i = 0; i < 100; i++) keep.push(Array.from({ length: 1_000 }, (_, j) => Math.sqrt(j)));
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.ok(keep.length > 0);

    await profiler.stop();
  } finally {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }

  assert.deepEqual(warnings, [], "the SDK reported no warnings or errors");
  for (const upload of uploads) {
    assert.match(upload.name, /^profile-test\{.*version=test.*\}$/, "the application name carries the version tag");
  }
  const wall = uploads.find((u) => u.profile.includes("wall"));
  // The heap profile's sample types are objects and space, which Pyroscope
  // serves as memory:inuse_objects and memory:inuse_space.
  const heap = uploads.find((u) => u.profile.includes("objects") && u.profile.includes("space"));
  assert.ok(wall, `a wall profile was uploaded; got ${uploads.length} uploads`);
  assert.ok(wall.profile.includes("cpu"), "the wall profile carries CPU time");
  assert.ok(heap, "a heap profile was uploaded");
});
