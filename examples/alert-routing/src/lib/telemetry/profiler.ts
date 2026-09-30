// Continuous profiling with Pyroscope's Node SDK: wall-clock time with CPU
// time alongside it, and the sampled live heap, uploaded every 15 seconds
// like the Go service's profiles. It runs only when PYROSCOPE_SERVER_ADDRESS
// is set.
//
// This file imports nothing of the service at run time, only types: `node
// --test` runs its upload test directly on Node, because the SDK's native
// profiler (@datadog/pprof) needs V8 and doesn't load under Bun.

import type { PyroscopeConfig } from "@pyroscope/nodejs";
import type { Env } from "./tracing";
import type { Logger } from "./types";

/** How often profiles are uploaded, matching the Go service's upload rate. */
export const UPLOAD_INTERVAL_MS = 15_000;

/** A running profiler. */
export interface Profiler {
  /** Uploads what was sampled since the last upload and stops sampling. */
  stop(): Promise<void>;
}

/**
 * The SDK configuration the environment asks for, or undefined when
 * PYROSCOPE_SERVER_ADDRESS is unset. Profile labels describe the process
 * (its service name and version); request ids or actors would create an
 * unbounded number of series. It throws when the address isn't an http or
 * https URL, which the SDK would otherwise only report on its first upload.
 * uploadIntervalMs is for tests, which can't wait 15 seconds for the first
 * heap profile: the SDK uploads the heap only on its interval, not on stop.
 */
export function profilerConfig(
  env: Env,
  version: string,
  uploadIntervalMs: number = UPLOAD_INTERVAL_MS,
): PyroscopeConfig | undefined {
  const address = env.PYROSCOPE_SERVER_ADDRESS;
  if (!address) return undefined;

  let url: URL | undefined;
  try {
    url = new URL(address);
  } catch {
    url = undefined;
  }
  if (url?.protocol !== "http:" && url?.protocol !== "https:") {
    throw new Error(
      `PYROSCOPE_SERVER_ADDRESS ${address} isn't an http or https URL; set it to Pyroscope's address, such as http://pyroscope:4040, or unset it to turn profiling off`,
    );
  }

  return {
    appName: env.OTEL_SERVICE_NAME || "alertrouter",
    serverAddress: address,
    tags: { version: version || "dev" },
    flushIntervalMs: uploadIntervalMs,
    // One wall profile per upload, with each sample's CPU time too, so
    // Pyroscope serves both a wall-clock and a CPU profile.
    wall: { samplingDurationMs: uploadIntervalMs, collectCpuTime: true },
  };
}

/**
 * Starts profiling when the environment configures it and returns the
 * running profiler, or undefined when profiling is off. The SDK is loaded
 * only then, so a process that doesn't profile never loads its native code.
 */
export async function startProfiler(
  env: Env,
  version: string,
  logger: Logger,
  uploadIntervalMs: number = UPLOAD_INTERVAL_MS,
): Promise<Profiler | undefined> {
  const config = profilerConfig(env, version, uploadIntervalMs);
  if (!config) return undefined;

  const { default: Pyroscope } = await import("@pyroscope/nodejs");
  // The SDK's own messages go to the process logger, at debug unless they
  // are warnings or errors.
  const say =
    (level: "debug" | "warn" | "error") =>
    (...args: unknown[]) =>
      logger.log(level, "pyroscope", { detail: args.map(String).join(" ") });
  Pyroscope.setLogger({
    trace: say("debug"),
    debug: say("debug"),
    info: say("debug"),
    warn: say("warn"),
    error: say("error"),
    fatal: say("error"),
  });
  try {
    Pyroscope.init(config);
    Pyroscope.start();
  } catch (err) {
    throw new Error(
      `starting continuous profiling failed: ${err instanceof Error ? err.message : String(err)}; check the PYROSCOPE_* settings, or unset PYROSCOPE_SERVER_ADDRESS to turn profiling off`,
    );
  }
  return { stop: () => Pyroscope.stop() };
}
