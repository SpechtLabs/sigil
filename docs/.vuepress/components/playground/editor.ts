// The CodeMirror setup the playground's editors share, and the two kinds of
// marks it puts on a file: diagnostics from check, and the rules that fired
// in the last run.

import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { bracketMatching, indentOnInput } from "@codemirror/language";
import { type Diagnostic as LintDiagnostic, lintGutter, setDiagnostics } from "@codemirror/lint";
import { EditorState, type Extension, RangeSet, StateEffect, StateField, type Text, type TransactionSpec } from "@codemirror/state";
import {
  Decoration,
  type DecorationSet,
  drawSelection,
  EditorView,
  GutterMarker,
  gutter,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import type { Diagnostic } from "@spechtlabs/sigil/worker";
import { highlighting, sigilSupport } from "./language.js";

/** A line a rule that fired in the last run starts on. */
export interface FiredLine {
  line: number;
  /** The rule's candidate is in the outcome. */
  winner: boolean;
  /** For the marker's tooltip: `page(reason: sustained)`. */
  label: string;
}

const setFired = StateEffect.define<FiredLine[]>();

class FiredMarker extends GutterMarker {
  constructor(readonly fired: FiredLine) {
    super();
  }

  override eq(other: FiredMarker): boolean {
    return other.fired.winner === this.fired.winner && other.fired.label === this.fired.label;
  }

  override toDOM(): Node {
    const el = document.createElement("span");
    el.className = this.fired.winner ? "pg-fired-mark pg-fired-mark--win" : "pg-fired-mark";
    el.title = `${this.fired.label} ${this.fired.winner ? "fired and won" : "fired"}`;
    return el;
  }
}

interface Fired {
  lines: DecorationSet;
  markers: RangeSet<GutterMarker>;
}

// The fired lines move with edits, so a highlight stays on its rule until
// the next run replaces it.
const firedField = StateField.define<Fired>({
  create: () => ({ lines: Decoration.none, markers: RangeSet.empty }),
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setFired)) return firedMarks(tr.state.doc, e.value);
    if (!tr.docChanged) return value;
    return { lines: value.lines.map(tr.changes), markers: value.markers.map(tr.changes) };
  },
  provide: (f) => EditorView.decorations.from(f, (v) => v.lines),
});

function firedMarks(doc: Text, fired: FiredLine[]): Fired {
  const lines = [];
  const markers = [];
  for (const f of [...fired].sort((a, b) => a.line - b.line)) {
    if (f.line < 1 || f.line > doc.lines) continue;
    const from = doc.line(f.line).from;
    lines.push(Decoration.line({ class: f.winner ? "pg-fired pg-fired--win" : "pg-fired" }).range(from));
    markers.push(new FiredMarker(f).range(from));
  }
  return { lines: Decoration.set(lines, true), markers: RangeSet.of(markers, true) };
}

const firedGutter = gutter({
  class: "pg-fired-gutter",
  markers: (view) => view.state.field(firedField).markers,
});

const base: Extension = [
  lineNumbers(),
  highlightActiveLineGutter(),
  history(),
  drawSelection(),
  indentOnInput(),
  bracketMatching(),
  highlightActiveLine(),
  EditorView.lineWrapping,
  highlighting,
  EditorState.tabSize.of(2),
  // Tab indents; Escape then Tab leaves the editor, as CodeMirror documents.
  keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
];

/** The state of one Sigil file's editor. */
export function sigilState(source: string, extra: Extension): EditorState {
  return EditorState.create({ doc: source, extensions: [base, sigilSupport, lintGutter(), firedField, firedGutter, extra] });
}

/** The state of the input or stubs editor. */
export function documentState(source: string, lang: "yaml" | "json", extra: Extension): EditorState {
  return EditorState.create({ doc: source, extensions: [base, lang === "json" ? json() : yaml(), extra] });
}

/** Whether a document reads as JSON, for picking the input's highlighting. */
export function looksLikeJson(source: string): boolean {
  return /^\s*[{[]/.test(source);
}

/** The transaction that replaces a file's fired-rule marks. */
export function firedSpec(fired: FiredLine[]): TransactionSpec {
  return { effects: setFired.of(fired) };
}

/** The transaction that replaces a file's diagnostics with check's. */
export function diagnosticsSpec(state: EditorState, diagnostics: Diagnostic[]): TransactionSpec {
  return setDiagnostics(state, diagnostics.map((d) => toLint(state.doc, d)));
}

/** Moves the cursor to a line and column and scrolls them into view. */
export function reveal(view: EditorView, line: number, column = 1): void {
  const doc = view.state.doc;
  const l = doc.line(Math.min(Math.max(line, 1), doc.lines));
  const pos = Math.min(l.from + Math.max(column - 1, 0), l.to);
  view.dispatch({ selection: { anchor: pos }, effects: EditorView.scrollIntoView(pos, { y: "center" }) });
  view.focus();
}

// A diagnostic has a line and a column; it underlines the word there, or
// the whole line when it has no column.
function toLint(doc: Text, d: Diagnostic): LintDiagnostic {
  const line = doc.line(Math.min(Math.max(d.line ?? 1, 1), doc.lines));
  let from = d.column === undefined ? line.from : Math.min(line.from + d.column - 1, line.to);
  let to = line.to;
  if (d.column !== undefined) {
    const word = /^[\w.]+|^\S/.exec(line.text.slice(from - line.from));
    to = from + (word?.[0].length ?? 0);
  }
  if (from === to && from > line.from) from--;
  return {
    from,
    to,
    severity: d.severity === "error" ? "error" : "warning",
    message: d.help ? `${d.message}\nhelp: ${d.help}` : d.message,
    source: d.lint,
  };
}
