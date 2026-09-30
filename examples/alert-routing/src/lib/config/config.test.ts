import { describe, expect, test } from "bun:test";

import { type Config, DEFAULTS, listenAddr, loadConfig, parseAddr } from "./config";

describe("loadConfig", () => {
  test("defaults to the Go service's flag defaults", () => {
    expect(loadConfig({})).toEqual({
      addr: ":8080",
      policiesDir: "",
      teamsFile: "",
      reloadIntervalMs: 30_000,
      shutdownTimeoutMs: 15_000,
      evaluationTimeoutMs: 50,
      batchTimeoutMs: DEFAULTS.batchTimeoutMs,
      dedupTtlMs: DEFAULTS.dedupTtlMs,
      dispatchConcurrency: DEFAULTS.dispatchConcurrency,
      dedupMaxEntries: 100_000,
      workers: DEFAULTS.workers,
      maxEventStreams: 32,
      historySize: DEFAULTS.historySize,
      debug: false,
      logFormat: "json",
    } satisfies Config);
  });

  test("reads every ALERTROUTER_ variable", () => {
    expect(
      loadConfig({
        ALERTROUTER_ADDR: "127.0.0.1:9000",
        ALERTROUTER_POLICIES: "/etc/alertrouter/policies",
        ALERTROUTER_TEAMS_FILE: "/etc/alertrouter/teams.yaml",
        ALERTROUTER_RELOAD_INTERVAL: "1m",
        ALERTROUTER_SHUTDOWN_TIMEOUT: "5s",
        ALERTROUTER_EVALUATION_TIMEOUT: "250ms",
        ALERTROUTER_BATCH_TIMEOUT: "20s",
        ALERTROUTER_DEDUP_TTL: "0",
        ALERTROUTER_DISPATCH_CONCURRENCY: "4",
        ALERTROUTER_DEDUP_MAX_ENTRIES: "10",
        ALERTROUTER_WORKERS: "3",
        ALERTROUTER_MAX_EVENT_STREAMS: "0",
        ALERTROUTER_HISTORY_SIZE: "0",
        ALERTROUTER_DEBUG: "true",
        ALERTROUTER_LOG_FORMAT: "console",
      }),
    ).toEqual({
      addr: "127.0.0.1:9000",
      policiesDir: "/etc/alertrouter/policies",
      teamsFile: "/etc/alertrouter/teams.yaml",
      reloadIntervalMs: 60_000,
      shutdownTimeoutMs: 5_000,
      evaluationTimeoutMs: 250,
      batchTimeoutMs: 20_000,
      dedupTtlMs: 0,
      dispatchConcurrency: 4,
      dedupMaxEntries: 10,
      workers: 3,
      maxEventStreams: 0,
      historySize: 0,
      debug: true,
      logFormat: "console",
    });
  });

  test("an empty variable keeps the default", () => {
    expect(loadConfig({ ALERTROUTER_RELOAD_INTERVAL: "", ALERTROUTER_DEBUG: "" }).reloadIntervalMs).toBe(30_000);
  });

  test("a reload interval of 0 disables polling", () => {
    expect(loadConfig({ ALERTROUTER_RELOAD_INTERVAL: "0s" }).reloadIntervalMs).toBe(0);
  });

  test.each([
    ["ALERTROUTER_ADDR", "", "the listen address is empty", "set ALERTROUTER_ADDR, for example :8080"],
    ["ALERTROUTER_ADDR", "8080", `the listen address "8080" isn't host:port`, "set ALERTROUTER_ADDR"],
    ["ALERTROUTER_ADDR", ":99999", `the listen address ":99999" isn't host:port`, "set ALERTROUTER_ADDR"],
    ["ALERTROUTER_RELOAD_INTERVAL", "-1s", "the reload interval -1s is negative", "or 0 to disable polling"],
    ["ALERTROUTER_SHUTDOWN_TIMEOUT", "0", "the shutdown timeout 0s isn't positive", "ALERTROUTER_SHUTDOWN_TIMEOUT"],
    ["ALERTROUTER_EVALUATION_TIMEOUT", "0s", "the evaluation timeout 0s isn't positive", "without a deadline"],
    ["ALERTROUTER_BATCH_TIMEOUT", "-5s", "the batch timeout -5s isn't positive", "ALERTROUTER_BATCH_TIMEOUT"],
    ["ALERTROUTER_DEDUP_TTL", "-1m", "the dedup TTL -1m is negative", "0 to disable deduplication"],
    ["ALERTROUTER_DISPATCH_CONCURRENCY", "0", "the dispatch concurrency 0 is less than 1", "such as 16"],
    ["ALERTROUTER_DISPATCH_CONCURRENCY", "many", "ALERTROUTER_DISPATCH_CONCURRENCY=many isn't a whole number", "16"],
    ["ALERTROUTER_DEDUP_MAX_ENTRIES", "0", "the dedup table size 0 is less than 1", "such as 100000"],
    ["ALERTROUTER_WORKERS", "0", "the worker count 0 is less than 1", "at least 2 keep one team from taking them all"],
    ["ALERTROUTER_MAX_EVENT_STREAMS", "-1", "the event stream limit -1 is negative", "such as 32"],
    ["ALERTROUTER_HISTORY_SIZE", "-1", "the history size -1 is negative", "ALERTROUTER_HISTORY_SIZE"],
    ["ALERTROUTER_LOG_FORMAT", "xml", "unknown log format xml", "set ALERTROUTER_LOG_FORMAT to json or console"],
    ["ALERTROUTER_DEBUG", "yes", "ALERTROUTER_DEBUG=yes isn't a boolean", "set ALERTROUTER_DEBUG to true or false"],
    [
      "ALERTROUTER_RELOAD_INTERVAL",
      "30",
      `ALERTROUTER_RELOAD_INTERVAL=30 isn't a duration: "30" isn't a duration`,
      "such as 30s",
    ],
  ])("%s=%j is refused", (name, value, message, advice) => {
    let err: unknown;
    try {
      loadConfig({ [name]: value });
    } catch (e) {
      err = e;
    }
    expect(err).toBeInstanceOf(Error);
    const top = err as Error & { advice: string[]; cause: Error & { advice: string[] } };
    expect(top.message).toBe("alertrouter can't start with this configuration");
    expect(top.cause.message).toBe(message);
    expect(top.cause.advice.join(" ")).toContain(advice);
  });
});

