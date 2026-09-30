// Package token defines the lexical tokens of the Sigil language and the
// source positions that tokens, AST nodes and diagnostics carry.
//
// It sits under every other stage of the compiler and imports nothing from
// the repository. The lexer turns source into a stream of [Token] values, the
// parser consumes them, and every later stage reports problems at a [Pos]
// taken from a token or a node.
//
// The token kinds follow the lexical grammar at
// https://sigil.specht-labs.de/reference/lexical/.
// Keywords get a [Kind] each, because the parser picks statement and
// expression parsers by looking at one token, and a switch on a kind is both
// faster and easier to read than a string comparison. [Lookup] maps an
// identifier's spelling to its keyword kind.
package token

import (
	"fmt"
	"strconv"
)

// Kind classifies a token. The zero Kind is [Illegal].
type Kind uint8

// The token kinds. The unexported markers delimit the classes that
// [Kind.IsLiteral], [Kind.IsOperator] and [Kind.IsKeyword] report. The
// comment after a literal kind is an example; after an operator it is the
// spelling. Each keyword kind KwX is the reserved word x, in lower case.
const (
	// Illegal is a token the lexer could not classify. The lexer records an
	// error for every Illegal token it emits; the token itself carries the
	// offending source text so the parser can resynchronize.
	Illegal Kind = iota
	// EOF marks the end of the source. Once the source is exhausted, the
	// lexer's Next method keeps returning it.
	EOF

	// Comment is a line comment, including the leading `//`. The lexer emits
	// comments so that a formatter can keep them; the parser skips them.
	Comment

	literalBeg
	Ident     // release
	Int       // 42
	Float     // 0.5
	Duration  // 1h30m
	String    // "deployer"
	RawString // `^team-[a-z]+$`
	literalEnd

	operatorBeg
	Eq        // ==
	NotEq     // !=
	Lt        // <
	LtEq      // <=
	Gt        // >
	GtEq      // >=
	Plus      // +
	Minus     // -
	Coalesce  // ??, the fallback for an absent optional
	Question  // ?, which makes a type optional: ?T
	OptDot    // ?., a field access through an optional
	At        // @, before the kind version a header pins
	Dot       // .
	Comma     // ,
	Colon     // :
	Assign    // =
	Arrow     // ->, before a host function's result type
	Pipe      // |, between the values of an enum or the reasons of a decision
	LParen    // (
	RParen    // )
	LBracket  // [
	RBracket  // ]
	LBrace    // {
	RBrace    // }
	Separator // ---, between two documents in one file
	operatorEnd

	keywordBeg

	// Policy and module files.
	KwPolicy
	KwModule
	KwUse
	KwAs
	KwParam
	KwLet
	KwPub
	KwWhen
	KwAssert

	// Kind files.
	KwKind
	KwVersion
	KwType
	KwInput
	KwFn
	KwEnum
	KwDecision
	KwPrecedence
	KwCollect
	KwDefault
	KwConflict

	// Operators.
	KwAnd
	KwOr
	KwXor
	KwNot
	KwIn
	KwAll
	KwAny
	KwFilter
	KwOne
	KwExclusive
	KwHas
	KwLike
	KwMatches
	KwPresent

	// Values.
	KwTrue
	KwFalse
	KwOutcome
	keywordEnd
)

