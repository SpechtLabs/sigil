package format_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/format"
	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/token"
)

func FuzzFormat(f *testing.F) {
	for _, pattern := range []string{"testdata/*.sigil", "../parser/testdata/*.sigil"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		for _, file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(src)
		}
	}
	f.Add([]byte("// comment only\n"))
	f.Add([]byte("policy p: K@1\nlet x = 0 .field\n"))
	f.Add([]byte("kind K version 1\nenum T: a |\n  b // x\ndecision d(f: int = 1, // y\n) { r // z\n s }\ndefault d(r)\n"))
	f.Add([]byte("kind K version 1\ndecision d {\n  f: T = a // y\n  in: int\n  reason: r\n    | s\n}\n"))
	f.Fuzz(func(t *testing.T, src []byte) {
		out, errs := format.Source("fuzz.sigil", src)
		if errs != nil {
			if out != nil {
				t.Fatal("formatter returned partial source on error")
			}
			return
		}
		if _, parseErrs := parser.ParseFile("formatted.sigil", out); parseErrs != nil {
			t.Fatalf("formatted source does not parse: %v\n%s", parseErrs, out)
		}
		again, errs := format.Source("fuzz.sigil", out)
		if errs != nil || !bytes.Equal(out, again) {
			t.Fatalf("formatting is not idempotent: %v\n%s\nthen:\n%s", errs, out, again)
		}
		if before, after := comments(src), comments(out); before != after {
			t.Fatalf("comments changed: %q != %q", before, after)
		}
	})
}

// comments lists the comments in src, sorted: moving a decision's
// reason ahead of its payload fields moves the comments that go with it.
func comments(src []byte) string {
	var out []string
	l := lexer.New(src)
	for tok := l.Next(); tok.Kind != token.EOF; tok = l.Next() {
		if tok.Kind == token.Comment {
			out = append(out, strings.TrimRight(tok.Text, " \t\r"))
		}
	}
	slices.Sort(out)
	return strings.Join(out, "\n")
}
