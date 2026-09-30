package eval_test

import (
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/parser"
)

// TestCompilePolicyErrors checks what CompilePolicy rejects.
func TestCompilePolicyErrors(t *testing.T) {
	const src = "policy p: Test@1\nparam approvers: list<string>\nwhen true { review(reason: a, approvers: approvers) }"
	tests := []struct {
		name    string
		params  map[string]eval.Value
		err     string
		help    string
		checked bool
	}{
		{name: "unbound param", checked: true, err: "p.sigil:2:1: param `approvers` has no value",
			help: `bind it with policy.Params{"approvers": ...} when compiling, or give it a default`},
		{name: "unchecked policy", params: map[string]eval.Value{"approvers": reflect.ValueOf([]string{})},
			err: "p.sigil:3:13: constructor `review` wasn't checked; compile only checked policies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := parser.ParseFile("p.sigil", []byte(src))
			doc := f.Docs[0].(*ast.PolicyDoc)
			k, b, _ := gokind.Build(gokind.Options{
				Name: "Test", Version: 1, Input: typeOf[Input](), Ranked: true,
				Decisions: []gokind.Decision{{Name: "deny", Payload: typeOf[None](), Reasons: []string{"b", "a", "d", "not_eligible", "soak_too_short"}}, {Name: "review", Payload: typeOf[ReviewData](), Reasons: []string{"c", "a", "service_owner"}}},
				Default:   &gokind.Default{Decision: "deny", Reason: "a"},
			})
			c := check.New("p.sigil")
			if tt.checked {
				c.Policy(doc, k)
			}
			_, err := eval.CompilePolicy(&eval.Source{Doc: doc, Info: c.Info(), File: "p.sigil", Src: []byte(src)}, k, b, nil, eval.Options{Params: tt.params})
			if err == nil || err.Error() != tt.err {
				t.Fatalf("CompilePolicy() error = %v, want %q", err, tt.err)
			}
			if tt.help != "" && err.Help != tt.help {
				t.Errorf("help = %q, want %q", err.Help, tt.help)
			}
		})
	}
}
