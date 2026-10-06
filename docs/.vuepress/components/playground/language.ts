// Syntax highlighting for the playground's editors. The Sigil tokenizer
// mirrors editors/vscode/syntaxes/sigil.tmLanguage.json, the grammar the
// docs' ```sigil fences use, rule for rule: the same regexes, tried in the
// same order, inside the same regions (type and decision bodies, enum and
// reason lists, typed declarations, calls). The classes it emits are
// coloured the way the site's Shiki themes (vitesse-light and vitesse-dark)
// colour those scopes; see the .sg-* rules in playground.css, and
// check-highlighting.ts, which compares the two on every fence and preset.
// YAML and JSON go through the same highlighter, so the input pane matches
// the docs' YAML and JSON fences too.

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
  sgDefinition: Tag.define(), // variable.other.definition, variable.other.property, variable.parameter
  sgBuiltin: Tag.define(), // support.type
  sgOperator: Tag.define(), // keyword.operator, keyword.operator.word
  sgPunctuation: Tag.define(), // punctuation.*, keyword.operator.assignment
  sgConstant: Tag.define(), // constant.other.enum, constant.character.escape
  sgInvalid: Tag.define(), // invalid.illegal
  sgQuote: Tag.define(), // punctuation.definition.string
};

type Style = keyof typeof sigil | "string" | "comment" | "number" | null;

const ID = "[A-Za-z_][A-Za-z0-9_]*";
const NAME = `${ID}(?:\\.${ID})*`;
const KEYWORDS =
  "policy|module|use|as|param|let|pub|when|assert|kind|version|enum|type|input|fn|decision|precedence|collect|default|conflict";
const WORD_OPERATORS = "and|or|xor|not|in|all|any|filter|one|exclusive|has|like|matches|present";

// The grammar's patterns, anchored at the stream's position. Each capture
// group gets the style at the same index of its row; text between the
// groups gets none.
const KIND_HEADER = re(`(kind)\\s+(${ID})(?:\\s+(version)\\s+(\\d+)(?:\\s*(,)\\s*(accepts)\\s*(:)\\s*(\\d+))?)?`);
const DOCUMENT_HEADER = re(`(policy|module)\\s+(${NAME})(?:\\s*(:)\\s*(${ID})(?:\\s*(@)\\s*(\\d+))?)?`);
const USE_SELECTIVE = re(`(use)\\s+(${NAME})(\\.)(\\{)`);
const USE = re(`(use)\\s+(${NAME})(?:\\s+(as)\\s+(${ID}))?`);
const ENUM_DECLARATION = re(`(enum)\\s+(${ID})\\s*(:)`);
const TYPE_DECLARATION = re(`(type)\\s+(${ID})\\s*(\\{)`);
const DECISION_DECLARATION = re(`(decision)\\s+(${ID})\\s*(\\{)`);
const FN_DECLARATION = re(`(fn)\\s+(${ID})`);
const TYPED_DECLARATION = re(`(input|param)\\s+(${ID})\\s*(:)`);
const DECLARATIONS: [RegExp, Style[]][] = [
  [re(`(let)\\s+(${ID})`), ["sgKeyword", "sgDefinition"]],
  [re(`(input|param)\\s+(${ID})`), ["sgKeyword", "sgDefinition"]],
  [re(`(type|enum)\\s+(${ID})`), ["sgKeyword", "sgType"]],
  [re(`(decision)\\s+(${ID})`), ["sgKeyword", "sgName"]],
];
const COLLECT = re(`(collect)\\s+(one|all)\\b`);
const EXCLUSIVE = re(`(exclusive)\\b(?!\\s+in\\b)`);
const PARAM_BOUND = re(`(,)\\s*\\b(min|max)\\s*(:)`);
const FIELD_LABEL = re(`(${ID})\\s*(:)`);
const REASON_FIELD = re(`(reason)\\s*(:)`);
const MEMBER = re(`(\\?\\.|\\.)\\s*(${ID})`);
const CALL = re(`(?!(?:${KEYWORDS}|${WORD_OPERATORS}|true|false|outcome)\\b)(${ID})\\s*(\\()`);
const NAMED_ARGUMENT = re(`(${ID})\\s*(:)`);

