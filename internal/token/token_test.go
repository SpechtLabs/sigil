package token

import "testing"

func TestLookup(t *testing.T) {
	tests := []struct {
		ident string
		want  Kind
	}{
		{"policy", KwPolicy},
		{"module", KwModule},
		{"use", KwUse},
		{"as", KwAs},
		{"param", KwParam},
		{"let", KwLet},
		{"pub", KwPub},
		{"when", KwWhen},
		{"assert", KwAssert},
		{"kind", KwKind},
		{"version", KwVersion},
		{"type", KwType},
		{"input", KwInput},
		{"fn", KwFn},
		{"decision", KwDecision},
		{"precedence", KwPrecedence},
		{"collect", KwCollect},
		{"default", KwDefault},
		{"and", KwAnd},
		{"or", KwOr},
		{"xor", KwXor},
		{"not", KwNot},
		{"in", KwIn},
		{"all", KwAll},
		{"any", KwAny},
		{"filter", KwFilter},
		{"one", KwOne},
		{"exclusive", KwExclusive},
		{"has", KwHas},
		{"like", KwLike},
		{"matches", KwMatches},
		{"present", KwPresent},
		{"true", KwTrue},
		{"false", KwFalse},
		{"outcome", KwOutcome},

		// Case matters.
		{"When", Ident},
		{"TRUE", Ident},
		// Built-in type names and decision names are ordinary identifiers.
		{"string", Ident},
		{"duration", Ident},
		{"list", Ident},
		{"map", Ident},
		{"deny", Ident},
		{"approve", Ident},
		// Symbols other languages use aren't words.
		{"", Ident},
		{"&&", Ident},
	}

	for _, tt := range tests {
		t.Run(tt.ident, func(t *testing.T) {
			if got := Lookup(tt.ident); got != tt.want {
				t.Errorf("Lookup(%q) = %v, want %v", tt.ident, got, tt.want)
			}
		})
	}
}

// TestEveryKeywordIsListed guards the keyword table against a keyword added
// to the Kind constants without a spelling, which Lookup would never return.
func TestEveryKeywordIsListed(t *testing.T) {
	n := 0
	for k := keywordBeg + 1; k < keywordEnd; k++ {
		n++
		if !k.IsKeyword() {
			t.Errorf("%v: IsKeyword() = false", k)
		}
		if got := Lookup(k.String()); got != k {
			t.Errorf("Lookup(%q) = %v, want %v", k.String(), got, k)
		}
	}
	if n != len(keywords) {
		t.Errorf("%d keyword kinds but %d keyword spellings", n, len(keywords))
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{Illegal, "illegal token"},
		{EOF, "end of file"},
		{Comment, "comment"},
		{Ident, "identifier"},
		{Int, "integer"},
		{Float, "float"},
		{Duration, "duration"},
		{String, "string"},
		{RawString, "raw string"},
		{Eq, "=="},
		{NotEq, "!="},
		{Coalesce, "??"},
		{OptDot, "?."},
		{At, "@"},
		{Arrow, "->"},
		{Separator, "---"},
		{LBrace, "{"},
		{KwWhen, "when"},
		{KwOutcome, "outcome"},
		// Markers and out-of-range values still print something.
		{literalBeg, "kind(3)"},
		{Kind(200), "kind(200)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestKindClasses(t *testing.T) {
	tests := []struct {
		kind     Kind
		literal  bool
		operator bool
		keyword  bool
	}{
		{kind: Illegal},
		{kind: EOF},
		{kind: Comment},
		{kind: Ident, literal: true},
		{kind: Int, literal: true},
		{kind: RawString, literal: true},
		{kind: Eq, operator: true},
		{kind: Separator, operator: true},
		{kind: KwPolicy, keyword: true},
		{kind: KwOutcome, keyword: true},
		{kind: literalBeg},
		{kind: literalEnd},
		{kind: operatorBeg},
		{kind: keywordEnd},
	}

	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			if got := tt.kind.IsLiteral(); got != tt.literal {
				t.Errorf("IsLiteral() = %v, want %v", got, tt.literal)
			}
			if got := tt.kind.IsOperator(); got != tt.operator {
				t.Errorf("IsOperator() = %v, want %v", got, tt.operator)
			}
			if got := tt.kind.IsKeyword(); got != tt.keyword {
				t.Errorf("IsKeyword() = %v, want %v", got, tt.keyword)
			}
		})
	}
}

func TestPos(t *testing.T) {
	tests := []struct {
		pos   Pos
		want  string
		valid bool
	}{
		{Pos{}, "-", false},
		{Pos{Offset: 0, Line: 1, Column: 1}, "1:1", true},
		{Pos{Offset: 40, Line: 12, Column: 21}, "12:21", true},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.pos.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			if got := tt.pos.IsValid(); got != tt.valid {
				t.Errorf("IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestTokenString(t *testing.T) {
	at := Pos{Offset: 4, Line: 2, Column: 3}
	tests := []struct {
		tok  Token
		want string
	}{
		{Token{Kind: EOF, Pos: at}, "2:3 end of file"},
		{Token{Kind: KwWhen, Text: "when", Pos: at}, "2:3 when"},
		{Token{Kind: LBrace, Text: "{", Pos: at}, "2:3 {"},
		{Token{Kind: Ident, Text: "release", Pos: at}, `2:3 identifier "release"`},
		{Token{Kind: String, Text: `"a\"b"`, Pos: at}, `2:3 string "\"a\\\"b\""`},
		{Token{Kind: Illegal, Text: "@", Pos: at}, `2:3 illegal token "@"`},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.tok.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
