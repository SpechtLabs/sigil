package lexer

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// tok is an expected token. When text is empty the kind's spelling is used,
// so keywords and operators can be listed by kind alone.
type tok struct {
	kind token.Kind
	text string
}

func (e tok) String() string {
	text := e.text
	if text == "" {
		text = e.kind.String()
	}
	return e.kind.String() + " " + text
}

func TestLex(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []tok
	}{
		{name: "empty", src: ""},
		{name: "whitespace only", src: " \t\r\n\n  "},

		// Identifiers and keywords.
		{name: "identifier", src: "release", want: []tok{{token.Ident, "release"}}},
		{name: "identifier with digits and underscores", src: "_x1_", want: []tok{{token.Ident, "_x1_"}}},
		{name: "keyword", src: "when", want: []tok{{kind: token.KwWhen}}},
		{name: "keywords are case-sensitive", src: "When TRUE", want: []tok{{token.Ident, "When"}, {token.Ident, "TRUE"}}},
		{name: "keyword prefix is an identifier", src: "whenever inputs", want: []tok{{token.Ident, "whenever"}, {token.Ident, "inputs"}}},
		{name: "type names are identifiers", src: "duration: duration", want: []tok{{token.Ident, "duration"}, {kind: token.Colon}, {token.Ident, "duration"}}},
		{name: "keyword after dot is lexed as keyword", src: "service.type", want: []tok{{token.Ident, "service"}, {kind: token.Dot}, {kind: token.KwType}}},
		{name: "keyword as field declaration", src: "type Resource {\n  kind: string\n}", want: []tok{
			{kind: token.KwType},
			{token.Ident, "Resource"},
			{kind: token.LBrace},
			{kind: token.KwKind},
			{kind: token.Colon},
			{token.Ident, "string"},
			{kind: token.RBrace},
		}},
		{name: "policy name is idents and dots", src: "deploy.common.{cleared}", want: []tok{
			{token.Ident, "deploy"},
			{kind: token.Dot},
			{token.Ident, "common"},
			{kind: token.Dot},
			{kind: token.LBrace},
			{token.Ident, "cleared"},
			{kind: token.RBrace},
		}},

		// Integers.
		{name: "int", src: "42", want: []tok{{token.Int, "42"}}},
		{name: "zero", src: "0", want: []tok{{token.Int, "0"}}},
		{name: "leading zeros", src: "007", want: []tok{{token.Int, "007"}}},
		{name: "max int64", src: "9223372036854775807", want: []tok{{token.Int, "9223372036854775807"}}},
		{name: "negative is unary minus", src: "-3", want: []tok{{kind: token.Minus}, {token.Int, "3"}}},

		// Floats.
		{name: "float", src: "0.5", want: []tok{{token.Float, "0.5"}}},
		{name: "float with zero fraction", src: "3.0", want: []tok{{token.Float, "3.0"}}},
		{name: "leading point is a dot", src: ".5", want: []tok{{kind: token.Dot}, {token.Int, "5"}}},
		{name: "second point ends the float", src: "1.5.2", want: []tok{{token.Float, "1.5"}, {kind: token.Dot}, {token.Int, "2"}}},

		// Durations.
		{name: "duration", src: "30m", want: []tok{{token.Duration, "30m"}}},
		{name: "chained duration", src: "1h30m", want: []tok{{token.Duration, "1h30m"}}},
		{name: "days", src: "2d", want: []tok{{token.Duration, "2d"}}},
		{name: "milliseconds", src: "500ms", want: []tok{{token.Duration, "500ms"}}},
		{name: "every unit", src: "1d2h3m4s5ms", want: []tok{{token.Duration, "1d2h3m4s5ms"}}},
		{name: "ms beats m then s", src: "1ms", want: []tok{{token.Duration, "1ms"}}},
		{name: "duration then operator", src: "24h>=min_soak", want: []tok{{token.Duration, "24h"}, {kind: token.GtEq}, {token.Ident, "min_soak"}}},
		{name: "duration then identifier needs space", src: "1h x", want: []tok{{token.Duration, "1h"}, {token.Ident, "x"}}},

		// Strings.
		{name: "string", src: `"deployer"`, want: []tok{{token.String, `"deployer"`}}},
		{name: "empty string", src: `""`, want: []tok{{token.String, `""`}}},
		{name: "string keeps escapes in text", src: `"line one\nline two"`, want: []tok{{token.String, `"line one\nline two"`}}},
		{name: "escaped quote", src: `"say \"hi\""`, want: []tok{{token.String, `"say \"hi\""`}}},
		{name: "escaped backslash before quote", src: `"a\\"`, want: []tok{{token.String, `"a\\"`}}},
		{name: "unicode string", src: `"é 🚀"`, want: []tok{{token.String, `"é 🚀"`}}},
		{name: "hex and unicode escapes", src: `"\x41é\U0001F680\101"`, want: []tok{{token.String, `"\x41é\U0001F680\101"`}}},
		{name: "adjacent strings", src: `"a""b"`, want: []tok{{token.String, `"a"`}, {token.String, `"b"`}}},

		// Raw strings.
		{name: "raw string", src: "`^team-[a-z]+$`", want: []tok{{token.RawString, "`^team-[a-z]+$`"}}},
		{name: "raw string has no escapes", src: "`a\\`", want: []tok{{token.RawString, "`a\\`"}}},
		{name: "raw string spans lines", src: "`a\nb`", want: []tok{{token.RawString, "`a\nb`"}}},
		{name: "raw string with quotes", src: "`\"x\"`", want: []tok{{token.RawString, "`\"x\"`"}}},

		// Comments.
		{name: "comment", src: "// This whole line is a comment.", want: []tok{{token.Comment, "// This whole line is a comment."}}},
		{name: "trailing comment", src: "let a = 1 // tail\nlet", want: []tok{
			{kind: token.KwLet},
			{token.Ident, "a"},
			{kind: token.Assign},
			{token.Int, "1"},
			{token.Comment, "// tail"},
			{kind: token.KwLet},
		}},
		{name: "comment excludes carriage return", src: "// a\r\nx", want: []tok{{token.Comment, "// a"}, {token.Ident, "x"}}},
		{name: "empty comment", src: "//\nx", want: []tok{{token.Comment, "//"}, {token.Ident, "x"}}},
		{name: "comment ends at newline not at slashes", src: "// a // b\n", want: []tok{{token.Comment, "// a // b"}}},

		// Operators and punctuation.
		{name: "comparison operators", src: "== != < <= > >=", want: []tok{
			{kind: token.Eq}, {kind: token.NotEq}, {kind: token.Lt}, {kind: token.LtEq}, {kind: token.Gt}, {kind: token.GtEq},
		}},
		{name: "arithmetic and coalesce", src: "+ - ?? ?", want: []tok{
			{kind: token.Plus}, {kind: token.Minus}, {kind: token.Coalesce}, {kind: token.Question},
		}},
		{name: "punctuation", src: ". , : = -> ( ) [ ] { }", want: []tok{
			{kind: token.Dot},
			{kind: token.Comma},
			{kind: token.Colon},
			{kind: token.Assign},
			{kind: token.Arrow},
			{kind: token.LParen},
			{kind: token.RParen},
			{kind: token.LBracket},
			{kind: token.RBracket},
			{kind: token.LBrace},
			{kind: token.RBrace},
		}},
		{name: "no spaces between operators", src: "a==b!=c<=d>=e", want: []tok{
			{token.Ident, "a"},
			{kind: token.Eq},
			{token.Ident, "b"},
			{kind: token.NotEq},
			{token.Ident, "c"},
			{kind: token.LtEq},
			{token.Ident, "d"},
			{kind: token.GtEq},
			{token.Ident, "e"},
		}},
		{name: "closing angle brackets are separate", src: "map<string, list<string>>", want: []tok{
			{token.Ident, "map"},
			{kind: token.Lt},
			{token.Ident, "string"},
			{kind: token.Comma},
			{token.Ident, "list"},
			{kind: token.Lt},
			{token.Ident, "string"},
			{kind: token.Gt},
			{kind: token.Gt},
		}},
		{name: "closing angle bracket glued to equals", src: "map<string, int>= {}", want: []tok{
			{token.Ident, "map"},
			{kind: token.Lt},
			{token.Ident, "string"},
			{kind: token.Comma},
			{token.Ident, "int"},
			{kind: token.GtEq},
			{kind: token.LBrace},
			{kind: token.RBrace},
		}},
		{name: "coalesce then question", src: "???", want: []tok{{kind: token.Coalesce}, {kind: token.Question}}},
		{name: "optional chaining", src: "a?.b", want: []tok{{kind: token.Ident, text: "a"}, {kind: token.OptDot}, {kind: token.Ident, text: "b"}}},
		{name: "coalesce then dot", src: "??.", want: []tok{{kind: token.Coalesce}, {kind: token.Dot}}},
		{name: "triple equals", src: "===", want: []tok{{kind: token.Eq}, {kind: token.Assign}}},

		// Minus, arrow and the document separator.
		{name: "separator", src: "---", want: []tok{{kind: token.Separator}}},
		{name: "separator between documents", src: "a\n\n---\n\nb", want: []tok{{token.Ident, "a"}, {kind: token.Separator}, {token.Ident, "b"}}},
		{name: "two minus signs", src: "--", want: []tok{{kind: token.Minus}, {kind: token.Minus}}},
		{name: "four minus signs", src: "----", want: []tok{{kind: token.Separator}, {kind: token.Minus}}},
		{name: "separator glued to operands", src: "min_soak---1h", want: []tok{{token.Ident, "min_soak"}, {kind: token.Separator}, {token.Duration, "1h"}}},
		{name: "double negation with space", src: "a - --b", want: []tok{
			{token.Ident, "a"}, {kind: token.Minus}, {kind: token.Minus}, {kind: token.Minus}, {token.Ident, "b"},
		}},
		{name: "arrow", src: "-> list<string>", want: []tok{{kind: token.Arrow}, {token.Ident, "list"}, {kind: token.Lt}, {token.Ident, "string"}, {kind: token.Gt}}},
		{name: "minus before arrow", src: "-->", want: []tok{{kind: token.Minus}, {kind: token.Arrow}}},
		{name: "minus then separator", src: "- ---", want: []tok{{kind: token.Minus}, {kind: token.Separator}}},

		// Larger samples.
		{name: "policy document", src: `
policy deploy.guardrails: DeployApproval

use deploy.common.{eligible}

param min_soak: duration = 24h

when not eligible {
  deny("not_eligible")
}

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}
`, want: []tok{
			{kind: token.KwPolicy},
			{token.Ident, "deploy"},
			{kind: token.Dot},
			{token.Ident, "guardrails"},
			{kind: token.Colon},
			{token.Ident, "DeployApproval"},
			{kind: token.KwUse},
			{token.Ident, "deploy"},
			{kind: token.Dot},
			{token.Ident, "common"},
			{kind: token.Dot},
			{kind: token.LBrace},
			{token.Ident, "eligible"},
			{kind: token.RBrace},
			{kind: token.KwParam},
			{token.Ident, "min_soak"},
			{kind: token.Colon},
			{token.Ident, "duration"},
			{kind: token.Assign},
			{token.Duration, "24h"},
			{kind: token.KwWhen},
			{kind: token.KwNot},
			{token.Ident, "eligible"},
			{kind: token.LBrace},
			{token.Ident, "deny"},
			{kind: token.LParen},
			{token.String, `"not_eligible"`},
			{kind: token.RParen},
			{kind: token.RBrace},
			{kind: token.KwWhen},
			{token.Ident, "release"},
			{kind: token.Dot},
			{token.Ident, "soak"},
			{kind: token.Lt},
			{token.Ident, "min_soak"},
			{kind: token.KwAnd},
			{kind: token.KwNot},
			{token.Ident, "release"},
			{kind: token.Dot},
			{token.Ident, "hotfix"},
			{kind: token.LBrace},
			{token.Ident, "deny"},
			{kind: token.LParen},
			{token.String, `"soak_too_short"`},
			{kind: token.RParen},
			{kind: token.RBrace},
		}},
		{name: "everything on one line", src: `let a = environment == "production" let b = "deployer" in actor.roles guardrails(min_soak: 4h) when a and b { review("service_owner", approvers: approvers) }`, want: []tok{
			{kind: token.KwLet},
			{token.Ident, "a"},
			{kind: token.Assign},
			{token.Ident, "environment"},
			{kind: token.Eq},
			{token.String, `"production"`},
			{kind: token.KwLet},
			{token.Ident, "b"},
			{kind: token.Assign},
			{token.String, `"deployer"`},
			{kind: token.KwIn},
			{token.Ident, "actor"},
			{kind: token.Dot},
			{token.Ident, "roles"},
			{token.Ident, "guardrails"},
			{kind: token.LParen},
			{token.Ident, "min_soak"},
			{kind: token.Colon},
			{token.Duration, "4h"},
			{kind: token.RParen},
			{kind: token.KwWhen},
			{token.Ident, "a"},
			{kind: token.KwAnd},
			{token.Ident, "b"},
			{kind: token.LBrace},
			{token.Ident, "review"},
			{kind: token.LParen},
			{token.String, `"service_owner"`},
			{kind: token.Comma},
			{token.Ident, "approvers"},
			{kind: token.Colon},
			{token.Ident, "approvers"},
			{kind: token.RParen},
			{kind: token.RBrace},
		}},
		{name: "kind header and fn", src: "kind DeployApproval version 1\nfn split(s: string, sep: string) -> list<string>", want: []tok{
			{kind: token.KwKind},
			{token.Ident, "DeployApproval"},
			{kind: token.KwVersion},
			{token.Int, "1"},
			{kind: token.KwFn},
			{token.Ident, "split"},
			{kind: token.LParen},
			{token.Ident, "s"},
			{kind: token.Colon},
			{token.Ident, "string"},
			{kind: token.Comma},
			{token.Ident, "sep"},
			{kind: token.Colon},
			{token.Ident, "string"},
			{kind: token.RParen},
			{kind: token.Arrow},
			{token.Ident, "list"},
			{kind: token.Lt},
			{token.Ident, "string"},
			{kind: token.Gt},
		}},
		{name: "map literal and quantifier", src: `service.labels has {"team": "payments"} and any r in actor.roles: r like "sre-*"`, want: []tok{
			{token.Ident, "service"},
			{kind: token.Dot},
			{token.Ident, "labels"},
			{kind: token.KwHas},
			{kind: token.LBrace},
			{token.String, `"team"`},
			{kind: token.Colon},
			{token.String, `"payments"`},
			{kind: token.RBrace},
			{kind: token.KwAnd},
			{kind: token.KwAny},
			{token.Ident, "r"},
			{kind: token.KwIn},
			{token.Ident, "actor"},
			{kind: token.Dot},
			{token.Ident, "roles"},
			{kind: token.Colon},
			{token.Ident, "r"},
			{kind: token.KwLike},
			{token.String, `"sre-*"`},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := lex(t, tt.src)
			checkTokens(t, got, tt.want)
			checkNoErrors(t, errs)
		})
	}
}

func TestLexErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []tok  // the whole token stream, Illegal tokens included
		msg  string // the one error's message
		help string // and its hint, or empty
		pos  string // the error's span as "line:col-line:col"
	}{
		// Numbers.
		{
			name: "int out of range", src: "9223372036854775808", want: []tok{{token.Illegal, "9223372036854775808"}},
			msg: "integer literal `9223372036854775808` is out of range", help: "integers are signed 64-bit, at most 9223372036854775807", pos: "1:1-1:20",
		},
		{
			name: "float without fraction digits", src: "5.", want: []tok{{token.Illegal, "5."}},
			msg: "a float literal needs digits on both sides of the point", help: "write `5.0`", pos: "1:1-1:3",
		},
		{
			name: "float without fraction digits before space", src: "5. x", want: []tok{{token.Illegal, "5."}, {token.Ident, "x"}},
			msg: "a float literal needs digits on both sides of the point", help: "write `5.0`", pos: "1:1-1:3",
		},
		{
			name: "exponent", src: "1e5", want: []tok{{token.Illegal, "1e5"}},
			msg: "unknown duration unit `e5` in `1e5`", help: "the units are ms, s, m, h and d, like `30m` or `1h30m`", pos: "1:1-1:4",
		},
		{
			name: "hex", src: "0x1F", want: []tok{{token.Illegal, "0x1F"}},
			msg: "unknown duration unit `x1F` in `0x1F`", help: "the units are ms, s, m, h and d, like `30m` or `1h30m`", pos: "1:1-1:5",
		},
		{
			name: "number glued to identifier", src: "1abc", want: []tok{{token.Illegal, "1abc"}},
			msg: "unknown duration unit `abc` in `1abc`", help: "the units are ms, s, m, h and d, like `30m` or `1h30m`", pos: "1:1-1:5",
		},

		// Durations.
		{
			name: "unknown unit after a valid one", src: "1hx", want: []tok{{token.Illegal, "1hx"}},
			msg: "unknown duration unit `hx` in `1hx`", help: "the units are ms, s, m, h and d, like `30m` or `1h30m`", pos: "1:1-1:4",
		},
		{
			name: "unit glued after ms", src: "1mss", want: []tok{{token.Illegal, "1mss"}},
			msg: "unknown duration unit `mss` in `1mss`", help: "the units are ms, s, m, h and d, like `30m` or `1h30m`", pos: "1:1-1:5",
		},
		{
			name: "duration component without unit", src: "1h30", want: []tok{{token.Illegal, "1h30"}},
			msg: "`30` in `1h30` is missing a unit", help: "every component of a duration needs a unit, like `30m`", pos: "1:1-1:5",
		},
		{
			name: "fractional duration", src: "1.5h", want: []tok{{token.Illegal, "1.5h"}},
			msg: "a duration can't have a fractional component", help: "write the fraction as a smaller unit, like `1h30m` instead of `1.5h`", pos: "1:1-1:5",
		},
		{
			name: "units out of order", src: "30m1h", want: []tok{{token.Illegal, "30m1h"}},
			msg: "units in `30m1h` aren't in descending order", help: "write the largest unit first, like `1h30m`", pos: "1:1-1:6",
		},
		{
			name: "unit twice", src: "1h1h", want: []tok{{token.Illegal, "1h1h"}},
			msg: "unit `h` appears twice in `1h1h`", help: "each unit may appear once in a duration; add the components together", pos: "1:1-1:5",
		},
		{
			name: "duration out of range", src: "106752d", want: []tok{{token.Illegal, "106752d"}},
			msg: "duration literal `106752d` is out of range", help: "a duration is at most about 292 years", pos: "1:1-1:8",
		},

		// Strings.
		{
			name: "unterminated string at end of file", src: `"abc`, want: []tok{{token.Illegal, `"abc`}},
			msg: "unterminated string literal", help: "close it with `\"`", pos: "1:1-1:5",
		},
		{
			name: "unterminated string at end of line", src: "\"abc\ndef", want: []tok{{token.Illegal, `"abc`}, {token.Ident, "def"}},
			msg: "unterminated string literal", help: "a double-quoted string can't span lines; close it with `\"` or use a raw string in backticks", pos: "1:1-1:5",
		},
		{
			name: "unterminated string ending in backslash", src: `"abc\`, want: []tok{{token.Illegal, `"abc\`}},
			msg: "unterminated string literal", help: "close it with `\"`", pos: "1:1-1:6",
		},
		{
			name: "unknown escape", src: `"a\qb"`, want: []tok{{token.Illegal, `"a\qb"`}},
			msg: "unknown escape sequence `\\q`", help: `the escapes are \n, \t, \\, \", \xhh, \uhhhh and the other Go string escapes`, pos: "1:3-1:5",
		},
		{
			name: "unknown escape after multi-byte character", src: `"é\qb"`, want: []tok{{token.Illegal, `"é\qb"`}},
			msg: "unknown escape sequence `\\q`", help: `the escapes are \n, \t, \\, \", \xhh, \uhhhh and the other Go string escapes`, pos: "1:3-1:5",
		},
		{
			name: "escaped single quote", src: `"\'"`, want: []tok{{token.Illegal, `"\'"`}},
			msg: "unknown escape sequence `\\'`", help: "a single quote needs no escape in a double-quoted string", pos: "1:2-1:4",
		},
		{
			name: "short hex escape", src: `"\x4"`, want: []tok{{token.Illegal, `"\x4"`}},
			msg: "unknown escape sequence `\\x`", help: `the escapes are \n, \t, \\, \", \xhh, \uhhhh and the other Go string escapes`, pos: "1:2-1:4",
		},
		{
			name: "unterminated raw string", src: "`abc\ndef", want: []tok{{token.Illegal, "`abc\ndef"}},
			msg: "unterminated raw string literal", help: "close it with a backtick", pos: "1:1-2:4",
		},

		// Symbols from other languages.
		{
			name: "bang", src: "!a", want: []tok{{token.Illegal, "!"}, {token.Ident, "a"}},
			msg: "unexpected character `!`", help: "use `not` to negate a condition, or `!=` to compare", pos: "1:1-1:2",
		},
		{
			name: "ampersands", src: "a && b", want: []tok{{token.Ident, "a"}, {token.Illegal, "&&"}, {token.Ident, "b"}},
			msg: "unexpected character `&&`", help: "use `and`", pos: "1:3-1:5",
		},
		{
			name: "single ampersand", src: "&", want: []tok{{token.Illegal, "&"}},
			msg: "unexpected character `&`", help: "use `and`", pos: "1:1-1:2",
		},
		{
			name: "pipes", src: "a || b", want: []tok{{token.Ident, "a"}, {token.Illegal, "||"}, {token.Ident, "b"}},
			msg: "unexpected character `||`", help: "use `or`", pos: "1:3-1:5",
		},

		// Anything else.
		{
			name: "at sign", src: "@", want: []tok{{token.Illegal, "@"}},
			msg: "unexpected character `@`", pos: "1:1-1:2",
		},
		{
			name: "non-ascii letter", src: "é", want: []tok{{token.Illegal, "é"}},
			msg: "unexpected character `é`", pos: "1:1-1:2",
		},
		{
			name: "non-breaking space", src: "a b", want: []tok{{token.Ident, "a"}, {token.Illegal, " "}, {token.Ident, "b"}},
			msg: "unexpected character U+00A0", pos: "1:2-1:3",
		},
		{
			name: "control character", src: "\x01", want: []tok{{token.Illegal, "\x01"}},
			msg: "unexpected character U+0001", pos: "1:1-1:2",
		},
		{
			name: "invalid utf-8", src: "\xff", want: []tok{{token.Illegal, "\xff"}},
			msg: "unexpected character U+FFFD", pos: "1:1-1:2",
		},
		{
			name: "single slash", src: "a / b", want: []tok{{token.Ident, "a"}, {token.Illegal, "/"}, {token.Ident, "b"}},
			msg: "unexpected character `/`", pos: "1:3-1:4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := lex(t, tt.src)
			checkTokens(t, got, tt.want)
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
			}
			e := errs[0]
			if e.Msg != tt.msg {
				t.Errorf("Msg = %q, want %q", e.Msg, tt.msg)
			}
			if e.Help != tt.help {
				t.Errorf("Help = %q, want %q", e.Help, tt.help)
			}
			if got := e.Pos.String() + "-" + e.End.String(); got != tt.pos {
				t.Errorf("span = %s, want %s", got, tt.pos)
			}
		})
	}
}