const DURATION = /^(?=\d)(?:\d+d)?(?:\d+h)?(?:\d+m(?!s))?(?:\d+s)?(?:\d+ms)?(?<=[dhms])\b/;
const FLOAT = /^\d+\.\d+\b/;
const INTEGER = /^\d+\b(?!\.\d)/;
const INVALID_NUMBER = /^\d[A-Za-z0-9_]*(?:\.\d[A-Za-z0-9_]*)?/;
const ESCAPE = /^\\(?:[abfnrtv\\"]|x[0-9A-Fa-f]{2}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8}|[0-7]{3})/;
const KEYWORD = new RegExp(`^(?:${KEYWORDS})\\b`);
const WORD_OPERATOR = new RegExp(`^(?:${WORD_OPERATORS})\\b`);
const CONSTANT = /^(?:true|false|outcome)\b/;
const BUILTIN = /^(?:bool|int|float|string|duration|timestamp|list|map)\b/;
const IDENTIFIER = new RegExp(`^${ID}\\b`);

/** A region the grammar's begin/end rules open, innermost last. */
type Region =
  | "type" // type T { ... }
  | "decision" // decision d { ... }
  | "reason" // reason: a | b, in a decision body
  | "enum" // enum T: a | b
  | "payload-field" // a decision field's type
  | "payload-default" // a decision field's = default
  | "fn" // fn f(types) -> type
  | "typed" // input and param types
  | "use-braces" // use m.{a, b as c}
  | "call" // f(...)
  | "group" // (...)
  | "string" // "..."
  | "raw"; // `...`

/** Regions that end at the end of their line. */
const ENDS_AT_EOL: ReadonlySet<Region> = new Set(["payload-field", "payload-default", "fn", "typed", "string"]);

interface State {
  regions: Region[];
  /** Tokens a multi-capture match still has to emit: [length, style]. */
  queue: [number, Style][];
  /** Nothing but whitespace before this position on the line. */
  lineStart: boolean;
  /** Right after a call's ( or , or at the start of a line: where a named argument may start. */
  argStart: boolean;
}

const sigilLanguage = StreamLanguage.define<State>({
  name: "sigil",
  startState: () => ({ regions: [], queue: [], lineStart: true, argStart: true }),
  copyState: (s) => ({ regions: [...s.regions], queue: [...s.queue], lineStart: s.lineStart, argStart: s.argStart }),
  blankLine: (state) => startLine(state, null),
  token(stream: StringStream, state: State): Style {
    if (state.queue.length === 0 && stream.sol()) startLine(state, stream);
    const style = state.queue.length > 0 ? drain(stream, state) : next(stream, state);
    // Whitespace keeps both flags; a call's ( or , is where a named argument may start.
    if (!/^\s*$/.test(stream.current())) {
      state.lineStart = false;
      state.argStart = state.regions.at(-1) === "call" && /^[(,]$/.test(stream.current());
    }
    return style;
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
  { tag: [sigil.sgPunctuation, tags.punctuation, tags.separator, tags.squareBracket, tags.brace, tags.meta], class: "sg-punctuation" },
  { tag: [sigil.sgConstant], class: "sg-constant" },
  { tag: [sigil.sgInvalid], class: "sg-invalid" },
  { tag: [sigil.sgQuote], class: "sg-quote" },
  { tag: [tags.comment, tags.lineComment], class: "sg-comment" },
  { tag: [tags.string, tags.special(tags.string), tags.content, tags.attributeValue], class: "sg-string" },
  { tag: [tags.number, tags.integer, tags.float], class: "sg-number" },
]);

export const highlighting = syntaxHighlighting(highlighter);

function re(source: string): RegExp {
  return new RegExp(`^${source}`, "d");
}

/** Ends the regions the previous line closed: those that end with their line, and lists not continued with |. */
function startLine(state: State, stream: StringStream | null): void {
  while (state.regions.length > 0 && ENDS_AT_EOL.has(state.regions.at(-1) as Region)) state.regions.pop();
  const top = state.regions.at(-1);
  if ((top === "enum" || top === "reason") && !(stream?.match(/^\s*\|/, false) ?? false)) state.regions.pop();
  state.lineStart = true;
  state.argStart = true;
}

function drain(stream: StringStream, state: State): Style {
  const [length, style] = state.queue.shift() as [number, Style];
  stream.pos += length;
  return style;
}

/** Matches a multi-capture pattern and queues a token per group, and the text between them. */
function captures(stream: StringStream, state: State, pattern: RegExp, styles: Style[], region?: Region): boolean {
  const m = stream.match(pattern, false) as RegExpMatchArray | null;
  if (m === null || m.indices === undefined) return false;
  let at = 0;
  const parts: [number, Style][] = [];
  m.indices.slice(1).forEach((span, i) => {
    if (span === undefined) return;
    if (span[0] > at) parts.push([span[0] - at, null]);
    parts.push([span[1] - span[0], styles[i] ?? null]);
    at = span[1];
  });
  if (m[0].length > at) parts.push([m[0].length - at, null]);
  state.queue.push(...parts.filter(([length]) => length > 0));
  if (region !== undefined) state.regions.push(region);
  return true;
}

/** One token: the innermost region's end, else its patterns, else plain text. */
function next(stream: StringStream, state: State): Style {
  for (;;) {
    const top = state.regions.at(-1);
    if (top === "string") return inString(stream, state);
    if (top === "raw") return inRaw(stream, state);
    if (stream.eatSpace()) return null;

    // Ends that match nothing, then ends that consume a token.
    const ahead = stream.string.slice(stream.pos);
    if (
      (top === "reason" && ahead.startsWith("}")) ||
      (top === "payload-field" && /^(?:\}|=|\/\/)/.test(ahead)) ||
      (top === "payload-default" && ahead.startsWith("}")) ||
      (top === "typed" && /^(?:=(?!=)|,|\/\/)/.test(ahead)) ||
      (top === "fn" && ahead.startsWith("//"))
    ) {
      state.regions.pop();
      continue;
    }
    if ((top === "type" || top === "decision" || top === "use-braces") && stream.eat("}")) {
      state.regions.pop();
      return "sgPunctuation";
    }
    if ((top === "call" || top === "group") && stream.eat(")")) {
      state.regions.pop();
      return "sgPunctuation";
    }

    const style = patterns(stream, state, top);
    if (style !== undefined) return style;
    if (!stream.match(/^[A-Za-z0-9_]+/)) stream.next();
    return null;
  }
}

/** The patterns of a region, or of the top level; undefined when none matches. */
function patterns(stream: StringStream, state: State, top: Region | undefined): Style | undefined {
  switch (top) {
    case undefined:
      return topLevel(stream, state);
    case "type":
      if (comment(stream)) return "comment";
      if (captures(stream, state, FIELD_LABEL, ["sgDefinition", "sgPunctuation"])) return drain(stream, state);
      return typeExpression(stream);
    case "decision":
      if (comment(stream)) return "comment";
      if (captures(stream, state, REASON_FIELD, ["sgKeyword", "sgPunctuation"], "reason")) return drain(stream, state);
      if (captures(stream, state, FIELD_LABEL, ["sgDefinition", "sgPunctuation"], "payload-field")) {
        return drain(stream, state);
      }
      if (stream.eat("=")) {
        state.regions.push("payload-default");
        return "sgPunctuation";
      }
      return undefined;
    case "reason":
    case "enum":
      if (comment(stream)) return "comment";
      if (stream.eat("|")) return "sgPunctuation";
      if (stream.match(IDENTIFIER)) return "sgConstant";
      return undefined;
    case "payload-field":
    case "typed":
      return typeExpression(stream);
    case "payload-default":
    case "group":
      return expression(stream, state);
    case "fn":
      if (stream.match("->")) return "sgOperator";
      if (stream.match(/^[()]/)) return "sgPunctuation";
      return typeExpression(stream);
    case "use-braces":
      if (comment(stream)) return "comment";
      if (stream.match(/^as\b/)) return "sgKeyword";
      if (stream.eat(",")) return "sgPunctuation";
      return undefined;
    case "call":
      if (state.argStart && captures(stream, state, NAMED_ARGUMENT, ["sgDefinition", "sgPunctuation"])) {
        return drain(stream, state);
      }
      return expression(stream, state);
    case "string":
    case "raw":
      return undefined;
  }
}

function topLevel(stream: StringStream, state: State): Style | undefined {
  if (comment(stream)) return "comment";
  if (state.lineStart && stream.match(/^---\s*$/)) return "sgPunctuation";
  const rules: [RegExp, Style[], Region?][] = [
    [KIND_HEADER, ["sgKeyword", "sgType", "sgKeyword", "number", "sgPunctuation", "sgKeyword", "sgPunctuation", "number"]],
    [DOCUMENT_HEADER, ["sgKeyword", "sgName", "sgPunctuation", "sgType", "sgPunctuation", "number"]],
    [USE_SELECTIVE, ["sgKeyword", "sgName", "sgPunctuation", "sgPunctuation"], "use-braces"],
    [USE, ["sgKeyword", "sgName", "sgKeyword", "sgName"]],
    [ENUM_DECLARATION, ["sgKeyword", "sgType", "sgPunctuation"], "enum"],
    [TYPE_DECLARATION, ["sgKeyword", "sgType", "sgPunctuation"], "type"],
    [DECISION_DECLARATION, ["sgKeyword", "sgName", "sgPunctuation"], "decision"],
    [FN_DECLARATION, ["sgKeyword", "sgName"], "fn"],
    [TYPED_DECLARATION, ["sgKeyword", "sgDefinition", "sgPunctuation"], "typed"],
    ...DECLARATIONS,
    [COLLECT, ["sgKeyword", "sgKeyword"]],
  ];
  for (const [pattern, styles, region] of rules) {
    if (captures(stream, state, pattern, styles, region)) return drain(stream, state);
  }
  if (state.lineStart && captures(stream, state, EXCLUSIVE, ["sgKeyword"])) return drain(stream, state);
  if (captures(stream, state, PARAM_BOUND, ["sgPunctuation", "sgKeyword", "sgPunctuation"])) return drain(stream, state);
  return expression(stream, state);
}

function expression(stream: StringStream, state: State): Style | undefined {
  if (comment(stream)) return "comment";
  if (stream.eat('"')) {
    state.regions.push("string");
    return "sgQuote";
  }
  if (stream.eat("`")) {
    state.regions.push("raw");
    return "sgQuote";
  }
  if (stream.match(DURATION) || stream.match(FLOAT) || stream.match(INTEGER)) return "number";
  if (stream.match(INVALID_NUMBER)) return "sgInvalid";
  if (captures(stream, state, MEMBER, ["sgPunctuation", "sgDefinition"])) return drain(stream, state);
  if (stream.match(KEYWORD)) return "sgKeyword";
  if (stream.match(WORD_OPERATOR)) return "sgOperator";
  if (stream.match(CONSTANT)) return "sgKeyword";
  if (stream.match(BUILTIN)) return "sgBuiltin";
  if (captures(stream, state, CALL, ["sgName", "sgPunctuation"], "call")) return drain(stream, state);
  if (stream.eat("(")) {
    state.regions.push("group");
    return "sgPunctuation";
  }
  if (stream.match(/^(?:\?\?|==|!=|<=|>=|->|[<>+-])/)) return "sgOperator";
  if (stream.eat("?")) return "sgOperator";
  if (stream.match(/^[=|@,:]/)) return "sgPunctuation";
  return undefined;
}

function typeExpression(stream: StringStream): Style | undefined {
  if (comment(stream)) return "comment";
  if (stream.eat("?")) return "sgOperator";
  if (stream.match(/^[<>,]/)) return "sgPunctuation";
  if (stream.match(BUILTIN)) return "sgBuiltin";
  if (stream.match(IDENTIFIER)) return "sgType";
  return undefined;
}

function comment(stream: StringStream): boolean {
  if (!stream.match("//")) return false;
  stream.skipToEnd();
  return true;
}

/** Inside "...": escapes, the closing quote, or a run of text. The line's end closes it too. */
function inString(stream: StringStream, state: State): Style {
  if (stream.eat('"')) {
    state.regions.pop();
    return "sgQuote";
  }
  if (stream.match(ESCAPE)) return "sgConstant";
  if (stream.match(/^\\./)) return "sgInvalid";
  if (!stream.match(/^[^"\\]+/)) stream.next();
  return "string";
}

/** Inside `...`, which may span lines: the closing backtick, or the text up to it. */
function inRaw(stream: StringStream, state: State): Style {
  if (stream.eat("`")) {
    state.regions.pop();
    return "sgQuote";
  }
  if (!stream.match(/^[^`]+/)) stream.next();
  return "string";
}
