// Syntax highlighting for the playground's editors. The Sigil tokenizer
// follows docs/.vuepress/sigil.tmLanguage.json, the grammar the docs'
// ```sigil fences use, scope for scope, and the classes it emits are
// coloured like the site's Shiki themes (vitesse-light and vitesse-dark)
// colour those scopes; see the .sg-* rules in playground.css. YAML and
// JSON go through the same highlighter, so the input pane matches the docs'
// YAML and JSON fences too.

import { StreamLanguage, syntaxHighlighting, type StringStream } from "@codemirror/language";
import { Tag, tagHighlighter, tags } from "@lezer/highlight";

// One tag per colour the grammar's scopes end up in, under the token names
// the tokenizer returns. The names start with sg so that none is one of
// StreamLanguage's legacy token names, which win over the token table:
// "type" would come out as typeName and "builtin" as variableName.standard,
// tags the highlighter below gives no class.
const sigil = {
  sgKeyword: Tag.define(), // keyword.other, keyword.control, constant.language
  sgName: Tag.define(), // entity.name.namespace, entity.name.function
  sgType: Tag.define(), // entity.name.type
  sgDefinition: Tag.define(), // variable.other.definition
  sgBuiltin: Tag.define(), // support.type
  sgOperator: Tag.define(), // keyword.operator, keyword.operator.word
  sgSeparator: Tag.define(), // punctuation.separator.document
};


const KEYWORDS = new Set([
  "when", "assert", "policy", "module", "use", "as", "param", "let", "pub", "kind", "version", "enum",
  "type", "input", "fn", "decision", "precedence", "collect", "default", "conflict", "true", "false", "outcome",
]);
const WORD_OPERATORS = new Set([
  "and", "or", "xor", "not", "in", "all", "any", "filter", "one", "exclusive", "has", "like", "matches", "present",
]);
const BUILTIN_TYPES = new Set(["bool", "int", "float", "string", "duration", "timestamp", "list", "map"]);
// The keyword that names what the next identifier declares.
const DECLARES: Record<string, string> = {
  policy: "sgName", module: "sgName", use: "sgName",
  kind: "sgType", type: "sgType",
  param: "sgDefinition", let: "sgDefinition", input: "sgDefinition",
  fn: "sgName", decision: "sgName",
};

interface State {
  /** Inside a raw string, which may span lines. */
  raw: boolean;
  /** What the next identifier declares, right after a declaring keyword. */
  declares: string | null;
}

const sigilLanguage = StreamLanguage.define<State>({
  name: "sigil",
  startState: () => ({ raw: false, declares: null }),
  token(stream: StringStream, state: State): string | null {
    if (state.raw) {
      state.raw = !stream.skipTo("`");
      if (!state.raw) stream.next();
      else stream.skipToEnd();
      return "string";
    }
    if (stream.sol() && stream.match(/^\s*---\s*$/)) return "sgSeparator";
    if (stream.eatSpace()) return null;
    if (stream.match("//")) {
      stream.skipToEnd();
      return "comment";
    }
    if (stream.peek() === '"') {
      stream.next();
      let escaped = false;
      for (let ch = stream.next(); ch !== undefined; ch = stream.next()) {
        if (ch === '"' && !escaped) break;
        escaped = !escaped && ch === "\\";
      }
      return "string";
    }
    if (stream.eat("`")) {
      state.raw = !stream.skipTo("`");
      if (state.raw) stream.skipToEnd();
      else stream.next();
      return "string";
    }
    if (stream.match(/^(?:\d+(?:ms|s|m|h|d))+\b/) || stream.match(/^\d+(?:\.\d+)?\b/)) return "number";

    const declares = state.declares;
    state.declares = null;
    if (declares === "sgName" && stream.match(/^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*/)) return declares;
    if (declares !== null && stream.match(/^[A-Za-z_]\w*/)) return declares;

    const word = stream.match(/^[A-Za-z_]\w*/) as RegExpMatchArray | null;
    if (word) {
      const w = word[0];
      if (w in DECLARES && stream.match(/^\s+(?=[A-Za-z_])/, false)) state.declares = DECLARES[w];
      if (KEYWORDS.has(w)) return "sgKeyword";
      if (WORD_OPERATORS.has(w)) return "sgOperator";
      if (BUILTIN_TYPES.has(w)) return "sgBuiltin";
      if (stream.match(/^\s*(?=\()/, false)) return "sgName";
      return null;
    }
    if (stream.match(/^(?:\?\?|==|!=|<=|>=|->|[<>+\-?])/)) return "sgOperator";
    stream.next();
    return null;
  },
  tokenTable: sigil,
  languageData: { commentTokens: { line: "//" } },
});

/** Sigil source, highlighted like the docs' ```sigil fences. */
export const sigilSupport = sigilLanguage;

/** Maps the Sigil tags and the YAML and JSON parsers' tags to the .sg-* classes. */
export const highlighter = tagHighlighter([
  { tag: [sigil.sgKeyword, tags.bool, tags.keyword], class: "sg-keyword" },
  { tag: [sigil.sgName], class: "sg-namespace" },
  { tag: [sigil.sgType], class: "sg-type" },
  { tag: [sigil.sgDefinition], class: "sg-definition" },
  { tag: [sigil.sgBuiltin, tags.propertyName, tags.definition(tags.propertyName)], class: "sg-builtin" },
  { tag: [sigil.sgOperator, tags.null], class: "sg-operator" },
  { tag: [sigil.sgSeparator, tags.punctuation, tags.separator, tags.squareBracket, tags.brace, tags.meta], class: "sg-punctuation" },
  { tag: [tags.comment, tags.lineComment], class: "sg-comment" },
  { tag: [tags.string, tags.special(tags.string), tags.content, tags.attributeValue], class: "sg-string" },
  { tag: [tags.number, tags.integer, tags.float], class: "sg-number" },
]);

export const highlighting = syntaxHighlighting(highlighter);
