// alertrouter's configuration: ALERTROUTER_* environment variables with the
// Go service's names and defaults (its flags, upper-cased with dashes as
// underscores), resolved once at startup and checked with advice on what to
// set instead. A test builds a Config from a plain object, so nothing here
// reads process.env on its own.

import { formatDuration, msToNs, nsToMs, parseDuration } from "../duration";
import { HumaneError, humane, messageOf, wrap } from "../errors";

/** The prefix of every environment variable alertrouter reads. */
export const ENV_PREFIX = "ALERTROUTER_";

export type LogFormat = "json" | "console";

/** The resolved configuration. Durations are in milliseconds. */
export interface Config {
  /** The listen address of the one HTTP port: API, UI, health and metrics. */
  addr: string;
  /** The directory holding the team policies; empty serves the embedded team bundle. */
  policiesDir: string;
  /** The team directory file (teams.yaml); empty serves the embedded one. */
  teamsFile: string;
  /** How often the policies directory is polled for changes; 0 disables polling. */
  reloadIntervalMs: number;
  /** How long a graceful shutdown may take. */
  shutdownTimeoutMs: number;
  /** How long one alert's evaluation may take before it's routed with the fallback. */
  evaluationTimeoutMs: number;
  /**
   * How long one webhook batch may take from receipt to answer, evaluations
   * and dispatch together. Alerts not evaluated by then go to the fallback.
   * Not in the Go service.
   */
  batchTimeoutMs: number;
  /**
   * How long a dispatched notification is remembered by fingerprint, so a
   * redelivered webhook doesn't page twice. 0 disables deduplication. Not in
   * the Go service.
   */
  dedupTtlMs: number;
  /** How many notifications of one batch are in flight at once. Not in the Go service. */
  dispatchConcurrency: number;
  /**
   * How many delivered notifications deduplication remembers at most; the
   * oldest are forgotten first. Not in the Go service.
   */
  dedupMaxEntries: number;
  /**
   * How many worker threads evaluate team policies. One team may use at
   * most half of them. Not in the Go service.
   */
  workers: number;
  /** How many console event streams (SSE) may be open at once. Not in the Go service. */
  maxEventStreams: number;
  /** How many routed alerts and notifications the UI's history keeps. Not in the Go service. */
  historySize: number;
  /** Debug logging. */
  debug: boolean;
  logFormat: LogFormat;
}

/** The defaults, one per setting that has one. The Dockerfile and compose rely on them. */
export const DEFAULTS = {
  addr: ":8080",
  reloadIntervalMs: 30_000,
  shutdownTimeoutMs: 15_000,
  evaluationTimeoutMs: 50,
  batchTimeoutMs: 10_000,
  dedupTtlMs: 5 * 60_000,
  dispatchConcurrency: 16,
  dedupMaxEntries: 100_000,
  // At least two, so one team's share (half the pool) leaves the others one.
  workers: Math.max(2, Math.min(4, availableCpus())),
  maxEventStreams: 32,
  historySize: 500,
  logFormat: "json" as LogFormat,
} as const;

/** The environment variable of each setting. */
export const ENV = {
  addr: "ALERTROUTER_ADDR",
  policiesDir: "ALERTROUTER_POLICIES",
  teamsFile: "ALERTROUTER_TEAMS_FILE",
  reloadInterval: "ALERTROUTER_RELOAD_INTERVAL",
  shutdownTimeout: "ALERTROUTER_SHUTDOWN_TIMEOUT",
  evaluationTimeout: "ALERTROUTER_EVALUATION_TIMEOUT",
  batchTimeout: "ALERTROUTER_BATCH_TIMEOUT",
  dedupTtl: "ALERTROUTER_DEDUP_TTL",
  dispatchConcurrency: "ALERTROUTER_DISPATCH_CONCURRENCY",
  dedupMaxEntries: "ALERTROUTER_DEDUP_MAX_ENTRIES",
  workers: "ALERTROUTER_WORKERS",
  maxEventStreams: "ALERTROUTER_MAX_EVENT_STREAMS",
  historySize: "ALERTROUTER_HISTORY_SIZE",
  debug: "ALERTROUTER_DEBUG",
  logFormat: "ALERTROUTER_LOG_FORMAT",
} as const;

