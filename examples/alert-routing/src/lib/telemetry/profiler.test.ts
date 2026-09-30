// The upload itself is tested on Node, in test/node/profiler-upload.ts: the
// SDK's native profiler doesn't load under Bun.

import { describe, expect, test } from "bun:test";
import { profilerConfig, startProfiler, UPLOAD_INTERVAL_MS } from "./profiler";
import type { Logger } from "./types";

const silent: Logger = { log() {}, debug() {}, info() {}, warn() {}, error() {} };

describe("profilerConfig", () => {
  test("is off without PYROSCOPE_SERVER_ADDRESS", () => {
    expect(profilerConfig({}, "v1")).toBeUndefined();
    expect(profilerConfig({ PYROSCOPE_SERVER_ADDRESS: "" }, "v1")).toBeUndefined();
  });

  test.each([
    ["defaults", { PYROSCOPE_SERVER_ADDRESS: "http://pyroscope:4040" }, "", "alertrouter", "dev"],
    [
      "from the environment",
      { PYROSCOPE_SERVER_ADDRESS: "https://profiles.example.com", OTEL_SERVICE_NAME: "router-canary" },
      "v1.2.3",
      "router-canary",
      "v1.2.3",
    ],
  ])("%s", (_name, env, version, appName, tag) => {
    expect(profilerConfig(env, version)).toEqual({
      appName,
      serverAddress: env.PYROSCOPE_SERVER_ADDRESS,
      tags: { version: tag },
      flushIntervalMs: UPLOAD_INTERVAL_MS,
      wall: { samplingDurationMs: UPLOAD_INTERVAL_MS, collectCpuTime: true },
    });
  });

  test.each(["://invalid", "pyroscope:4040", "ftp://pyroscope:4040"])("rejects %s, naming the setting", (address) => {
    expect(() => profilerConfig({ PYROSCOPE_SERVER_ADDRESS: address }, "")).toThrow(/PYROSCOPE_SERVER_ADDRESS/);
  });
});

describe("startProfiler", () => {
  test("starts nothing, and loads no native code, without a backend", async () => {
    expect(await startProfiler({}, "v1", silent)).toBeUndefined();
  });

  test("rejects an invalid backend before loading the SDK", async () => {
    await expect(startProfiler({ PYROSCOPE_SERVER_ADDRESS: "://invalid" }, "v1", silent)).rejects.toThrow(
      /isn't an http or https URL/,
    );
  });
});