// names holds the spelling of every operator and keyword, and a description
// for the kinds that have no single spelling. String uses it for messages like
// "expected `{`, found `let`".
var names = [...]string{
	Illegal: "illegal token",
	EOF:     "end of file",
	Comment: "comment",

	Ident:     "identifier",
	Int:       "integer",
	Float:     "float",
	Duration:  "duration",
	String:    "string",
	RawString: "raw string",

	Eq:        "==",
	NotEq:     "!=",
	Lt:        "<",
	LtEq:      "<=",
	Gt:        ">",
	GtEq:      ">=",
	Plus:      "+",
	Minus:     "-",
	Coalesce:  "??",
	Question:  "?",
	OptDot:    "?.",
	At:        "@",
	Dot:       ".",
	Comma:     ",",
	Colon:     ":",
	Assign:    "=",
	Arrow:     "->",
	Pipe:      "|",
	LParen:    "(",
	RParen:    ")",
	LBracket:  "[",
	RBracket:  "]",
	LBrace:    "{",
	RBrace:    "}",
	Separator: "---",

	KwPolicy:     "policy",
	KwModule:     "module",
	KwUse:        "use",
	KwAs:         "as",
	KwParam:      "param",
	KwLet:        "let",
	KwPub:        "pub",
	KwWhen:       "when",
	KwAssert:     "assert",
	KwKind:       "kind",
	KwVersion:    "version",
	KwType:       "type",
	KwInput:      "input",
	KwFn:         "fn",
	KwEnum:       "enum",
	KwDecision:   "decision",
	KwPrecedence: "precedence",
	KwCollect:    "collect",
	KwDefault:    "default",
	KwConflict:   "conflict",
	KwAnd:        "and",
	KwOr:         "or",
	KwXor:        "xor",
	KwNot:        "not",
	KwIn:         "in",
	KwAll:        "all",
	KwAny:        "any",
	KwFilter:     "filter",
	KwOne:        "one",
	KwExclusive:  "exclusive",
	KwHas:        "has",
	KwLike:       "like",
	KwMatches:    "matches",
	KwPresent:    "present",
	KwTrue:       "true",
	KwFalse:      "false",
	KwOutcome:    "outcome",
}

// keywords maps each keyword's spelling to its kind. It's derived from names
// so the two can't drift apart.
var keywords = func() map[string]Kind {
	m := make(map[string]Kind, keywordEnd-keywordBeg)
	for k := keywordBeg + 1; k < keywordEnd; k++ {
		m[names[k]] = k
	}
	return m
}()

// Lookup returns the keyword kind for ident, or Ident when it isn't a
// keyword. Keywords are case-sensitive: `When` is an identifier.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return Ident
}

// String returns the spelling of an operator or keyword, and a description
// such as "identifier" or "end of file" for the other kinds. A value outside
// the declared kinds formats as "kind(N)".
func (k Kind) String() string {
	if int(k) < len(names) && names[k] != "" {
		return names[k]
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// IsLiteral reports whether k is an identifier or a literal.
func (k Kind) IsLiteral() bool { return literalBeg < k && k < literalEnd }

// IsOperator reports whether k is an operator or punctuation.
func (k Kind) IsOperator() bool { return operatorBeg < k && k < operatorEnd }

// IsKeyword reports whether k is a reserved word.
func (k Kind) IsKeyword() bool { return keywordBeg < k && k < keywordEnd }

// Pos is a position in a source file. Offset is what code uses to slice the
// source; Line and Column are what people read in diagnostics.
//
// Positions compare by Offset within one file. They carry no file name; a
// diagnostic pairs them with one.
type Pos struct {
	Offset int // byte offset from the start of the source, starting at 0
	Line   int // line number, starting at 1
	Column int // column in characters, not bytes, starting at 1; a tab is one column
}

// IsValid reports whether p was set. The zero Pos is not a position.
func (p Pos) IsValid() bool { return p.Line > 0 }

// String formats p as line:column, or as "-" when p is not valid.
func (p Pos) String() string {
	if !p.IsValid() {
		return "-"
	}
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

// Token is one lexical token with its source text and span. End is the
// position just after the token's last character, so End.Offset-Pos.Offset
// is the token's length in bytes, and a multi-line raw string has End on a
// later line than Pos.
type Token struct {
	Text string // the source text exactly as written, quotes included; empty for EOF
	Pos  Pos    // the token's first character
	End  Pos    // just after the token's last character
	Kind Kind
}

// String formats t for test output and debugging: the position and kind,
// followed by the quoted text for identifiers, literals, comments and
// Illegal tokens.
func (t Token) String() string {
	switch {
	case t.Kind == EOF:
		return fmt.Sprintf("%s %s", t.Pos, t.Kind)
	case t.Kind.IsKeyword() || t.Kind.IsOperator():
		return fmt.Sprintf("%s %s", t.Pos, t.Kind)
	default:
		return fmt.Sprintf("%s %s %q", t.Pos, t.Kind, t.Text)
	}
}