type Env = Readonly<Record<string, string | undefined>>;

/**
 * Resolves the configuration from env over the defaults and checks it with
 * validateConfig. Throws a HumaneError naming the variable to fix.
 */
export function loadConfig(env: Env): Config {
  try {
    const cfg: Config = {
      addr: str(env, ENV.addr, DEFAULTS.addr),
      policiesDir: str(env, ENV.policiesDir, ""),
      teamsFile: str(env, ENV.teamsFile, ""),
      reloadIntervalMs: duration(env, ENV.reloadInterval, DEFAULTS.reloadIntervalMs),
      shutdownTimeoutMs: duration(env, ENV.shutdownTimeout, DEFAULTS.shutdownTimeoutMs),
      evaluationTimeoutMs: duration(env, ENV.evaluationTimeout, DEFAULTS.evaluationTimeoutMs),
      batchTimeoutMs: duration(env, ENV.batchTimeout, DEFAULTS.batchTimeoutMs),
      dedupTtlMs: duration(env, ENV.dedupTtl, DEFAULTS.dedupTtlMs),
      dispatchConcurrency: integer(env, ENV.dispatchConcurrency, DEFAULTS.dispatchConcurrency),
      dedupMaxEntries: integer(env, ENV.dedupMaxEntries, DEFAULTS.dedupMaxEntries),
      workers: integer(env, ENV.workers, DEFAULTS.workers),
      maxEventStreams: integer(env, ENV.maxEventStreams, DEFAULTS.maxEventStreams),
      historySize: integer(env, ENV.historySize, DEFAULTS.historySize),
      debug: bool(env, ENV.debug),
      logFormat: str(env, ENV.logFormat, DEFAULTS.logFormat) as LogFormat,
    };
    validateConfig(cfg);
    return cfg;
  } catch (err) {
    throw wrap(
      err,
      "alertrouter can't start with this configuration",
      "every setting is an ALERTROUTER_* environment variable; see the README for the list and the defaults",
    );
  }
}

/** Throws for the first setting that can't work, with advice on what to set instead. */
export function validateConfig(c: Config): void {
  if (c.addr === "") throw humane("the listen address is empty", `set ${ENV.addr}, for example :8080`);
  if (parseAddr(c.addr) === undefined) {
    throw humane(
      `the listen address ${JSON.stringify(c.addr)} isn't host:port`,
      `set ${ENV.addr} to a port with an optional host, such as :8080 or 127.0.0.1:8080`,
    );
  }
  if (c.reloadIntervalMs < 0) {
    throw humane(
      `the reload interval ${show(c.reloadIntervalMs)} is negative`,
      `set ${ENV.reloadInterval} to a positive duration such as 30s, or 0 to disable polling`,
    );
  }
  if (c.shutdownTimeoutMs <= 0) {
    throw humane(
      `the shutdown timeout ${show(c.shutdownTimeoutMs)} isn't positive`,
      `set ${ENV.shutdownTimeout} to a positive duration such as 15s`,
    );
  }
  if (c.evaluationTimeoutMs <= 0) {
    throw humane(
      `the evaluation timeout ${show(c.evaluationTimeoutMs)} isn't positive`,
      `set ${ENV.evaluationTimeout} to a positive duration such as 1s; an evaluation without a deadline could hold an alert for as long as its input makes it run`,
    );
  }
  if (c.batchTimeoutMs <= 0) {
    throw humane(
      `the batch timeout ${show(c.batchTimeoutMs)} isn't positive`,
      `set ${ENV.batchTimeout} to a positive duration such as 10s, shorter than the Alertmanager receiver's timeout`,
    );
  }
  if (c.dedupTtlMs < 0) {
    throw humane(
      `the dedup TTL ${show(c.dedupTtlMs)} is negative`,
      `set ${ENV.dedupTtl} to a duration such as 5m, shorter than the route's group_interval, or 0 to disable deduplication`,
    );
  }
  if (c.dispatchConcurrency < 1) {
    throw humane(
      `the dispatch concurrency ${c.dispatchConcurrency} is less than 1`,
      `set ${ENV.dispatchConcurrency} to how many notifications may be in flight at once, such as 16`,
    );
  }
  if (c.dedupMaxEntries < 1) {
    throw humane(
      `the dedup table size ${c.dedupMaxEntries} is less than 1`,
      `set ${ENV.dedupMaxEntries} to how many delivered notifications to remember, such as 100000`,
    );
  }
  if (c.workers < 1) {
    throw humane(
      `the worker count ${c.workers} is less than 1`,
      `set ${ENV.workers} to how many threads evaluate team policies, such as 4; at least 2 keep one team from taking them all`,
    );
  }
  if (c.maxEventStreams < 0) {
    throw humane(
      `the event stream limit ${c.maxEventStreams} is negative`,
      `set ${ENV.maxEventStreams} to how many console streams may be open at once, such as 32, or 0 to refuse them all`,
    );
  }
  if (c.historySize < 0) {
    throw humane(
      `the history size ${c.historySize} is negative`,
      `set ${ENV.historySize} to how many routed alerts the console keeps, such as 500, or 0 to keep none`,
    );
  }
  if (c.logFormat !== "json" && c.logFormat !== "console") {
    throw humane(`unknown log format ${c.logFormat}`, `set ${ENV.logFormat} to json or console`);
  }
}

