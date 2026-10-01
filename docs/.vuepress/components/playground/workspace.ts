// The playground's workspace: the files, the input and stubs, and which
// policy runs. It's what a preset fills in and what a shared link carries.

import type { SourceFile } from "@spechtlabs/sigil/worker";

/** What the Run button does. Test mode joins Evaluate later. */
export type Mode = "evaluate";

export interface Workspace {
  files: SourceFile[];
  /** The input document, JSON or YAML. */
  input: string;
  /** Host function stubs in the format of a test file's `stubs:`, YAML. */
  stubs: string;
  /** The root policy; empty means the only one the files define. */
  policy: string;
  mode: Mode;
}

export const modes: { id: Mode; label: string }[] = [{ id: "evaluate", label: "Evaluate" }];

const POLICY_HEADER = /^[ \t]*policy[ \t]+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)/gm;
const DEFINITION_HEADER = /^[ \t]*(?:policy|module)[ \t]+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)/gm;
const PATH_SEGMENT = /^[A-Za-z0-9_][A-Za-z0-9_.-]*$/;

/**
 * The names of the policies the files define, in file order. It reads the
 * `policy` headers, so the picker keeps working while a file doesn't
 * compile.
 */
export function policyNames(files: SourceFile[]): string[] {
  const names: string[] = [];
  for (const f of files) {
    for (const m of f.source.matchAll(POLICY_HEADER)) {
      if (!names.includes(m[1])) names.push(m[1]);
    }
  }
  return names;
}

/**
 * Which file defines each policy and module, for following an explanation's
 * chain, which names policies rather than files.
 */
export function definitions(files: SourceFile[]): Map<string, string> {
  const defs = new Map<string, string>();
  for (const f of files) {
    for (const m of f.source.matchAll(DEFINITION_HEADER)) {
      if (!defs.has(m[1])) defs.set(m[1], f.path);
    }
  }
  return defs;
}

/**
 * Why a path can't name a file of the workspace, or undefined when it can.
 * Paths are relative and end in .sigil, the way the CLI reads a directory.
 */
export function pathProblem(path: string, files: SourceFile[], current?: string): string | undefined {
  if (path === "") return "Give the file a name.";
  if (!path.endsWith(".sigil")) return "Sigil files end in .sigil.";
  if (path.split("/").some((s) => !PATH_SEGMENT.test(s))) {
    return "Use a relative path of letters, digits, _, - and ., like checkout/alerts.sigil.";
  }
  if (path !== current && files.some((f) => f.path === path)) return `There's already a file named ${path}.`;
  return undefined;
}

/** A name for a new file that no file has yet. */
export function freshPath(files: SourceFile[]): string {
  for (let i = 1; ; i++) {
    const path = i === 1 ? "untitled.sigil" : `untitled-${i}.sigil`;
    if (!files.some((f) => f.path === path)) return path;
  }
}

/** Splits a trace position, `file:line:column`, into its parts. */
export function parsePosition(position: string | undefined): { file: string; line: number; column: number } | undefined {
  const m = position === undefined ? null : /^(.*):(\d+):(\d+)$/.exec(position);
  return m === null ? undefined : { file: m[1], line: Number(m[2]), column: Number(m[3]) };
}
