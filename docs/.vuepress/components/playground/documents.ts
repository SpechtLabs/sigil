// Reading the input and stubs panes. The input reads like `sigil eval
// --input`: JSON first, YAML when it isn't JSON. Stubs read like a test
// file's `stubs:`.
//
// YAML is parsed with the YAML 1.2 core schema, the JSON-compatible one,
// and never with YAML 1.1 rules or a JavaScript-specific schema: those turn
// `2026-01-01` into a Date and `no` into false before the engine sees them.
// Under the core schema an unquoted date or timestamp stays the string it
// was written as, which is what a timestamp input wants, and an unquoted
// `1.10` is a number, as it is for the CLI, so a string field rejects it
// instead of silently reading "1.1". Quote such strings, as the CLI
// reference says. Merge keys, `<<: *anchor`, work as they do in the CLI's
// YAML parser.
//
// Numbers become JavaScript numbers, so an int beyond ±2^53 loses
// precision before the engine sees it, where the CLI keeps it exact.

import type { JsonValue, Stub } from "@spechtlabs/sigil/worker";
import { parse, YAMLParseError } from "yaml";

/** A document that doesn't parse, with where, when the parser knows. */
export class DocumentError extends Error {
  readonly line: number | undefined;

  constructor(message: string, line?: number) {
    super(message);
    this.line = line;
  }
}

/** The input document as the object `policy.eval` takes. */
export function parseInput(text: string): Record<string, JsonValue> {
  if (text.trim() === "") {
    throw new DocumentError("The input is empty. Give an object with one key per input the kind declares.");
  }
  const value = parseDocument(text, "input");
  if (!isObject(value)) {
    throw new DocumentError("The input must be an object with one key per input the kind declares.");
  }
  return value;
}

/** The stubs, or undefined when the pane is empty. */
export function parseStubs(text: string): Record<string, Stub> | undefined {
  if (text.trim() === "") return undefined;
  const value = parseDocument(text, "stubs");
  if (value === null) return undefined;
  if (!isObject(value)) {
    throw new DocumentError("Stubs map each host function's name to a stub, like a test file's stubs:.");
  }
  for (const [name, stub] of Object.entries(value)) {
    if (!isObject(stub)) {
      throw new DocumentError(`The stub for ${name} must be an object with returns:, error: or calls:.`);
    }
  }
  return value as Record<string, Stub>;
}

function parseDocument(text: string, what: string): JsonValue {
  try {
    return JSON.parse(text) as JsonValue;
  } catch {
    // Not JSON; YAML is a superset, so its error is the one to show.
  }
  try {
    return parse(text, { schema: "core", merge: true, uniqueKeys: true, prettyErrors: false }) as JsonValue;
  } catch (err) {
    if (err instanceof YAMLParseError) {
      const line = err.linePos?.[0].line;
      throw new DocumentError(`The ${what} isn't JSON or YAML${line === undefined ? "" : ` (line ${line})`}: ${firstLine(err.message)}`, line);
    }
    throw err;
  }
}

function isObject(v: unknown): v is Record<string, JsonValue> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function firstLine(s: string): string {
  return s.split("\n")[0].replace(/ at line \d+, column \d+:?$/, "");
}