test("the default worker count is 2 to 4, by the CPUs available", () => {
  expect(DEFAULTS.workers).toBeGreaterThanOrEqual(2);
  expect(DEFAULTS.workers).toBeLessThanOrEqual(4);
});

describe("parseAddr", () => {
  test.each([
    [":8080", { host: "", port: 8080 }],
    ["0.0.0.0:80", { host: "0.0.0.0", port: 80 }],
    ["localhost:3000", { host: "localhost", port: 3000 }],
    ["[::1]:8080", { host: "::1", port: 8080 }],
    ["8080", undefined],
    ["host:", undefined],
    [":123456", undefined],
    ["::1:8080", undefined],
  ])("%s", (addr, want) => {
    expect(parseAddr(addr)).toEqual(want);
  });
});

describe("listenAddr", () => {
  test.each([
    [
      "the container: the entry point set PORT from ALERTROUTER_ADDR",
      { PORT: "8080", HOSTNAME: "0.0.0.0" },
      ":8080",
      ":8080",
    ],
    ["the container with a host", { PORT: "9000", HOSTNAME: "127.0.0.1" }, "127.0.0.1:9000", "127.0.0.1:9000"],
    ["next dev --port 8091, ALERTROUTER_ADDR left at its default", { PORT: "8091" }, ":8080", ":8091"],
    ["next dev on its default port", { PORT: "3000" }, ":8080", ":3000"],
    ["no PORT at all, as in the tests", {}, ":8080", ":8080"],
    ["an empty PORT", { PORT: "" }, ":8080", ":8080"],
  ])("%s", (_name, env, configured, want) => {
    expect(listenAddr(env, configured)).toBe(want);
  });
});
