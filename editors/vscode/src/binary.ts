// Finds the sigil binary the language server runs as. Nothing here imports
// vscode: the extension passes in what it read from the editor, so the unit
// tests drive every path, Windows' included, with plain values.

import { constants } from "node:fs";
import { access, chmod, stat } from "node:fs/promises";
import { type PlatformPath, posix, win32 } from "node:path";

/** Where the binary came from, in the order they're tried. */
export type Source = "setting" | "bundled" | "path";

/** A binary that exists and can be run. */
export interface Found {
  kind: "found";
  path: string;
  source: Source;
}

/** sigil.path names something that isn't there; nothing else is tried. */
export interface BadSetting {
  kind: "bad-setting";
  setting: string;
  /** Whether the setting is a command name, looked up on PATH. */
  onPath: boolean;
  /** The paths the setting was looked up as. */
  tried: string[];
}

/** No setting, no bundled binary and no sigil on PATH. */
export interface Missing {
  kind: "missing";
  tried: string[];
}

export type Resolution = Found | BadSetting | Missing;

/** What resolution reads from the editor and the environment. */
export interface Inputs {
  /** sigil.path, trimmed; empty when unset. */
  setting: string;
  /** The extension's install directory, which holds bin/ in a platform VSIX. */
  extensionPath: string;
  /** The first workspace folder, which a relative sigil.path is relative to. */
  workspaceFolder: string | undefined;
  /** The user's home directory, for a sigil.path starting with ~/. */
  home: string;
  /** process.platform. */
  platform: NodeJS.Platform;
  /** The environment PATH, and on Windows PATHEXT, are read from. */
  env: Record<string, string | undefined>;
  /** Reports whether a file exists and can be run; defaults to the file system. */
  isExecutable?: (path: string) => Promise<boolean>;
}

/** The binary's file name on a platform. */
export function binaryName(platform: NodeJS.Platform): string {
  return platform === "win32" ? "sigil.exe" : "sigil";
}

/** Where a platform VSIX keeps the release binary. */
export function bundledPath(extensionPath: string, platform: NodeJS.Platform): string {
  return paths(platform).join(extensionPath, "bin", binaryName(platform));
}

/**
 * Finds the binary: sigil.path when set, then the one bundled with the
 * extension, then sigil on PATH. A sigil.path that doesn't resolve is an
 * error rather than a reason to fall back, so a typo never runs some other
 * sigil without saying so.
 */
export async function resolveBinary(inputs: Inputs): Promise<Resolution> {
  const isExecutable = inputs.isExecutable ?? fileIsExecutable;

  if (inputs.setting !== "") {
    const candidates = settingCandidates(inputs);
    for (const candidate of candidates) {
      if (await isExecutable(candidate)) return { kind: "found", path: candidate, source: "setting" };
    }
    return {
      kind: "bad-setting",
      setting: inputs.setting,
      onPath: isCommandName(inputs.setting),
      tried: candidates,
    };
  }

  const bundled = bundledPath(inputs.extensionPath, inputs.platform);
  const tried = [bundled];
  if (await isExecutable(bundled)) return { kind: "found", path: bundled, source: "bundled" };

  for (const candidate of pathCandidates("sigil", inputs)) {
    tried.push(candidate);
    if (await isExecutable(candidate)) return { kind: "found", path: candidate, source: "path" };
  }
  return { kind: "missing", tried };
}

/**
 * Makes the bundled binary executable when it isn't. A VSIX is a zip, and an
 * extractor that drops the file mode would leave the binary unrunnable. It
 * does nothing on Windows, or when there's no bundled binary.
 */
export async function ensureBundledExecutable(extensionPath: string, platform: NodeJS.Platform): Promise<void> {
  if (platform === "win32") return;
  const bundled = bundledPath(extensionPath, platform);
  try {
    const info = await stat(bundled);
    if ((info.mode & 0o111) === 0) await chmod(bundled, info.mode | 0o755);
  } catch {
    // No bundled binary (a universal VSIX or a dev build), or one we can't
    // change; resolution reports what it finds either way.
  }
}

/** The path functions for a platform, so Windows paths resolve on any host. */
function paths(platform: NodeJS.Platform): PlatformPath {
  return platform === "win32" ? win32 : posix;
}

/** The paths sigil.path can mean, in the order they're tried. */
function settingCandidates(inputs: Inputs): string[] {
  const { setting, platform } = inputs;
  const p = paths(platform);
  if (setting === "~" || setting.startsWith("~/") || setting.startsWith("~\\")) {
    return [p.join(inputs.home, setting.slice(1))];
  }
  if (p.isAbsolute(setting)) return [setting];
  if (isCommandName(setting)) return pathCandidates(setting, inputs);
  return [p.resolve(inputs.workspaceFolder ?? inputs.home, setting)];
}

/** A bare name, such as sigil or sigil-dev, which is looked up on PATH. */
function isCommandName(setting: string): boolean {
  return !setting.startsWith("~") && !setting.includes("/") && !setting.includes("\\");
}

/** Every file a command name could be on PATH, with PATHEXT's extensions on Windows. */
function pathCandidates(command: string, inputs: Inputs): string[] {
  const windows = inputs.platform === "win32";
  const p = paths(inputs.platform);
  const pathVar = windows ? (inputs.env.Path ?? inputs.env.PATH) : inputs.env.PATH;
  const dirs = (pathVar ?? "").split(p.delimiter).filter((dir) => dir !== "");
  const exts = windows
    ? (inputs.env.PATHEXT ?? ".COM;.EXE;.BAT;.CMD")
        .split(";")
        .filter((ext) => ext !== "")
        .map((ext) => ext.toLowerCase())
    : [""];

  const out: string[] = [];
  for (const dir of dirs) {
    for (const ext of exts) {
      out.push(p.join(dir, command.toLowerCase().endsWith(ext) ? command : command + ext));
    }
  }
  return out;
}

/** Whether path is a file the current user can run. */
async function fileIsExecutable(path: string): Promise<boolean> {
  try {
    const info = await stat(path);
    if (!info.isFile()) return false;
    if (process.platform !== "win32") await access(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}
