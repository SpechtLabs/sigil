import { describe, expect, test } from "bun:test";
import contributes from "../package.json" with { type: "json" };
import type { Resolution } from "../src/binary";
import { type Getter, needsRestart, RESTART_ON, readSettings, resolutionError, SECTION } from "../src/settings";

/** A getter over a plain object of sigil.* values. */
function getter(values: Record<string, unknown>): Getter {
  return <T>(key: string, fallback: T): T => (key in values ? (values[key] as T) : fallback);
}

describe("readSettings", () => {
  const cases: { name: string; values: Record<string, unknown>; want: ReturnType<typeof readSettings> }[] = [
    { name: "defaults", values: {}, want: { path: "", serverEnabled: true } },
    {
      name: "values as set, the path trimmed",
      values: { path: "  /opt/sigil  ", "server.enabled": false },
      want: { path: "/opt/sigil", serverEnabled: false },
    },
    {
      name: "values of the wrong type fall back to the defaults",
      values: { path: 42, "server.enabled": "no" },
      want: { path: "", serverEnabled: true },
    },
  ];
  for (const c of cases) {
    test(c.name, () => {
      expect(readSettings(getter(c.values))).toEqual(c.want);
    });
  }
});

describe("needsRestart", () => {
  const cases: { changed: string[]; want: boolean }[] = [
    { changed: ["sigil.path"], want: true },
    { changed: ["sigil.server.enabled"], want: true },
    { changed: ["sigil.trace.server"], want: false },
    { changed: ["editor.fontSize"], want: false },
    { changed: [], want: false },
  ];
  for (const c of cases) {
    test(`${c.changed.join(", ") || "nothing"} changed`, () => {
      // affectsConfiguration matches a key and every key below it.
      const affects = (section: string) => c.changed.some((k) => k === section || k.startsWith(`${section}.`));
      expect(needsRestart(affects)).toBe(c.want);
    });
  }
});

test("every setting the extension reads or restarts for is declared in package.json", () => {
  const declared = Object.keys(contributes.contributes.configuration.properties);
  for (const key of [...RESTART_ON, `${SECTION}.trace.server`, `${SECTION}.checkForUpdates`])
    expect(declared).toContain(key);
  // readSettings reads these keys of the section.
  for (const key of ["path", "server.enabled"]) expect(declared).toContain(`${SECTION}.${key}`);
});

describe("resolutionError", () => {
  const cases: { name: string; res: Resolution; want: string | undefined }[] = [
    { name: "found", res: { kind: "found", path: "/usr/bin/sigil", source: "path" }, want: undefined },
    {
      name: "a path setting",
      res: { kind: "bad-setting", setting: "~/sigil", reason: "missing", onPath: false, tried: ["/home/me/sigil"] },
      want: `sigil.path is set to "~/sigil", but there's no executable at /home/me/sigil. Fix the setting, or clear it to use the bundled sigil.`,
    },
    {
      name: "a command name setting",
      res: {
        kind: "bad-setting",
        setting: "sigil-dev",
        reason: "missing",
        onPath: true,
        tried: ["/a/sigil-dev", "/b/sigil-dev"],
      },
      want: `sigil.path is set to "sigil-dev", but there's no sigil-dev on PATH. Fix the setting, or clear it to use the bundled sigil.`,
    },
    {
      name: "a relative path in an untrusted workspace",
      res: { kind: "bad-setting", setting: "bin/sigil", reason: "untrusted", onPath: false, tried: [] },
      want: `sigil.path is set to "bin/sigil", a path relative to the workspace, which an untrusted workspace can't choose. Trust the workspace, or make sigil.path absolute.`,
    },
    {
      name: "a batch file on Windows",
      res: { kind: "bad-setting", setting: "C:\\tools\\sigil.cmd", reason: "batch-file", onPath: false, tried: [] },
      want: `sigil.path is set to "C:\\tools\\sigil.cmd", a batch file, which can't be started without a shell. Point it at sigil.exe.`,
    },
    {
      name: "missing",
      res: { kind: "missing", tried: ["/ext/bin/sigil"] },
      want: "Couldn't find the sigil binary: this extension build doesn't bundle one, and there's no sigil on PATH. Install sigil, or point sigil.path at it.",
    },
  ];
  for (const c of cases) {
    test(c.name, () => {
      expect(resolutionError(c.res)).toBe(c.want);
    });
  }
});
