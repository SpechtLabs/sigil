// The e2e suite tests alertrouter from the outside, over HTTP, against the
// docker compose stack in examples/alert-routing/. It never imports the
// service: every assertion is about the wire contract, metrics in Mimir,
// logs in Loki, traces in Tempo and profiles in Pyroscope, which is what
// Alertmanager, a client or an operator sees.
//
// It runs only with ALERTROUTER_E2E=1, so a plain `bun test` skips it:
// start the stack and run it with `mise run e2e`. The endpoints come from
// ALERTROUTER_URL, MIMIR_URL, TEMPO_URL, LOKI_URL, PYROSCOPE_URL and
// GRAFANA_URL; the hot reload and timeout specs edit the team policies under
// ALERTROUTER_POLICIES_DIR.
import { expect } from "bun:test";
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { basename, dirname, join } from "node:path";
import { type Answer, Client, expectStatus, PATH_METRICS, PATH_READYZ } from "../fixture/client";
import { eventually } from "../fixture/eventually";
import { type Families, parseMetrics } from "../fixture/metrics";
import { EXAMPLES_DIR } from "../fixture/requests";

/** Whether the suite runs at all. */
export const E2E = process.env.ALERTROUTER_E2E === "1";

/** How long one spec may take: a backend poll of up to a minute, and the reloads around it. Pass it to every test. */
export const SPEC_TIMEOUT = 120_000;

function envOr(key: string, fallback: string): string {
  const v = process.env[key];
  return v === undefined || v === "" ? fallback : v;
}

// The defaults match the ports docker-compose.yaml publishes.
export const alertrouter = new Client(envOr("ALERTROUTER_URL", "http://localhost:8080"));
export const tempo = new Client(envOr("TEMPO_URL", "http://localhost:3200"));
export const mimir = new Client(envOr("MIMIR_URL", "http://localhost:9009"));
export const loki = new Client(envOr("LOKI_URL", "http://localhost:3100"));
export const pyroscope = new Client(envOr("PYROSCOPE_URL", "http://localhost:4040"));
export const grafana = new Client(envOr("GRAFANA_URL", "http://localhost:3000"));

/** The host side of the compose bind mount. */
export const policiesDir = envOr("ALERTROUTER_POLICIES_DIR", join(EXAMPLES_DIR, "policies", "teams"));

/** Alloy scrapes and batches asynchronously, and the profiler uploads every 15s. */
export const BACKEND = { timeoutMs: 60_000, intervalMs: 1_000 };

let ready: Promise<void> | undefined;

/**
 * Waits for readiness once per run. `docker compose up --wait` returns once
 * the healthchecks pass, but the suite may also run against a stack that is
 * still starting, so it waits itself instead of failing every spec with a
 * refused connection.
 */
export function waitReady(): Promise<void> {
  ready ??= eventually(
    async () => {
      const a = await alertrouter.get(PATH_READYZ);
      if (a.status !== 200) throw new Error(`/readyz answered ${a.status}`);
    },
    { timeoutMs: 60_000, intervalMs: 1_000 },
  ).catch((err: unknown) => {
    throw new Error(`alertrouter at ${alertrouter.baseURL} never became ready; is the compose stack up? ${err}`);
  });
  return ready;
}

/** Fetches /metrics and parses it. */
export async function scrapeMetrics(): Promise<Families> {
  const a = await alertrouter.get(PATH_METRICS);
  expectStatus(a, 200);
  return parseMetrics(a.body);
}

/**
 * How long a reload may take to see what the suite wrote. The stack reads the
 * directory through a bind mount, and Docker Desktop's file sharing can show
 * the container the old bytes for a moment after the host changed them.
 */
const MOUNT_SETTLE = { timeoutMs: 15_000, intervalMs: 250 };

/**
 * Reloads until the service serves exactly what the mounted directory holds
 * now: the reload succeeds and the served fingerprint is the directory's.
 */
export async function reloadUntilServed(): Promise<void> {
  const want = fingerprintDir(policiesDir);
  await eventually(async () => {
    expectStatus(await alertrouter.reload(), 200);
    expect((await alertrouter.served()).fingerprint, "the served bundle isn't the mounted directory yet").toBe(want);
  }, MOUNT_SETTLE);
}

/**
 * Reloads until the service rejects the mounted directory, and returns that
 * answer: the broken bytes may take a moment to reach the container.
 */
