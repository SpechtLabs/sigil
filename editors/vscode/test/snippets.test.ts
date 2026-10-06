// Expands the snippets as VS Code would with their defaults, fills in the
// placeholders a user has to type, and checks the result with the sigil CLI:
// the kind skeleton and the policy and module snippets must make a bundle
// that `sigil check` accepts, in the layout `sigil fmt` writes.

import { describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { sigilCLI } from "./sigil-cli";

interface Snippet {
  prefix: string;
  description: string;
  body: string[];
}

const snippets: Record<string, Snippet> = JSON.parse(
  readFileSync(join(import.meta.dir, "../snippets/sigil.code-snippets"), "utf8"),
);

/** The snippet with this prefix. */
function snippet(prefix: string): Snippet {
  const found = Object.values(snippets).find((s) => s.prefix === prefix);
  if (found === undefined) throw new Error(`no snippet with prefix ${prefix}`);
  return found;
}

/**
 * Expands a snippet: each tabstop takes its value from fill, else its
 * default; a mirrored ${n} repeats tabstop n's value, $0 is fill[0] or
 * nothing, and a tab is two spaces, the indentation sigil fmt writes.
 */
function expand(prefix: string, fill: Record<number, string> = {}): string {
  const values: Record<number, string> = { ...fill };
  const text = snippet(prefix).body.join("\n");
  const withDefaults = text.replace(/\$\{(\d+):([^}]*)\}/g, (_, n: string, def: string) => {
    values[Number(n)] ??= def;
    return values[Number(n)] ?? def;
  });
  return withDefaults
    .replace(/\$\{(\d+)\}/g, (_, n: string) => values[Number(n)] ?? "")
    .replace(/\$0/g, fill[0] ?? "")
    .replace(/\t/g, "  ");
}

describe("snippets", () => {
  test("each has a prefix, a description and a body", () => {
    for (const [name, s] of Object.entries(snippets)) {
      expect(s.prefix, name).not.toBe("");
      expect(s.description, name).not.toBe("");
      expect(s.body.length, name).toBeGreaterThan(0);
    }
  });

  test("expand mirrors tabstops and takes the defaults", () => {
    expect(expand("decide")).toBe("deny(reason: no_rule_matched)");
    expect(expand("kind")).toContain("precedence deny > allow\n\ndefault deny(reason: no_rule_matched)");
  });

  const sigil = sigilCLI();
  test.skipIf(sigil === undefined)("make a bundle sigil check accepts, formatted as sigil fmt writes it", () => {
    const dir = mkdtempSync(join(tmpdir(), "sigil-snippets-"));
    const kind = [
      expand("kind").trimEnd(),
      "",
      expand("enum", { 1: "Tier", 2: "critical", 3: "standard" }),
      "",
      expand("type", { 1: "Actor", 2: "name" }),
      "",
      expand("decision", { 1: "review", 2: "owner", 3: "approvers", 4: "list<string>" }),
      "",
    ].join("\n");
    // review joins the kind's precedence, which must name every decision.
    const bundle = kind.replace("precedence deny > allow", "precedence deny > review > allow");
    const module = `${expand("module", { 0: 'request != ""' })}\n`;
    const policy = [
      expand("policy").trimEnd(),
      "",
      expand("use", { 2: "name" }),
      "",
      expand("param", { 1: "approvers", 2: "list<string>" }),
      "",
      expand("when", { 1: 'request == "admin"' }),
      "",
      expand("when", { 1: "name", 2: "review", 3: "owner, approvers: approvers" }),
      "",
      expand("assert", { 1: "named", 2: 'request != ""' }),
      "",
    ].join("\n");
    writeFileSync(join(dir, "kind.sigil"), bundle);
    writeFileSync(join(dir, "common.sigil"), module);
    writeFileSync(join(dir, "production.sigil"), policy);

    const run = (...args: string[]) => execFileSync(sigil ?? "", args, { cwd: dir, encoding: "utf8", stdio: "pipe" });
    try {
      run("check", ".");
      run("fmt", "--check", ".");
    } catch (err) {
      const e = err as { stdout?: string; stderr?: string };
      throw new Error(
        `${e.stdout ?? ""}${e.stderr ?? ""}\n--- kind.sigil\n${bundle}\n--- common.sigil\n${module}\n--- production.sigil\n${policy}`,
      );
    }
  });
});
