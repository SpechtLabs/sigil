// The process around the service: it reads the environment, sets up
// telemetry, builds the service in a startup span, installs it for the
// route handlers, and owns the signals: SIGHUP reloads the team policies,
// SIGINT and SIGTERM shut down gracefully. Next calls it once per server
// process from instrumentation.ts. An initial load that fails exits the
// process: serving without team policies would route every alert to the
// default channel, and a pod that never becomes ready stalls a rollout with
// a broken bundle on the old pods instead.

import { readFile } from "node:fs/promises";

import { SpanStatusCode } from "@opentelemetry/api";

import { type Config, listenAddr, loadConfig } from "@/lib/config/config";
import { goDurationString, msToNs } from "@/lib/duration";
import { PLATFORM_FILES, TEAM_FILES, TEAMS_YAML } from "@/lib/embedded";
import { adviceOf, messageOf, wrap } from "@/lib/errors";
import { loadSigilModule } from "@/lib/sigil";
import { directoryBundle, embeddedBundle } from "@/lib/store/bundle";
import { DEFAULT_SOURCE, loadTeamsFile, TeamDirectory } from "@/lib/teams/directory";
import { setupTelemetry } from "@/lib/telemetry/telemetry";
import type { Telemetry } from "@/lib/telemetry/types";
import { setService } from "./registry";
import { createService, type Service } from "./service";

/** Bounds flushing the last spans on exit, apart from the shutdown timeout, which may be spent by then. */
const TELEMETRY_FLUSH_TIMEOUT_MS = 5_000;

/** Boots the service, or exits the process with status 1 when it can't start. */
export async function boot(env: NodeJS.ProcessEnv): Promise<void> {
  let config: Config;
  try {
    config = loadConfig(env);
  } catch (err) {
    // No logger yet: this goes to standard error the way the Go service's
    // CLI prints a humane error.
    process.stderr.write(`${display(err)}\n`);
    process.exit(1);
  }

  const version = env.ALERTROUTER_VERSION ?? "dev";
  let telemetry: Telemetry;
  try {
    telemetry = await setupTelemetry({ env, version, logFormat: config.logFormat, debug: config.debug });
  } catch (err) {
    process.stderr.write(`${display(err)}\n`);
    process.exit(1);
  }

  let service: Service;
  try {
    service = await start(config, telemetry, version);
  } catch (err) {
    telemetry.logger.error("alertrouter failed to start", {
      error: messageOf(err),
      causes: causesOf(err),
      advice: adviceOf(err),
    });
    await flush(telemetry);
    process.exit(1);
  }

  setService(service);
  service.watch();
  handleSignals(service, telemetry);
}

/**
 * Loads the team directory and the Sigil engine and builds the service in
 * an alertrouter.startup span, so the initial policy load shows up in the
 * traces as part of it.
 */
async function start(config: Config, telemetry: Telemetry, version: string): Promise<Service> {
  const span = telemetry.tracer.startSpan("alertrouter.startup", {
    attributes: {
      "alertrouter.version": version,
      "alertrouter.addr": listenAddr(process.env, config.addr),
      "sigil.source": config.policiesDir === "" ? "embedded" : config.policiesDir,
    },
  });
  try {
    const teams = await loadTeams(config.teamsFile).catch((err: unknown) => {
      throw wrap(
        err,
        "alertrouter won't start without a team directory that loads",
        "fix the team directory, or leave ALERTROUTER_TEAMS_FILE empty for the one bundled with alertrouter",
      );
    });
    span.setAttributes({ "alertrouter.teams": teams.names(), "alertrouter.teams_source": teams.source });

    const wasm = await loadSigilModule();
    const service = createService({
      config,
      telemetry,
      wasm,
      teams,
      bundle: config.policiesDir === "" ? embeddedBundle(TEAM_FILES) : directoryBundle(config.policiesDir),
      platform: PLATFORM_FILES,
      // A router that can't compute the platform's page must not keep
      // answering: exiting lets the supervisor start a fresh process.
      onFatal: (err) => {
        telemetry.logger.error("alertrouter exits: the platform engine can't be replaced", {
          error: err.message,
          causes: causesOf(err),
          advice: adviceOf(err),
        });
        void flush(telemetry).finally(() => process.exit(1));
      },
    });
    await service.start().catch((err: unknown) => {
      throw wrap(
        err,
        "alertrouter won't start without team policies that load",
        "fix the team policies, or point ALERTROUTER_POLICIES at a directory that loads",
        "every team in the team directory needs a policy <team>.alerts that invokes platform.paging",
      );
    });

    telemetry.logger.info("alertrouter started", {
      version,
      addr: listenAddr(process.env, config.addr),
      teams: teams.names(),
      teams_source: teams.source,
      policies: service.store.source,
      reload_interval: goDurationString(msToNs(config.reloadIntervalMs)),
      evaluation_timeout: goDurationString(msToNs(config.evaluationTimeoutMs)),
    });
    return service;
  } catch (err) {
    span.recordException(err instanceof Error ? err : new Error(String(err)));
    span.setStatus({ code: SpanStatusCode.ERROR, message: messageOf(err) });
    throw err;
  } finally {
    span.end();
  }
}

function loadTeams(path: string): Promise<TeamDirectory> {
  if (path === "") return Promise.resolve(TeamDirectory.parse(TEAMS_YAML, DEFAULT_SOURCE));
  return loadTeamsFile(path, (p) => readFile(p, "utf8"));
}

function handleSignals(service: Service, telemetry: Telemetry): void {
  process.on("SIGHUP", () => {
    // A failure is logged by the store, and the previous bundle keeps serving.
    service.reload("sighup").catch(() => undefined);
  });

  let stopping = false;
  const stop = async (signal: NodeJS.Signals) => {
    if (stopping) return;
    stopping = true;
    telemetry.logger.info("alertrouter shutting down", { signal });
    const err = await service.shutdown();
    if (err !== undefined)
      telemetry.logger.error("graceful shutdown failed", { error: err.message, advice: adviceOf(err) });
    setService(undefined);
    await flush(telemetry);
    process.exit(err === undefined ? 0 : 1);
  };
  process.on("SIGTERM", (s) => void stop(s));
  process.on("SIGINT", (s) => void stop(s));
}

async function flush(telemetry: Telemetry): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([
      telemetry.shutdown(),
      new Promise((resolve) => {
        timer = setTimeout(resolve, TELEMETRY_FLUSH_TIMEOUT_MS);
      }),
    ]);
  } catch (err) {
    process.stderr.write(`${display(err)}\n`);
  } finally {
    clearTimeout(timer);
  }
}

/** The messages of the errors below err, outermost first. */
function causesOf(err: unknown): string[] {
  const out: string[] = [];
  for (
    let e = err instanceof Error ? err.cause : undefined;
    e !== undefined && e !== null;
    e = e instanceof Error ? e.cause : undefined
  ) {
    out.push(messageOf(e));
  }
  return out;
}

/** A humane error as text: the message, its causes, and what to do. */
function display(err: unknown): string {
  const lines: string[] = [];
  for (let e: unknown = err; e !== undefined && e !== null; e = e instanceof Error ? e.cause : undefined) {
    lines.push(lines.length === 0 ? messageOf(e) : `  caused by: ${messageOf(e)}`);
    if (!(e instanceof Error)) break;
  }
  const advice = adviceOf(err);
  if (advice.length > 0) lines.push("", "Advice:", ...advice.map((a) => `  - ${a}`));
  return lines.join("\n");
}
