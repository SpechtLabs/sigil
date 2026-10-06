import { describe, expect, test } from "bun:test";
import { chmod, mkdir, mkdtemp, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  binaryName,
  bundledPath,
  ensureBundledExecutable,
  type Inputs,
  type Resolution,
  resolveBinary,
} from "../src/binary";

const EXT = "/ext";
const WIN_EXT = "C:\\ext";

/** Inputs for a Unix host where only the listed files exist and run. */
function unix(setting: string, files: string[], overrides: Partial<Inputs> = {}): Inputs {
  const present = new Set(files);
  return {
    setting,
    extensionPath: EXT,
    workspaceFolder: "/work",
    workspaceTrusted: true,
    home: "/home/me",
    platform: "linux",
    env: { PATH: "/usr/local/bin:/usr/bin" },
    isExecutable: async (path) => present.has(path),
    ...overrides,
  };
}

/** Inputs for a Windows host where only the listed files exist. */
function windows(setting: string, files: string[], env: Record<string, string> = {}): Inputs {
  const present = new Set(files);
  return {
    setting,
    extensionPath: WIN_EXT,
    workspaceFolder: "C:\\work",
    workspaceTrusted: true,
    home: "C:\\Users\\me",
    platform: "win32",
    env: { Path: "C:\\tools;C:\\bin\\", PATHEXT: ".COM;.EXE", ...env },
    isExecutable: async (path) => present.has(path),
  };
}