export async function reloadUntilRejected(): Promise<Answer> {
  return eventually(async () => {
    const a = await alertrouter.reload();
    expect(a.status, "the service still loads the bundle; the broken file hasn't reached it yet").toBe(500);
    return a;
  }, MOUNT_SETTLE);
}

/**
 * The bundle fingerprint of dir, as alertrouter (and the Go service before
 * it) computes it: SHA-256 over every `.sigil` file not under a dot name, in
 * directory-walk order (each directory's entries sorted by bytes), each as
 * `<len(path)>:<path><len(data)>:<data>` with byte lengths and paths
 * relative to dir.
 */
export function fingerprintDir(dir: string): string {
  const h = createHash("sha256");
  const walk = (rel: string) => {
    const names = readdirSync(join(dir, rel))
      .filter((n) => !n.startsWith("."))
      .sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));
    for (const name of names) {
      const path = rel === "" ? name : `${rel}/${name}`;
      if (statSync(join(dir, path)).isDirectory()) {
        walk(path);
      } else if (name.endsWith(".sigil")) {
        const p = Buffer.from(path);
        const data = readFileSync(join(dir, path));
        h.update(`${p.length}:`);
        h.update(p);
        h.update(`${data.length}:`);
        h.update(data);
      }
    }
  };
  walk("");
  return h.digest("hex");
}

/**
 * Replaces path's content in one step: the bytes go to a dot file next to it,
 * which the bundle loader skips, and a rename puts them in place, so a reload
 * never reads a half-written policy.
 */
export function writeAtomic(path: string, content: string): void {
  const tmp = join(dirname(path), `.${basename(path)}.e2e-${process.pid}-${Date.now()}`);
  writeFileSync(tmp, content);
  renameSync(tmp, path);
}

/**
 * Why the specs that edit the bundle can't run, or undefined when they can:
 * the directory is missing or read-only here, or the service serves a
 * bundle that isn't the mounted directory. The probe starts with a dot,
 * which the bundle loader skips, so a reload racing with it never sees it.
 */
export async function unwritableReason(): Promise<string | undefined> {
  if (!existsSync(policiesDir) || !statSync(policiesDir).isDirectory()) {
    return `ALERTROUTER_POLICIES_DIR ${policiesDir} is not a directory`;
  }
  try {
    const probe = mkdtempSync(join(policiesDir, ".e2e-probe-"));
    rmSync(probe, { recursive: true });
  } catch (err) {
    return `ALERTROUTER_POLICIES_DIR ${policiesDir} is not writable: ${err}`;
  }
  await waitReady();
  if ((await alertrouter.served()).source === "embedded") {
    return `alertrouter serves its embedded bundle, so editing ${policiesDir} changes nothing`;
  }
  return undefined;
}

/** Undo steps, run last first by {@link restore}; each spec that edits the bundle registers its own. */
const undo: (() => Promise<void>)[] = [];

/** Runs every registered undo step, last first. Call it from afterEach. */
export async function restore(): Promise<void> {
  const errors: unknown[] = [];
  for (let step = undo.pop(); step !== undefined; step = undo.pop()) {
    try {
      await step();
    } catch (err) {
      errors.push(err);
    }
  }
  if (errors.length > 0) throw errors[0];
}

/**
 * Replaces the one occurrence of from in path with to, and registers the
 * restore first: the original bytes go back, the service reloads, and verify
 * checks the original behavior is back.
 */
export function editFile(path: string, from: string, to: string, verify: () => Promise<void>): void {
  const original = readFileSync(path, "utf8");
  expect(original.split(from).length - 1, `${path} no longer holds ${from} once; update the spec`).toBe(1);
  undo.push(async () => {
    writeAtomic(path, original);
    await reloadUntilServed();
    await verify();
  });
  writeAtomic(path, original.replace(from, to));
}

/** Replaces path with content, and registers the restore first. */
export function replaceFile(path: string, content: string): void {
  const original = readFileSync(path, "utf8");
  undo.push(async () => {
    writeAtomic(path, original);
    await reloadUntilServed();
  });
  writeAtomic(path, content);
}

/** Writes a new file at path, and registers its removal first. It refuses to overwrite a file it didn't create. */
export function createFile(path: string, content: string, afterRemoval: () => Promise<void>): void {
  expect(existsSync(path), `${path} already exists; the suite won't overwrite a file it didn't create`).toBe(false);
  undo.push(async () => {
    rmSync(path);
    await reloadUntilServed();
    await afterRemoval();
  });
  writeAtomic(path, content);
}
