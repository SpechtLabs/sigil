// The playground's workspace: the files, the input and stubs, and what Run
// does. It's what a preset fills in and what a shared link carries.

import type { SourceFile } from "@spechtlabs/sigil/worker";

/** What the Run button does: evaluate one policy, or run the test files. */
export type Mode = "evaluate" | "test";

/**
 * What a file is, from its name, the way `sigil test` reads a directory:
 * Sigil documents, test files, and the data files cases name.
 */
export type FileKind = "sigil" | "test" | "data";

export interface Workspace {
  /** Every file: Sigil documents, test files and data files. */
  files: SourceFile[];
  /** The input document, JSON or YAML. */
  input: string;
  /** Host function stubs in the format of a test file's `stubs:`, YAML. */
  stubs: string;
  /** The root policy; empty means the only one the files define. */
  policy: string;
  mode: Mode;
  /** The regular expression that picks the test cases to run; empty runs all. */
  run: string;
}

export const modes: { id: Mode; label: string }[] = [
  { id: "evaluate", label: "Evaluate" },
  { id: "test", label: "Test" },
];

const POLICY_HEADER = /^[ \t]*policy[ \t]+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)/gm;
const DEFINITION_HEADER = /^[ \t]*(?:policy|module)[ \t]+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)/gm;
const PATH_SEGMENT = /^[A-Za-z0-9_][A-Za-z0-9_.-]*$/;
const TEST_FILE = /_test\.ya?ml$/;
const DATA_FILE = /\.(?:json|ya?ml)$/;

export function fileKind(path: string): FileKind {
  if (path.endsWith(".sigil")) return "sigil";
  return TEST_FILE.test(path) ? "test" : "data";
}

/** The files of one kind, in workspace order. */
export function filesOf(files: SourceFile[], kind: FileKind): SourceFile[] {
  return files.filter((f) => fileKind(f.path) === kind).map((f) => ({ path: f.path, source: f.source }));
}

/**
 * The names of the policies the files define, in file order. It reads the
 * `policy` headers, so the picker keeps working while a file doesn't
 * compile.
 */
export function policyNames(files: SourceFile[]): string[] {
  const names: string[] = [];
  for (const f of filesOf(files, "sigil")) {
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
  for (const f of filesOf(files, "sigil")) {
    for (const m of f.source.matchAll(DEFINITION_HEADER)) {
      if (!defs.has(m[1])) defs.set(m[1], f.path);
    }
  }
  return defs;
}

/**
 * Why a path can't name a file of the workspace, or undefined when it can.
 * Paths are relative, and the name says what the file is: .sigil for Sigil
 * documents, _test.yaml for test files, .json or .yaml for data.
 */
export function pathProblem(path: string, files: SourceFile[], current?: string): string | undefined {
  if (path === "") return "Give the file a name.";
  if (!path.endsWith(".sigil") && !DATA_FILE.test(path)) {
    return "Name Sigil files .sigil, test files _test.yaml, and data files .json or .yaml.";
  }
  if (path.split("/").some((s) => !PATH_SEGMENT.test(s))) {
    return "Use a relative path of letters, digits, _, - and ., like checkout/alerts.sigil.";
  }
  if (path !== current && files.some((f) => f.path === path)) return `There's already a file named ${path}.`;
  return undefined;
}

/** A name for a new file that no file has yet, `untitled.sigil` or a test file. */
export function freshPath(files: SourceFile[], kind: "sigil" | "test" = "sigil", dir = ""): string {
  const ext = kind === "sigil" ? ".sigil" : "_test.yaml";
  for (let i = 1; ; i++) {
    const path = `${dir}${i === 1 ? "untitled" : `untitled-${i}`}${ext}`;
    if (!files.some((f) => f.path === path)) return path;
  }
}

/** The directory part of a path, with its trailing slash, or "". */
export function dirOf(path: string): string {
  return path.slice(0, path.lastIndexOf("/") + 1);
}

/** Splits a trace position, `file:line:column`, into its parts. */
export function parsePosition(position: string | undefined): { file: string; line: number; column: number } | undefined {
  const m = position === undefined ? null : /^(.*):(\d+):(\d+)$/.exec(position);
  return m === null ? undefined : { file: m[1], line: Number(m[2]), column: Number(m[3]) };
}
