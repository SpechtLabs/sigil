import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { TEAM_FILES } from "../embedded";
import { directoryBundle, embeddedBundle, fingerprint, SOURCE_EMBEDDED } from "./bundle";

// The fixture and its fingerprint as the Go service's store.Fingerprint
// computes it (internal/store/fingerprint.go, run on the same files): hidden
// entries and non-.sigil files are skipped, and entries are walked in
// byte order, so "Root.sigil" comes before "a/".
const FIXTURE: Record<string, string> = {
  "a/alerts.sigil": "policy a.alerts: AlertRouting@1\n",
  "a/alerts_test.yaml": "x",
  "b-team/alerts.sigil": "policy b.alerts: AlertRouting@1\n# ünïcode\n",
  "b-team/nested/x.sigil": "module b.x: AlertRouting@1\n",
  ".hidden/h.sigil": "hidden",
  ".z.sigil": "z",
  "Root.sigil": "root\n",
};
const GO_FINGERPRINT = "d34494218d17ee50fb9f3009a2b6b30c2dffd995618994744f2cde7e27d7e693";

let dir: string;

beforeAll(async () => {
  dir = await mkdtemp(join(tmpdir(), "alertrouter-bundle-"));
  for (const [path, source] of Object.entries(FIXTURE)) {
    await mkdir(dirname(join(dir, path)), { recursive: true });
    await writeFile(join(dir, path), source);
  }
});

afterAll(() => rm(dir, { recursive: true, force: true }));

describe("directoryBundle", () => {
  test("fingerprints a directory exactly as the Go service does", async () => {
    const bundle = await directoryBundle(dir).read();
    expect(bundle.fingerprint).toBe(GO_FINGERPRINT);
  });

  test("reads the .sigil files only, skipping hidden entries, with paths relative to the directory", async () => {
    const bundle = await directoryBundle(dir).read();
    expect(bundle.files.map((f) => f.path)).toEqual([
      "Root.sigil",
      "a/alerts.sigil",
      "b-team/alerts.sigil",
      "b-team/nested/x.sigil",
    ]);
    expect(bundle.files[2]?.source).toBe(FIXTURE["b-team/alerts.sigil"] as string);
  });

  test("names the directory as its source", () => {
    expect(directoryBundle(dir).source).toBe(dir);
  });

  test("a directory that doesn't exist is an error that says what to check", async () => {
    const missing = join(dir, "missing");
    const err = await directoryBundle(missing)
      .read()
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toBe(`reading the AlertRouting policies from ${missing} failed`);
    expect((err as { advice: string[] }).advice).toEqual(["check that the policies directory exists and is readable"]);
  });

  test("a change to a file changes the fingerprint", async () => {
    const before = (await directoryBundle(dir).read()).fingerprint;
    await writeFile(join(dir, "Root.sigil"), "root, changed\n");
    const after = (await directoryBundle(dir).read()).fingerprint;
    await writeFile(join(dir, "Root.sigil"), FIXTURE["Root.sigil"] as string);
    expect(after).not.toBe(before);
    expect((await directoryBundle(dir).read()).fingerprint).toBe(before);
  });
});

describe("embeddedBundle", () => {
  test("fingerprints the same files the same way, in any order", async () => {
    const onDisk = await directoryBundle(dir).read();
    const shuffled = [...onDisk.files].reverse();
    expect(fingerprint(shuffled)).toBe(GO_FINGERPRINT);
    const embedded = embeddedBundle(shuffled);
    expect(embedded.source).toBe(SOURCE_EMBEDDED);
    expect((await embedded.read()).fingerprint).toBe(GO_FINGERPRINT);
  });

  test("the bundled team policies fingerprint like policies/teams on disk", async () => {
    const onDisk = await directoryBundle(join(import.meta.dirname, "../../../policies/teams")).read();
    expect((await embeddedBundle(TEAM_FILES).read()).fingerprint).toBe(onDisk.fingerprint);
  });
});
