// Tokenizes every .sigil file in the repository with the extension's
// grammar. The files sigil-lint checks are valid Sigil, so none of them may
// have a token the grammar marks invalid.illegal: a scope like that on valid
// source is a grammar bug. The caret assertions (test/grammar/assertions)
// and the snapshots (test/grammar/snapshots) pin the scopes themselves;
// `bun run test:grammar` runs those.

import { describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import * as oniguruma from "vscode-oniguruma";
import { type IGrammar, INITIAL, parseRawGrammar, Registry } from "vscode-textmate";
import manifest from "../package.json" with { type: "json" };

const root = join(import.meta.dir, "..");
const repo = join(root, "../..");

// The sigil-lint task's exclusions in .mise.toml, less the tree-sitter
// grammar's highlight tests, which are unformatted but valid: inputs that
// don't parse, or don't lex, on purpose.
const EXCLUDED = [
  ":!:internal/parser/testdata",
  ":!:internal/format/testdata",
  ":!:pkg/policy/testdata/errors_check.sigil",
  ":!:pkg/policy/testdata/errors_diagnostics.sigil",
  ":!:cmd/sigil/command/check/testdata/kinds/legacy.sigil",
  ":!:editors/vscode/test/grammar/assertions",
  ":!:editors/vscode/test/grammar/unformatted",
];

async function loadGrammar(): Promise<IGrammar> {
  const wasm = readFileSync(join(root, "node_modules/vscode-oniguruma/release/onig.wasm"));
  await oniguruma.loadWASM(wasm.buffer as ArrayBuffer);
  const paths: Record<string, string> = {};
  for (const g of manifest.contributes.grammars) paths[g.scopeName] = join(root, g.path);
  const registry = new Registry({
    onigLib: Promise.resolve({
      createOnigScanner: (patterns) => new oniguruma.OnigScanner(patterns),
      createOnigString: (s) => new oniguruma.OnigString(s),
    }),
    loadGrammar: async (scopeName) => {
      const path = paths[scopeName];
      return path === undefined ? null : parseRawGrammar(readFileSync(path, "utf8"), path);
    },
  });
  const grammar = await registry.loadGrammar("source.sigil");
  if (grammar === null) throw new Error("source.sigil didn't load");
  return grammar;
}

/** Every token of a file with an invalid.illegal scope, as file:line:col. */
function illegalTokens(grammar: IGrammar, file: string, text: string): string[] {
  const found: string[] = [];
  let stack = INITIAL;
  text.split(/\r?\n/).forEach((line, i) => {
    const res = grammar.tokenizeLine(line, stack);
    for (const t of res.tokens) {
      if (t.scopes.some((s) => s.startsWith("invalid.illegal"))) {
        found.push(`${file}:${i + 1}:${t.startIndex + 1}: ${JSON.stringify(line.slice(t.startIndex, t.endIndex))}`);
      }
    }
    stack = res.ruleStack;
  });
  return found;
}

function sigilFiles(): string[] | undefined {
  try {
    const out = execFileSync(
      "git",
      ["ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "*.sigil", ...EXCLUDED],
      {
        cwd: repo,
        encoding: "utf8",
      },
    );
    return out.split("\0").filter((f) => f !== "");
  } catch {
    return undefined;
  }
}

describe("the grammar", async () => {
  const grammar = await loadGrammar();
  const files = sigilFiles();

  test.skipIf(files === undefined)("marks nothing in the repository's .sigil files invalid", () => {
    const list = files ?? [];
    expect(list.length).toBeGreaterThan(100);
    const illegal = list.flatMap((f) => illegalTokens(grammar, f, readFileSync(join(repo, f), "utf8")));
    expect(illegal).toEqual([]);
  });

  test("marks what the lexer rejects invalid", () => {
    for (const bad of ["0x10", "1e3", "1_000", "1.5h", "30m1h", "1h1h", "1h30", "5w", `"\\'"`]) {
      expect(illegalTokens(grammar, "inline", `let x = ${bad}`)).not.toEqual([]);
    }
  });

  test("keeps highlighting to the end of a long line", () => {
    // A pattern that can match the empty string makes vscode-textmate give up
    // on the rest of the line, as the duration pattern once did after any word
    // ending in d, h, m or s. Every operator below must keep its scope.
    const line = `let x = ${Array.from({ length: 2000 }, (_, i) => `a${i} and b`).join(" or ")}`;
    const res = grammar.tokenizeLine(line, INITIAL);
    const ops = res.tokens.filter((t) => t.scopes.includes("keyword.operator.word.sigil"));
    expect(ops.length).toBe(2000 + 1999);
  });
});

test("the Markdown injection embeds the Sigil grammar", () => {
  const injection = manifest.contributes.grammars.find((g) => g.scopeName === "markdown.sigil.codeblock");
  expect(injection?.injectTo).toEqual(["text.html.markdown"]);
  const raw = readFileSync(join(root, injection?.path ?? ""), "utf8");
  expect(raw).toContain('"include": "source.sigil"');
  expect(raw).toContain("meta.embedded.block.sigil");
});