// TestLexRecovers checks that an error doesn't hide the tokens after it and
// that every error is reported.
func TestLexRecovers(t *testing.T) {
	got, errs := lex(t, "a @ b # c\n\"unterminated\n1h1h d")
	checkTokens(t, got, []tok{
		{token.Ident, "a"},
		{token.Illegal, "@"},
		{token.Ident, "b"},
		{token.Illegal, "#"},
		{token.Ident, "c"},
		{token.Illegal, `"unterminated`},
		{token.Illegal, "1h1h"},
		{token.Ident, "d"},
	})
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	want := []string{
		"1:3: unexpected character `@`",
		"1:7: unexpected character `#`",
		"2:1: unterminated string literal",
		"3:1: unit `h` appears twice in `1h1h`",
	}
	if g, w := strings.Join(msgs, "\n"), strings.Join(want, "\n"); g != w {
		t.Errorf("errors:\n%s\nwant:\n%s", g, w)
	}
}

func TestLexPositions(t *testing.T) {
	pos := func(off, line, col int) token.Pos { return token.Pos{Offset: off, Line: line, Column: col} }

	tests := []struct {
		name string
		src  string
		want []token.Token
	}{
		{name: "empty", src: "", want: []token.Token{
			{Kind: token.EOF, Pos: pos(0, 1, 1), End: pos(0, 1, 1)},
		}},
		{name: "lines", src: "let a =\n  \"b\"\n", want: []token.Token{
			{Kind: token.KwLet, Text: "let", Pos: pos(0, 1, 1), End: pos(3, 1, 4)},
			{Kind: token.Ident, Text: "a", Pos: pos(4, 1, 5), End: pos(5, 1, 6)},
			{Kind: token.Assign, Text: "=", Pos: pos(6, 1, 7), End: pos(7, 1, 8)},
			{Kind: token.String, Text: `"b"`, Pos: pos(10, 2, 3), End: pos(13, 2, 6)},
			{Kind: token.EOF, Pos: pos(14, 3, 1), End: pos(14, 3, 1)},
		}},
		{name: "columns count characters not bytes", src: `x = "é" y`, want: []token.Token{
			{Kind: token.Ident, Text: "x", Pos: pos(0, 1, 1), End: pos(1, 1, 2)},
			{Kind: token.Assign, Text: "=", Pos: pos(2, 1, 3), End: pos(3, 1, 4)},
			{Kind: token.String, Text: `"é"`, Pos: pos(4, 1, 5), End: pos(8, 1, 8)},
			{Kind: token.Ident, Text: "y", Pos: pos(9, 1, 9), End: pos(10, 1, 10)},
			{Kind: token.EOF, Pos: pos(10, 1, 10), End: pos(10, 1, 10)},
		}},
		{name: "crlf line endings", src: "a\r\nb", want: []token.Token{
			{Kind: token.Ident, Text: "a", Pos: pos(0, 1, 1), End: pos(1, 1, 2)},
			{Kind: token.Ident, Text: "b", Pos: pos(3, 2, 1), End: pos(4, 2, 2)},
			{Kind: token.EOF, Pos: pos(4, 2, 2), End: pos(4, 2, 2)},
		}},
		{name: "tab is one column", src: "\ta", want: []token.Token{
			{Kind: token.Ident, Text: "a", Pos: pos(1, 1, 2), End: pos(2, 1, 3)},
			{Kind: token.EOF, Pos: pos(2, 1, 3), End: pos(2, 1, 3)},
		}},
		{name: "raw string spanning lines", src: "`a\nb` c", want: []token.Token{
			{Kind: token.RawString, Text: "`a\nb`", Pos: pos(0, 1, 1), End: pos(5, 2, 3)},
			{Kind: token.Ident, Text: "c", Pos: pos(6, 2, 4), End: pos(7, 2, 5)},
			{Kind: token.EOF, Pos: pos(7, 2, 5), End: pos(7, 2, 5)},
		}},
		{name: "comment then newline", src: "// c\nx", want: []token.Token{
			{Kind: token.Comment, Text: "// c", Pos: pos(0, 1, 1), End: pos(4, 1, 5)},
			{Kind: token.Ident, Text: "x", Pos: pos(5, 2, 1), End: pos(6, 2, 2)},
			{Kind: token.EOF, Pos: pos(6, 2, 2), End: pos(6, 2, 2)},
		}},
		{name: "separator", src: "a\n---\nb", want: []token.Token{
			{Kind: token.Ident, Text: "a", Pos: pos(0, 1, 1), End: pos(1, 1, 2)},
			{Kind: token.Separator, Text: "---", Pos: pos(2, 2, 1), End: pos(5, 2, 4)},
			{Kind: token.Ident, Text: "b", Pos: pos(6, 3, 1), End: pos(7, 3, 2)},
			{Kind: token.EOF, Pos: pos(7, 3, 2), End: pos(7, 3, 2)},
		}},
		{name: "adjacent tokens share a boundary", src: "a.b", want: []token.Token{
			{Kind: token.Ident, Text: "a", Pos: pos(0, 1, 1), End: pos(1, 1, 2)},
			{Kind: token.Dot, Text: ".", Pos: pos(1, 1, 2), End: pos(2, 1, 3)},
			{Kind: token.Ident, Text: "b", Pos: pos(2, 1, 3), End: pos(3, 1, 4)},
			{Kind: token.EOF, Pos: pos(3, 1, 4), End: pos(3, 1, 4)},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New([]byte(tt.src))
			for i, want := range tt.want {
				got := l.Next()
				if got != want {
					t.Errorf("token %d = %+v, want %+v", i, got, want)
				}
			}
			checkNoErrors(t, l.Errors())
		})
	}
}

