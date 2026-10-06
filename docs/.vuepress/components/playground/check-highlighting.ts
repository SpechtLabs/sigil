// Checks that the playground's editors colour every token they tag: each
// ```sigil fence in the docs, and each file of the presets (Sigil, test
// and data files), is parsed as the editors parse it, and a token whose tags the
// highlighter gives no class fails the check. A token without a class takes
// the editor's plain colour, so a tag that slips through (as StreamLanguage's
// legacy token names did) goes unnoticed in light mode and unreadable in
// dark. Then it compares colours: every Sigil source must come out of the
// playground's tokenizer in the colours Shiki gives it with the TextMate
// grammar, as on the docs' fences. `bun run check:playground` runs it, and so
// does the docs build.

import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { ensureSyntaxTree, type Language, type LanguageSupport, type StreamLanguage } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { getStyleTags, highlightTree } from "@lezer/highlight";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { highlighter, sigilSupport } from "./language.ts";
import { fileKind } from "./workspace.ts";
import { createHighlighter } from "shiki";
import sigilGrammar from "../../../../editors/vscode/syntaxes/sigil.tmLanguage.json" with { type: "json" };

interface Source {
  where: string;
  text: string;
  lang: StreamLanguage<unknown> | LanguageSupport | Language;
}

const docs = join(import.meta.dir, "../../..");

const sources: Source[] = [];
for (const file of markdown(docs)) {
  const text = readFileSync(file, "utf8");
  for (const m of text.matchAll(/^```sigil[^\n]*\n([\s\S]*?)^```/gm)) {
    const line = text.slice(0, m.index).split("\n").length;
    sources.push({ where: `${relative(docs, file)}:${line}`, text: m[1], lang: sigilSupport });
  }
}
// The presets as presets.ts loads them, from their directories; it can't be
// imported here, since import.meta.glob is Vite's.
const presets = join(import.meta.dir, "presets");
for (const file of walk(presets)) {
  const path = relative(presets, file);
  const lang = fileKind(path) === "sigil" ? sigilSupport : path.endsWith(".json") ? json() : yaml();
  sources.push({ where: `preset ${path}`, text: readFileSync(file, "utf8"), lang });
}

const problems: string[] = [];
for (const s of sources) {
  const state = EditorState.create({ doc: s.text, extensions: [s.lang] });
  const tree = ensureSyntaxTree(state, state.doc.length, 5_000);
  if (tree === null) {
    problems.push(`${s.where}: didn't parse in time`);
    continue;
  }
  tree.iterate({
    enter(node) {
      const style = getStyleTags(node);
      if (style === null || highlighter.style(style.tags) !== null) return;
      const token = state.doc.sliceString(node.from, node.to);
      const line = state.doc.lineAt(node.from).number;
      problems.push(`${s.where}, line ${line}: ${JSON.stringify(token)} (${node.name}) has tags the highlighter gives no class`);
    },
  });
}

if (problems.length > 0) {
  console.error(problems.join("\n"));
  console.error(`\n${problems.length} tokens would take the editor's plain colour; map their tags in language.ts.`);
  process.exit(1);
}
console.log(`Every tagged token has a class, in ${sources.length} sources.`);

// The colours: Shiki highlights each Sigil source with the grammar and
// vitesse-light, as the docs' fences are, and every character that isn't
// whitespace must get the same colour from the playground's class, read from
// playground.css. A difference means language.ts no longer mirrors the
// grammar.
const shiki = await createHighlighter({ themes: ["vitesse-light"], langs: [sigilGrammar as any] });
const palette = lightColours(readFileSync(join(import.meta.dir, "playground.css"), "utf8"));
const mismatches: string[] = [];
let compared = 0;
for (const s of sources) {
  if (s.lang !== sigilSupport) continue;
  compared++;
  const state = EditorState.create({ doc: s.text, extensions: [s.lang] });
  const tree = ensureSyntaxTree(state, state.doc.length, 5_000);
  if (tree === null) continue;
  const ours: string[] = Array.from(s.text, () => palette.plain);
  highlightTree(tree, highlighter, (from, to, classes) => {
    const colour = palette[classes.replace(/^sg-/, "")] ?? palette.plain;
    for (let i = from; i < to; i++) ours[i] = colour;
  });
  const theirs: string[] = Array.from(s.text, () => palette.plain);
  for (const line of shiki.codeToTokens(s.text, { lang: "sigil", theme: "vitesse-light" }).tokens) {
    for (const t of line) {
      for (let i = 0; i < t.content.length; i++) theirs[t.offset + i] = (t.color ?? palette.plain).toLowerCase();
    }
  }
  for (let i = 0; i < s.text.length; i++) {
    if (/\s/.test(s.text[i] ?? "") || ours[i] === theirs[i]) continue;
    const line = state.doc.lineAt(i);
    mismatches.push(
      `${s.where}, line ${line.number}, column ${i - line.from + 1}: ${JSON.stringify(line.text.trim())} is ${ours[i]} in the playground, ${theirs[i]} in the docs`,
    );
    break;
  }
}
if (mismatches.length > 0) {
  console.error(mismatches.join("\n"));
  console.error(`\n${mismatches.length} Sigil sources are coloured differently from the docs' fences; mirror the grammar in language.ts.`);
  process.exit(1);
}
console.log(`The playground colours ${compared} Sigil sources as the docs do.`);

/** The light theme's --sg-* colours, by class name without the sg- prefix, lower case. */
function lightColours(css: string): Record<string, string> {
  const block = /\.pg \{([^}]*--sg-plain[^}]*)\}/.exec(css)?.[1] ?? "";
  const out: Record<string, string> = {};
  for (const m of block.matchAll(/--sg-([a-z]+):\s*(#[0-9a-fA-F]+);/g)) out[m[1] as string] = (m[2] as string).toLowerCase();
  if (out.plain === undefined) throw new Error("playground.css has no --sg-plain in its .pg block");
  return out as Record<string, string> & { plain: string };
}

function* markdown(dir: string): Generator<string> {
  for (const path of walk(dir)) if (path.endsWith(".md")) yield path;
}

function* walk(dir: string): Generator<string> {
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const path = join(dir, name);
    if (statSync(path).isDirectory()) yield* walk(path);
    else yield path;
  }
}