describe("resolveBinary", () => {
  const cases: { name: string; inputs: Inputs; want: Resolution }[] = [
    {
      name: "the setting wins over the bundled binary and PATH",
      inputs: unix("/opt/sigil/bin/sigil", ["/opt/sigil/bin/sigil", "/ext/bin/sigil", "/usr/bin/sigil"]),
      want: { kind: "found", path: "/opt/sigil/bin/sigil", source: "setting" },
    },
    {
      name: "the bundled binary wins over PATH",
      inputs: unix("", ["/ext/bin/sigil", "/usr/bin/sigil"]),
      want: { kind: "found", path: "/ext/bin/sigil", source: "bundled" },
    },
    {
      name: "PATH is searched in order when there's no bundled binary",
      inputs: unix("", ["/usr/local/bin/sigil", "/usr/bin/sigil"]),
      want: { kind: "found", path: "/usr/local/bin/sigil", source: "path" },
    },
    {
      name: "the last PATH entry is found too",
      inputs: unix("", ["/usr/bin/sigil"]),
      want: { kind: "found", path: "/usr/bin/sigil", source: "path" },
    },
    {
      name: "nothing found lists every place tried",
      inputs: unix("", []),
      want: { kind: "missing", tried: ["/ext/bin/sigil", "/usr/local/bin/sigil", "/usr/bin/sigil"] },
    },
    {
      name: "no PATH at all leaves the bundled binary as the only place",
      inputs: unix("", [], { env: {} }),
      want: { kind: "missing", tried: ["/ext/bin/sigil"] },
    },
    {
      name: "a setting that doesn't exist is an error, not a fall back",
      inputs: unix("/nope/sigil", ["/ext/bin/sigil", "/usr/bin/sigil"]),
      want: { kind: "bad-setting", setting: "/nope/sigil", reason: "missing", onPath: false, tried: ["/nope/sigil"] },
    },
    {
      name: "a ~/ setting is under the home directory",
      inputs: unix("~/go/bin/sigil", ["/home/me/go/bin/sigil"]),
      want: { kind: "found", path: "/home/me/go/bin/sigil", source: "setting" },
    },
    {
      name: "a relative setting is relative to the workspace folder",
      inputs: unix("bin/sigil", ["/work/bin/sigil"]),
      want: { kind: "found", path: "/work/bin/sigil", source: "setting" },
    },
    {
      name: "a relative setting without a workspace folder is relative to the home directory",
      inputs: unix("./bin/sigil", ["/home/me/bin/sigil"], { workspaceFolder: undefined }),
      want: { kind: "found", path: "/home/me/bin/sigil", source: "setting" },
    },
    {
      name: "a command name setting is looked up on PATH",
      inputs: unix("sigil-dev", ["/usr/bin/sigil-dev", "/ext/bin/sigil"]),
      want: { kind: "found", path: "/usr/bin/sigil-dev", source: "setting" },
    },
    {
      name: "a command name setting that isn't on PATH is an error",
      inputs: unix("sigil-dev", ["/ext/bin/sigil"]),
      want: {
        kind: "bad-setting",
        setting: "sigil-dev",
        reason: "missing",
        onPath: true,
        tried: ["/usr/local/bin/sigil-dev", "/usr/bin/sigil-dev"],
      },
    },
    {
      name: "relative PATH entries are skipped, so a workspace's bin can't answer for sigil",
      inputs: unix("", ["bin/sigil", "./tools/sigil", "/usr/bin/sigil"], { env: { PATH: "bin:./tools::/usr/bin" } }),
      want: { kind: "found", path: "/usr/bin/sigil", source: "path" },
    },
    {
      name: "with only relative PATH entries, nothing is found on PATH",
      inputs: unix("", ["bin/sigil"], { env: { PATH: "bin" } }),
      want: { kind: "missing", tried: ["/ext/bin/sigil"] },
    },
    {
      name: "a command name setting skips relative PATH entries too",
      inputs: unix("sigil-dev", ["bin/sigil-dev"], { env: { PATH: "bin" } }),
      want: { kind: "bad-setting", setting: "sigil-dev", reason: "missing", onPath: true, tried: [] },
    },
    {
      name: "an untrusted workspace can't resolve a relative setting, even one in user settings",
      inputs: unix("bin/sigil", ["/work/bin/sigil"], { workspaceTrusted: false }),
      want: { kind: "bad-setting", setting: "bin/sigil", reason: "untrusted", onPath: false, tried: [] },
    },
    {
      name: "an untrusted workspace still resolves an absolute setting",
      inputs: unix("/opt/sigil", ["/opt/sigil"], { workspaceTrusted: false }),
      want: { kind: "found", path: "/opt/sigil", source: "setting" },
    },
    {
      name: "an untrusted workspace still resolves a ~/ setting",
      inputs: unix("~/go/bin/sigil", ["/home/me/go/bin/sigil"], { workspaceTrusted: false }),
      want: { kind: "found", path: "/home/me/go/bin/sigil", source: "setting" },
    },
    {
      name: "an untrusted workspace still looks a command name up on PATH",
      inputs: unix("sigil", ["/usr/bin/sigil"], { workspaceTrusted: false }),
      want: { kind: "found", path: "/usr/bin/sigil", source: "setting" },
    },
    {
      name: "an untrusted workspace still uses the bundled binary",
      inputs: unix("", ["/ext/bin/sigil"], { workspaceTrusted: false }),
      want: { kind: "found", path: "/ext/bin/sigil", source: "bundled" },
    },
    {
      name: "Windows: the bundled binary is sigil.exe",
      inputs: windows("", ["C:\\ext\\bin\\sigil.exe"]),
      want: { kind: "found", path: "C:\\ext\\bin\\sigil.exe", source: "bundled" },
    },
    {
      name: "Windows: PATH entries get PATHEXT's extensions",
      inputs: windows("", ["C:\\bin\\sigil.exe"]),
      want: { kind: "found", path: "C:\\bin\\sigil.exe", source: "path" },
    },
    {
      name: "Windows: nothing found",
      inputs: windows("", []),
      want: {
        kind: "missing",
        tried: [
          "C:\\ext\\bin\\sigil.exe",
          "C:\\tools\\sigil.com",
          "C:\\tools\\sigil.exe",
          "C:\\bin\\sigil.com",
          "C:\\bin\\sigil.exe",
        ],
      },
    },
    {
      name: "Windows: PATH is read under either spelling, with a default PATHEXT",
      inputs: { ...windows("", ["D:\\sigil.exe"]), env: { PATH: "D:\\" } },
      want: { kind: "found", path: "D:\\sigil.exe", source: "path" },
    },
    {
      name: "Windows: a command name that has its extension isn't given another",
      inputs: windows("sigil.exe", ["C:\\tools\\sigil.exe"]),
      want: { kind: "found", path: "C:\\tools\\sigil.exe", source: "setting" },
    },
    {
      name: "Windows: an absolute setting",
      inputs: windows("D:\\sigil\\sigil.exe", ["D:\\sigil\\sigil.exe"]),
      want: { kind: "found", path: "D:\\sigil\\sigil.exe", source: "setting" },
    },
    {
      name: "Windows: a relative setting is relative to the workspace folder",
      inputs: windows("bin\\sigil.exe", ["C:\\work\\bin\\sigil.exe"]),
      want: { kind: "found", path: "C:\\work\\bin\\sigil.exe", source: "setting" },
    },
    {
      name: "Windows: .BAT and .CMD on PATHEXT are skipped, since they need a shell",
      inputs: windows("", ["C:\\tools\\sigil.cmd", "C:\\bin\\sigil.exe"], { PATHEXT: ".COM;.EXE;.BAT;.CMD" }),
      want: { kind: "found", path: "C:\\bin\\sigil.exe", source: "path" },
    },
    {
      name: "Windows: a batch file setting is refused",
      inputs: windows("C:\\tools\\sigil.CMD", ["C:\\tools\\sigil.CMD"]),
      want: { kind: "bad-setting", setting: "C:\\tools\\sigil.CMD", reason: "batch-file", onPath: false, tried: [] },
    },
    {
      name: "Windows: relative PATH entries are skipped",
      inputs: { ...windows("", ["bin\\sigil.exe", "D:\\sigil.exe"]), env: { Path: "bin;D:\\", PATHEXT: ".EXE" } },
      want: { kind: "found", path: "D:\\sigil.exe", source: "path" },
    },
    {
      name: "Windows: an untrusted workspace can't resolve a relative setting",
      inputs: { ...windows("bin\\sigil.exe", ["C:\\work\\bin\\sigil.exe"]), workspaceTrusted: false },
      want: { kind: "bad-setting", setting: "bin\\sigil.exe", reason: "untrusted", onPath: false, tried: [] },
    },
    {
      name: "Windows: a ~\\ setting is under the home directory",
      inputs: windows("~\\sigil.exe", ["C:\\Users\\me\\sigil.exe"]),
      want: { kind: "found", path: "C:\\Users\\me\\sigil.exe", source: "setting" },
    },
  ];

  for (const c of cases) {
    test(c.name, async () => {
      expect(await resolveBinary(c.inputs)).toEqual(c.want);
    });
  }
});