func TestNextAfterEOF(t *testing.T) {
	l := New([]byte("a"))
	l.Next()
	for i := 0; i < 3; i++ {
		got := l.Next()
		want := token.Token{Kind: token.EOF, Pos: token.Pos{Offset: 1, Line: 1, Column: 2}, End: token.Pos{Offset: 1, Line: 1, Column: 2}}
		if got != want {
			t.Errorf("Next() after EOF = %+v, want %+v", got, want)
		}
	}
}

// lex tokenizes src and returns every token before EOF plus the errors.
func lex(t *testing.T, src string) ([]token.Token, []*diag.Error) {
	t.Helper()
	l := New([]byte(src))
	var toks []token.Token
	for i := 0; ; i++ {
		tk := l.Next()
		if tk.Kind == token.EOF {
			return toks, l.Errors()
		}
		if tk.End.Offset <= tk.Pos.Offset {
			t.Fatalf("token %d %v is empty; the lexer would never advance", i, tk)
		}
		toks = append(toks, tk)
	}
}

// checkTokens compares kinds and texts, ignoring positions.
func checkTokens(t *testing.T, got []token.Token, want []tok) {
	t.Helper()
	gs := make([]string, 0, len(got))
	ws := make([]string, 0, len(want))
	for _, g := range got {
		gs = append(gs, g.Kind.String()+" "+g.Text)
	}
	for _, w := range want {
		ws = append(ws, w.String())
	}
	if g, w := strings.Join(gs, "\n"), strings.Join(ws, "\n"); g != w {
		t.Errorf("tokens:\n%s\nwant:\n%s", g, w)
	}
}

func checkNoErrors(t *testing.T, errs []*diag.Error) {
	t.Helper()
	for _, e := range errs {
		t.Errorf("unexpected error: %v", e)
	}
}

// checkLiteralErr fails unless err's message is msg, where an empty msg means
// no error is expected.
func checkLiteralErr(t *testing.T, err *diag.Error, msg string) {
	t.Helper()
	switch {
	case msg == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case msg != "" && err == nil:
		t.Fatalf("no error, want %q", msg)
	case msg != "" && err.Msg != msg:
		t.Errorf("Msg = %q, want %q", err.Msg, msg)
	}
}
