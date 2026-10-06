package gogen_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gogen"
)

// tail is what every kind source below ends with: one decision, ranked
// and defaulted, so each case adds only what it tests.
const tail = "\ndecision deny {\n  reason: no\n}\n\ncollect one\nprecedence deny\n\ndefault deny(reason: no)\n"

// TestGenerate covers Generate on a kind model with no source to point
// into: the package name it writes, and every problem it reports, each
// without a position.
func TestGenerate(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		pkg      string
		wantPkg  string   // the package clause of the generated file
		wantErrs []string // the messages, in order, with their help after " | "
	}{
		{name: "default package", src: "kind DeployApproval version 1\n" + tail, wantPkg: "deployapproval"},
		{name: "named package", src: "kind DeployApproval version 1\n" + tail, pkg: "approval", wantPkg: "approval"},
		{
			name: "a package that isn't an identifier", src: "kind DeployApproval version 1\n" + tail, pkg: "a.b",
			wantErrs: []string{`package name "a.b" isn't a Go identifier | name another with --package, a Go identifier that isn't a keyword, like ` + "`deployapproval`"},
		},
		{
			name: "a kind named after a keyword", src: "kind Go version 1\n" + tail,
			wantErrs: []string{`package name "go" is a Go keyword | the package is named after the kind; name another with --package, a Go identifier that isn't a keyword, like ` + "`deployapproval`"},
		},
		{
			name: "a reserved enum name", src: "kind K version 1\n\nenum error: a\n\ninput e: error\n" + tail,
			wantErrs: []string{"Go code can't declare enum error: error is predeclared in Go, and the generated code uses Go's own | a generated type keeps the kind's name, which is the one policies write; rename enum error in the kind, which is a breaking change"},
		},
		{
			name: "an unused type", src: "kind K version 1\n\ntype T {\n  x: int\n}\n" + tail,
			wantErrs: []string{"type T is used by no input, host function or payload, so a Go kind can't declare it | use it in an input, a host function or a payload, or remove it"},
		},
		{
			name: "types out of order", src: "kind K version 1\n\ntype B {\n  x: int\n}\n\ntype A {\n  b: B\n}\n\ninput a: A\n" + tail,
			wantErrs: []string{"a Go kind can't declare type B here | a Go kind declares struct types in the order its inputs, host functions and payloads first use them; declare them as A, B, which changes no policy"},
		},
		{
			name: "a type reached through a function", src: "kind K version 1\n\ntype A {\n  x: int\n}\n\ntype B {\n  y: int\n}\n\ninput b: B\n\nfn f(A) -> bool\n" + tail,
			wantErrs: []string{"a Go kind can't declare type A here | a Go kind declares struct types in the order its inputs, host functions and payloads first use them; declare them as B, A, which changes no policy"},
		},
		{
			name: "enums out of order", src: "kind K version 1\n\nenum B: b\nenum A: a\n\ninput x: A\ninput y: B\n" + tail,
			wantErrs: []string{"a Go kind can't declare enum B here | a Go kind declares the enums its inputs, host functions and payloads use in the order they first use them, then the others; declare them as A, B, which changes no policy"},
		},
		{
			name: "an enum reached through a payload", src: "kind K version 1\n\nenum P: p\nenum I: i\n\ninput x: I\n\ndecision allow {\n  reason: ok\n  p: P = p\n}\n\ndecision deny {\n  reason: no\n}\n\ncollect one\nprecedence allow > deny\n\ndefault deny(reason: no)\n",
			wantErrs: []string{"a Go kind can't declare enum P here | a Go kind declares the enums its inputs, host functions and payloads use in the order they first use them, then the others; declare them as I, P, which changes no policy"},
		},
		{
			name: "precedence out of declaration order", src: "kind K version 1\n\ndecision allow {\n  reason: ok\n}\n\ndecision deny {\n  reason: no\n}\n\ncollect one\nprecedence deny > allow\n\ndefault deny(reason: no)\n",
			wantErrs: []string{"a Go kind can't rank its decisions in another order than it declares them: policy.WithDecisions takes them in precedence order | declare the decisions in precedence order, deny, allow, which changes no policy"},
		},
		{
			name: "a collecting kind's precedence is free", src: "kind K version 1\n\ndecision a {\n  reason: x\n}\n\ndecision b {\n  reason: y\n}\n\ncollect all\nprecedence b > a\n", wantPkg: "k",
		},
		{
			name: "default and conflict arguments", src: "kind K version 1\n\ndecision deny {\n  reason: no | clash\n  code: int = 1\n}\n\ncollect one\nprecedence deny\n\ndefault deny(reason: no, code: 1)\nconflict deny(reason: clash, code: 2)\n",
			wantErrs: []string{
				"a Go kind can't pass code to its default | the argument equals the default of deny.code; drop it",
				"a Go kind can't pass code to its conflict | a Go kind's conflict takes its payload from the fields' defaults; give deny.code the default instead, and drop the argument",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, errs := check.LoadKind("k.sigil", []byte(tt.src))
			if errs != nil {
				t.Fatalf("the kind doesn't load: %v", errs)
			}
			code, errs := gogen.Generate(k, gogen.Options{Package: tt.pkg})
			got := make([]string, len(errs))
			for i, e := range errs {
				if e.Pos.Line != 0 {
					t.Errorf("diagnostic %q has a position without a locator", e.Msg)
				}
				got[i] = e.Msg + " | " + e.Help
			}
			if strings.Join(got, "\n") != strings.Join(tt.wantErrs, "\n") {
				t.Fatalf("Generate() reported\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.wantErrs, "\n"))
			}
			if tt.wantErrs != nil {
				if code != nil {
					t.Errorf("Generate() = %d bytes with diagnostics, want nil", len(code))
				}
				return
			}
			f, err := parser.ParseFile(token.NewFileSet(), "kind.go", code, parser.PackageClauseOnly)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name.Name != tt.wantPkg {
				t.Errorf("package %s, want %s", f.Name.Name, tt.wantPkg)
			}
		})
	}
}

// TestGenerateNil covers Generate without a kind.
func TestGenerateNil(t *testing.T) {
	if code, errs := gogen.Generate(nil, gogen.Options{}); code != nil || errs == nil {
		t.Fatalf("Generate(nil) = %d bytes, %v; want an error", len(code), errs)
	}
}

// TestGenerateFile covers a kind file that doesn't parse or load: the
// loader's diagnostics come back, with no kind and no code.
func TestGenerateFile(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "doesn't parse", src: "kind K version\n", want: "k.sigil:2:1"},
		{name: "doesn't load", src: "kind K version 1\n\ninput x: nope\n" + tail, want: "unknown type"},
		{name: "not a kind", src: "policy p: K@1\n", want: "expected a kind document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, k, errs := gogen.GenerateFile("k.sigil", []byte(tt.src), gogen.Options{})
			if code != nil || k != nil || errs == nil {
				t.Fatalf("GenerateFile() = %d bytes, kind %v, errors %v; want only errors", len(code), k, errs)
			}
			if !strings.Contains(errs.Error(), tt.want) {
				t.Errorf("errors = %v, want %q", errs, tt.want)
			}
		})
	}
}