describe("the file system", () => {
  test("finds a real executable, and skips a file that can't run and a directory", async () => {
    const dir = await mkdtemp(join(tmpdir(), "sigil-binary-"));
    const ext = join(dir, "ext");
    const pathDir = join(dir, "path");
    await mkdir(join(ext, "bin"), { recursive: true });
    await mkdir(join(pathDir, "sigil"), { recursive: true }); // a directory named sigil
    const plain = join(dir, "plain");
    await writeFile(plain, "");
    await chmod(plain, 0o644);
    const runnable = join(dir, "runnable");
    await writeFile(runnable, "#!/bin/sh\n");
    await chmod(runnable, 0o755);

    const base = {
      extensionPath: ext,
      workspaceFolder: dir,
      workspaceTrusted: true,
      home: dir,
      platform: process.platform,
      env: { PATH: pathDir },
    };
    expect(await resolveBinary({ ...base, setting: runnable })).toEqual({
      kind: "found",
      path: runnable,
      source: "setting",
    });
    if (process.platform !== "win32") {
      expect((await resolveBinary({ ...base, setting: plain })).kind).toBe("bad-setting");
    }
    expect((await resolveBinary({ ...base, setting: "" })).kind).toBe("missing");
  });

  test.skipIf(process.platform === "win32")("makes a bundled binary that lost its mode executable", async () => {
    const ext = await mkdtemp(join(tmpdir(), "sigil-bundled-"));
    await mkdir(join(ext, "bin"));
    const bundled = bundledPath(ext, process.platform);
    await writeFile(bundled, "#!/bin/sh\n");
    await chmod(bundled, 0o644);

    await ensureBundledExecutable(ext, process.platform);
    expect((await stat(bundled)).mode & 0o777).toBe(0o755);

    // Already executable: left as it is.
    await chmod(bundled, 0o700);
    await ensureBundledExecutable(ext, process.platform);
    expect((await stat(bundled)).mode & 0o777).toBe(0o700);

    const found = await resolveBinary({
      setting: "",
      extensionPath: ext,
      workspaceFolder: undefined,
      workspaceTrusted: true,
      home: ext,
      platform: process.platform,
      env: {},
    });
    expect(found).toEqual({ kind: "found", path: bundled, source: "bundled" });
  });

  test("leaves things alone without a bundled binary, and on Windows", async () => {
    const ext = await mkdtemp(join(tmpdir(), "sigil-unbundled-"));
    await ensureBundledExecutable(ext, process.platform);
    await ensureBundledExecutable(ext, "win32");
  });
});

test("binaryName", () => {
  expect(binaryName("darwin")).toBe("sigil");
  expect(binaryName("linux")).toBe("sigil");
  expect(binaryName("win32")).toBe("sigil.exe");
});
