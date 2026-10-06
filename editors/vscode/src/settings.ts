// The extension's settings, read through a getter so the unit tests don't
// need vscode. A new setting is a row in package.json's contributes and a
// field here; RESTART_ON lists the ones the server has to restart for.

import type { Resolution } from "./binary";

/** The settings section; every key below lives under it. */
export const SECTION = "sigil";

/** What the extension reads from the sigil section. */
export interface Settings {
  /** sigil.path, trimmed: empty means bundled, then PATH. */
  path: string;
  /** sigil.server.enabled: false keeps highlighting and snippets only. */
  serverEnabled: boolean;
}

/**
 * The settings a running server can't pick up. sigil.trace.server isn't
 * here: the language client applies it to the running server itself.
 */
export const RESTART_ON: readonly string[] = ["sigil.path", "sigil.server.enabled"];

/** Reads a key of the sigil section, or the fallback when it's unset or the wrong type. */
export type Getter = <T>(key: string, fallback: T) => T;

/** Reads the settings, ignoring values of the wrong type. */
export function readSettings(get: Getter): Settings {
  const path = get<unknown>("path", "");
  const enabled = get<unknown>("server.enabled", true);
  return {
    path: typeof path === "string" ? path.trim() : "",
    serverEnabled: typeof enabled === "boolean" ? enabled : true,
  };
}

/** Whether a configuration change touches a setting the server restarts for. */
export function needsRestart(affects: (section: string) => boolean): boolean {
  return RESTART_ON.some((key) => affects(key));
}

/** The install instructions, which a missing binary's error links to. */
export const INSTALL_URL = "https://sigil.specht-labs.de/guides/editors/vscode/#install-sigil";

/** The error to show for a binary that wasn't found, or undefined when it was. */
export function resolutionError(res: Resolution): string | undefined {
  switch (res.kind) {
    case "found":
      return undefined;
    case "bad-setting": {
      const where = res.onPath ? `no ${res.setting} on PATH` : `no executable at ${res.tried[0]}`;
      return `sigil.path is set to "${res.setting}", but there's ${where}. Fix the setting, or clear it to use the bundled sigil.`;
    }
    case "missing":
      return "Couldn't find the sigil binary: this extension build doesn't bundle one, and there's no sigil on PATH. Install sigil, or point sigil.path at it.";
  }
}
