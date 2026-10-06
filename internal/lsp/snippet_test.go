package lsp

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
)

// TestSnippetsCheck accepts every snippet completion offers at each
// marker and tabs through it, leaving each placeholder's default, and
// checks that the document it leaves has no error: a snippet's defaults
// are code. Where all is false, only the snippets of the type the
// context expects are tried, as the others don't fit there.
func TestSnippetsCheck(t *testing.T) {
	tests := []struct {
		name string
		file string // production.sigil when empty
		src  string
		all  bool
	}{
		{name: "a statement", src: head + "<|>\n", all: true},
		{name: "a statement after a let", src: head + "let name = 1\nlet x = name > 0\nwhen x {\n  deny(reason: not_eligible)\n}\n<|>\n", all: true},
		{name: "a statement in a body", src: head + "when cleared {\n  <|>\n}\n", all: true},
		{name: "a module statement", file: "common.sigil", src: "module deploy.common: DeployApproval@2\n\n<|>\n", all: true},
		{name: "a let's value", src: head + "let v = <|>\n", all: true},
		{name: "above an import", src: "policy payments.production: DeployApproval@2\n\n<|>\nuse deploy.common.{cleared}\n\nwhen cleared {\n  deny(reason: not_eligible)\n}\n", all: true},
		{name: "an element of a list default", src: head + "param p: list<string> = [<|>]\n"},
		{name: "an element of a map default", src: head + "param p: map<string, string> = {<|>: \"x\"}\n"},
		{name: "a condition", src: head + "when <|> {\n  deny(reason: not_eligible)\n}\n"},
		{name: "a quantifier's body", src: head + "when any c in changes: <|> {\n  deny(reason: not_eligible)\n}\n"},
		{name: "a duration", src: head + "when release.soak > <|> {\n  deny(reason: not_eligible)\n}\n"},
		{name: "a string", src: head + "when environment == <|> {\n  deny(reason: not_eligible)\n}\n"},
		{name: "a list", src: head + "when environment in <|> {\n  deny(reason: not_eligible)\n}\n"},
		{name: "a payload field", src: head + "when cleared {\n  review(reason: service_owner, approvers: <|>)\n}\n"},
		{name: "a host function's argument", src: head + "let v = split(<|>, \",\")\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := tt.file
			if file == "" {
				file = "production.sigil"
			}
			v, offset := viewOf(t, file, tt.src)
			items, from, to := v.complete(offset)
			src := strings.Replace(tt.src, marker, "", 1)
			tried := 0
			for _, it := range items {
				if it.snippet == "" || !tt.all && it.rank != rankExpected {
					continue
				}
				tried++
				text := src[:from] + expand(it.snippet) + src[to:]
				l := &memLoader{files: testWorkspace(t)}
				snap := l.Load(root, map[string][]byte{root + "/" + file: []byte(text)})
				for _, e := range snap.Diagnostics {
					if e.File == root+"/"+file && e.Severity == diag.SeverityError {
						t.Errorf("%s inserts %q, which leaves %s", it.label, it.snippet, e.Error())
					}
				}
			}
			if tried == 0 {
				t.Errorf("no snippet to try: %+v", items)
			}
		})
	}
}

// TestExpand checks the snippet expansion the tests use.
func TestExpand(t *testing.T) {
	tests := []struct{ snippet, want string }{
		{"when ${1:true} {\n\t$0\n}", "when true {\n\t\n}"},
		{`f(${1:""}, $2)`, `f("", )`},
		{`${1:{\}}`, "{}"},
		{`a\$b \\ ${2}`, `a$b \ `},
	}
	for _, tt := range tests {
		if got := expand(tt.snippet); got != tt.want {
			t.Errorf("expand(%q) = %q, want %q", tt.snippet, got, tt.want)
		}
	}
}

// expand returns the text a snippet leaves when its user tabs through
// every placeholder: each placeholder's default, and nothing for a bare
// tabstop.
func expand(snippet string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(snippet); i++ {
		switch c := snippet[i]; {
		case c == '\\' && i+1 < len(snippet):
			i++
			b.WriteByte(snippet[i])
		case c == '$' && i+1 < len(snippet) && snippet[i+1] == '{':
			i += 2
			for i < len(snippet) && snippet[i] >= '0' && snippet[i] <= '9' {
				i++
			}
			if i < len(snippet) && snippet[i] == '}' {
				continue
			}
			depth++ // past the `:`, the default follows
		case c == '$':
			for i+1 < len(snippet) && snippet[i+1] >= '0' && snippet[i+1] <= '9' {
				i++
			}
		case c == '}' && depth > 0:
			depth--
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
