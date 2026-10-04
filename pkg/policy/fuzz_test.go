package policy_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/format"
	"github.com/spechtlabs/sigil/pkg/policy"
)

func FuzzCompileEval(f *testing.F) {
	for _, src := range []string{"", "policy p: DeployApproval@1", "policy p: DeployApproval@1\nwhen release.hotfix { approve(reason: a) }", "policy p: DeployApproval@1\nassert(\"named\", service.name != \"\")\nreview(reason: a, approvers: service.owners)", "module m: DeployApproval@1\npub let ok = release.hotfix\n---\npolicy p: DeployApproval@1\nuse m.{ok}\nwhen ok { approve(reason: a) }"} {
		f.Add(src, "production", true)
	}
	// The golden bundles: most are of policy p, in the kind Deploy is.
	files, err := filepath.Glob("testdata/*.sigil")
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(src), "production", false)
	}
	f.Fuzz(func(t *testing.T, src, env string, hotfix bool) {
		p, err := Deploy.Compile(src, "p")
		if err != nil {
			return
		}
		in := Input{Release: Release{Hotfix: hotfix}, Environment: env}
		first, err := p.Eval(context.Background(), in)
		again, err2 := p.Eval(context.Background(), in)
		if !reflect.DeepEqual(first, again) || errorText(err) != errorText(err2) {
			t.Fatal("evaluation is not repeatable")
		}
		formatted, errs := format.Source("", []byte(src))
		if errs != nil {
			t.Fatal(errs)
		}
		other, err := Deploy.Compile(string(formatted), "p")
		if err != nil {
			t.Fatalf("formatting broke compilation: %v", err)
		}
		got, err3 := other.Eval(context.Background(), in)
		// Formatting moves diagnostics and trace positions. Outcome data and
		// error categories must retain their meaning.
		if reflect.TypeOf(err2) != reflect.TypeOf(err3) || !reflect.DeepEqual(outcomes(first), outcomes(got)) {
			t.Fatal("formatting changed policy behavior")
		}
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func outcomes(r *policy.Result) []any {
	out := make([]any, len(r.Outcome))
	for i, e := range r.Outcome {
		out[i] = []any{e.Decision, e.Reason, e.Payload}
	}
	return out
}

// Generated rules reach resolution on every mutation. Reordering rules
// must preserve the winner.
func FuzzPolicyOrder(f *testing.F) {
	f.Add(true, false, uint8(0))
	f.Add(false, true, uint8(3))
	f.Fuzz(func(t *testing.T, hotfix, blocked bool, permutation uint8) {
		rules := []string{
			"when release.hotfix { approve(reason: a) }",
			"when environment == \"blocked\" { deny(reason: b) }",
			"when not release.hotfix { review(reason: a, approvers: []) }",
		}
		for i := len(rules) - 1; i > 0; i-- {
			j := int(permutation) % (i + 1)
			rules[i], rules[j] = rules[j], rules[i]
			permutation /= uint8(i + 1)
		}
		src := "policy p: DeployApproval@1\n" + strings.Join(rules, "\n")
		p, err := Deploy.Compile(src, "p")
		if err != nil {
			t.Fatal(err)
		}
		in := Input{Release: Release{Hotfix: hotfix}}
		want := "review"
		if hotfix {
			want = "approve"
		}
		if blocked {
			in.Environment, want = "blocked", "deny"
		}
		got, err := p.Eval(context.Background(), in)
		if err != nil || got.Decision != want {
			t.Fatalf("got %v, %v; want %s", got, err, want)
		}
	})
}

func FuzzComposition(f *testing.F) {
	f.Add("production", true, false)
	f.Add("blocked", false, true)
	f.Fuzz(func(t *testing.T, env string, hotfix, gated bool) {
		guard := "policy guard: DeployApproval@1\nwhen environment == \"blocked\" { deny(reason: b) }"
		call := "guard()"
		if gated {
			call = "when release.hotfix { guard() }"
		}
		root := "policy p: DeployApproval@1\nuse guard\n" + call + "\nwhen true { approve(reason: a) }"
		// MapFS is deliberately not a comparable Go value. From accepts any
		// fs.FS, including one backed by a map.
		trusted := policy.MapFS(map[string]string{"guard.sigil": guard})
		p, err := Deploy.Load(policy.MapFS(map[string]string{"root.sigil": root}), "p",
			policy.Require("guard", policy.From(trusted)), policy.Require("guard", policy.From(trusted)))
		if gated {
			if err == nil {
				t.Fatal("gated invocation satisfied Require")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		in := Input{Environment: env, Release: Release{Hotfix: hotfix}}
		got, err := p.Eval(context.Background(), in)
		want := "approve"
		if env == "blocked" {
			want = "deny"
		}
		if err != nil || got.Decision != want {
			t.Fatalf("got %v, %v; want %s", got, err, want)
		}
		// A copy of the trusted guard is the same definition; an empty one
		// replaces it, which the load rejects.
		if _, err := Deploy.Load(policy.MapFS(map[string]string{"root.sigil": root, "guard.sigil": guard}), "p", policy.Require("guard", policy.From(trusted))); err != nil {
			t.Fatalf("a copy of the trusted policy: %v", err)
		}
		if _, err := Deploy.Load(policy.MapFS(map[string]string{"root.sigil": root, "guard.sigil": "policy guard: DeployApproval@1\n"}), "p", policy.Require("guard", policy.From(trusted))); err == nil {
			t.Fatal("bundle replaced a trusted policy")
		}
	})
}

func FuzzCollect(f *testing.F) {
	read := policy.NewDecision[policy.None]("read", "member")
	write := policy.NewDecision[policy.None]("write", "owner")
	f.Add(true, true, false, uint8(2))
	f.Add(false, false, true, uint8(0))
	f.Fuzz(func(t *testing.T, member, owner, exclusive bool, duplicates uint8) {
		opts := []policy.Option{policy.WithVersion(1), policy.WithCollect(read, write)}
		if exclusive {
			opts = append(opts, policy.WithExclusive(read, write))
		}
		k := policy.NewKind[policy.None]("Roles", opts...)
		src := fmt.Sprintf("policy p: Roles@1\nwhen %t { read(reason: member) }\nwhen %t { write(reason: owner) }\n", member, owner)
		src += strings.Repeat(fmt.Sprintf("when %t { read(reason: member) }\n", member), int(duplicates%16))
		p, err := k.Compile(src, "p")
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Eval(context.Background(), policy.None{})
		if exclusive && member && owner {
			var conflict *policy.ConflictError
			if !errors.As(err, &conflict) || len(got.Outcome) != 0 {
				t.Fatalf("expected conflict and empty fallback, got %v, %v", got, err)
			}
			return
		}
		count := 0
		if member {
			count++
		}
		if owner {
			count++
		}
		if err != nil || len(got.Outcome) != count {
			t.Fatalf("expected %d folded outcomes, got %v, %v", count, got, err)
		}
	})
}