/**
 * Splits a Go-style listen address, ":8080", "127.0.0.1:8080" or
 * "[::1]:8080", into the host (empty for every interface) and the port.
 */
export function parseAddr(addr: string): { host: string; port: number } | undefined {
  const m = /^(?:\[([^\]]+)\]|([^:[\]]*)):(\d{1,5})$/.exec(addr);
  if (m === null) return undefined;
  const port = Number(m[3]);
  if (port > 65_535) return undefined;
  return { host: m[1] ?? m[2] ?? "", port };
}

/**
 * The address the server listens on, for the startup log and span. The
 * container's entry point (bin/alertrouter.mjs) hands ALERTROUTER_ADDR to
 * Next as PORT, so there the two agree and the configured address is the
 * answer. Under `next dev` or `next start` without that entry point, Next
 * listens on its own port, --port or PORT, which it writes back to PORT
 * before the service boots; the configured address never applies then, so
 * the answer is that port on every interface.
 */
export function listenAddr(env: Env, configured: string): string {
  const port = env.PORT;
  if (port === undefined || port === "") return configured;
  if (parseAddr(configured)?.port === Number(port)) return configured;
  return `:${port}`;
}

// The CPUs this process may use, which a container's CPU limit lowers.
function availableCpus(): number {
  const os = process.getBuiltinModule("node:os");
  return typeof os.availableParallelism === "function" ? os.availableParallelism() : os.cpus().length;
}

function str(env: Env, name: string, fallback: string): string {
  return env[name] ?? fallback;
}

function duration(env: Env, name: string, fallbackMs: number): number {
  const raw = env[name];
  if (raw === undefined || raw === "") return fallbackMs;
  try {
    return nsToMs(parseDuration(raw));
  } catch (err) {
    throw new HumaneError(`${name}=${raw} isn't a duration: ${messageOf(err)}`, [
      `set ${name} to a duration such as ${show(fallbackMs)}`,
    ]);
  }
}

function integer(env: Env, name: string, fallback: number): number {
  const raw = env[name];
  if (raw === undefined || raw === "") return fallback;
  if (!/^-?\d+$/.test(raw.trim())) {
    throw humane(`${name}=${raw} isn't a whole number`, `set ${name} to a number such as ${fallback}`);
  }
  return Number(raw.trim());
}

// The values strconv.ParseBool, and so the Go service's --debug, accepts.
const TRUE = new Set(["1", "t", "T", "TRUE", "true", "True"]);
const FALSE = new Set(["0", "f", "F", "FALSE", "false", "False"]);

function bool(env: Env, name: string): boolean {
  const raw = env[name];
  if (raw === undefined || raw === "") return false;
  if (TRUE.has(raw)) return true;
  if (FALSE.has(raw)) return false;
  throw humane(`${name}=${raw} isn't a boolean`, `set ${name} to true or false`);
}

function show(ms: number): string {
  return formatDuration(msToNs(ms));
}
