// The CodeMirror setup the playground's editors share, and the marks it
// puts on files: diagnostics from check and the rules that fired in the last
// run on Sigil files, and each case's result on test files.

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
  WidgetType,
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

/** A test case's result, at the line its `- name:` is on. */
export interface CaseMark {
  line: number;
  passed: boolean;
  name: string;
  /** How it failed: the failures, or why it couldn't run. */
  messages: string[];
}

/** What the last test run says about one test file. */
export interface TestMarks {
  cases: CaseMark[];
  /** Why none of the file's cases ran. */
  error?: string;
}

const setCases = StateEffect.define<TestMarks>();

class CaseMarker extends GutterMarker {
  constructor(readonly mark: CaseMark) {
    super();
  }

  override eq(other: CaseMarker): boolean {
    return other.mark.passed === this.mark.passed && other.mark.name === this.mark.name;
  }

  override toDOM(): Node {
    const el = document.createElement("span");
    el.className = this.mark.passed ? "pg-case-mark pg-case-mark--pass" : "pg-case-mark pg-case-mark--fail";
    el.textContent = this.mark.passed ? "✓" : "✗";
    el.title = `${this.mark.name}: ${this.mark.passed ? "passed" : "failed"}`;
    return el;
  }
}

// How a failed case, or a file that couldn't run, reads in the editor: a
// note under the case, or above the file's first line.
class NoteWidget extends WidgetType {
  constructor(
    readonly title: string,
    readonly messages: string[],
  ) {
    super();
  }

  override eq(other: NoteWidget): boolean {
    return other.title === this.title && other.messages.join("\n") === this.messages.join("\n");
  }

  override toDOM(): HTMLElement {
    const el = document.createElement("div");
    el.className = "pg-case-note";
    const title = el.appendChild(document.createElement("div"));
    title.className = "pg-case-note__title";
    title.textContent = this.title;
    for (const m of this.messages) {
      const line = el.appendChild(document.createElement("div"));
      line.className = "pg-case-note__message";
      line.textContent = m;
    }
    return el;
  }
}

interface Cases {
  decorations: DecorationSet;
  markers: RangeSet<GutterMarker>;
}

const casesField = StateField.define<Cases>({
  create: () => ({ decorations: Decoration.none, markers: RangeSet.empty }),
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setCases)) return caseMarks(tr.state.doc, e.value);
    if (!tr.docChanged) return value;
    return { decorations: value.decorations.map(tr.changes), markers: value.markers.map(tr.changes) };
  },
  provide: (f) => EditorView.decorations.from(f, (v) => v.decorations),
});

function caseMarks(doc: Text, marks: TestMarks): Cases {
  const decorations = [];
  const markers = [];
  if (marks.error !== undefined) {
    const widget = new NoteWidget("This file's cases can't run", [marks.error]);
    decorations.push(Decoration.widget({ widget, block: true, side: -1 }).range(0));
  }
  for (const c of [...marks.cases].sort((a, b) => a.line - b.line)) {
    if (c.line < 1 || c.line > doc.lines) continue;
    const from = doc.line(c.line).from;
    markers.push(new CaseMarker(c).range(from));
    if (c.passed) continue;
    decorations.push(Decoration.line({ class: "pg-case-failed" }).range(from));
    const widget = new NoteWidget(c.messages.length > 1 ? `${c.messages.length} differences` : "Failed", c.messages);
    decorations.push(Decoration.widget({ widget, block: true, side: 1 }).range(doc.line(caseEnd(doc, c.line)).to));
  }
  return { decorations: Decoration.set(decorations, true), markers: RangeSet.of(markers, true) };
}

// The last line of the case that starts on a line: the lines after it that
// are indented deeper than its `-`, without trailing blanks and comments.
function caseEnd(doc: Text, start: number): number {
  const indent = (text: string) => text.length - text.trimStart().length;
  const dash = indent(doc.line(start).text);
  let end = start;
  for (let n = start + 1; n <= doc.lines; n++) {
    const text = doc.line(n).text;
    const trimmed = text.trim();
    if (trimmed === "" || trimmed.startsWith("#")) continue;
    if (indent(text) <= dash) break;
    end = n;
  }
  return end;
}

const casesGutter = gutter({
  class: "pg-case-gutter",
  markers: (view) => view.state.field(casesField).markers,
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

/** The state of a test file's editor. */
export function testState(source: string, extra: Extension): EditorState {
  return EditorState.create({ doc: source, extensions: [base, yaml(), casesField, casesGutter, extra] });
}

/** The state of a data file's editor, or of the input or stubs editor. */
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

/** The transaction that replaces a test file's case marks. */
export function casesSpec(marks: TestMarks): TransactionSpec {
  return { effects: setCases.of(marks) };
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
