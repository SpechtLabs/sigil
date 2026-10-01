// Checks that the playground's editors colour every token they tag: each
// ```sigil fence in the docs, and each file, input and stubs of the
// presets, is parsed as the editors parse it, and a token whose tags the
// highlighter gives no class fails the check. A token without a class takes
// the editor's plain colour, so a tag that slips through (as StreamLanguage's
// legacy token names did) goes unnoticed in light mode and unreadable in
// dark. `bun run check:playground` runs it, and so does the docs build.

import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { ensureSyntaxTree, type Language, type LanguageSupport, type StreamLanguage } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { getStyleTags } from "@lezer/highlight";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { looksLikeJson } from "./editor.ts";
import { highlighter, sigilSupport } from "./language.ts";
import { presets } from "./presets.ts";

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
for (const p of presets) {
  const ws = p.workspace;
  for (const f of ws.files) sources.push({ where: `preset ${p.id}: ${f.path}`, text: f.source, lang: sigilSupport });
  sources.push({ where: `preset ${p.id}: input`, text: ws.input, lang: looksLikeJson(ws.input) ? json() : yaml() });
  if (ws.stubs) sources.push({ where: `preset ${p.id}: stubs`, text: ws.stubs, lang: yaml() });
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

function* markdown(dir: string): Generator<string> {
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const path = join(dir, name);
    if (statSync(path).isDirectory()) yield* markdown(path);
    else if (name.endsWith(".md")) yield path;
  }
}
