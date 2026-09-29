// Package benchtest supplies the deterministic workloads the engine's
// benchmarks share: the Bench@1 kind, an input every rule matches, and
// policies that grow by rule count or compose through imports and
// invocations.
//
// `mise run bench -- --baseline <rev>`, which the CI benchmark job runs,
// copies this package and the current *_bench_test.go files onto the
// baseline revision, so both revisions run the same workloads. The
// package must therefore still build against the baseline's internal
// packages. The numbers these workloads produce are reported at
// https://sigil.specht-labs.de/reference/performance/.
package benchtest

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Input is the Bench@1 kind's input, with the scalar, list and map fields
// the workloads read.
type Input struct {
	Labels  map[string]string `policy:"labels"`
	Actor   string            `policy:"actor"`
	Roles   []string          `policy:"roles"`
	Scores  []int64           `policy:"scores"`
	Enabled bool              `policy:"enabled"`
}

// Payload is the typed payload of the `allow` decision.
type Payload struct {
	TTL time.Duration `policy:"ttl,default=1h"`
}

// Value returns an input on which [Expression] holds and the input
// asserts of [Policy] and [Composed] pass, so every rule fires. Each call
// returns fresh slices and maps.
func Value() Input {
	return Input{Actor: "ada", Roles: []string{"reader", "deployer"}, Scores: []int64{1, 2, 3, 4, 5, 6, 7, 8}, Labels: map[string]string{"team": "payments", "region": "eu"}, Enabled: true}
}

// WithOptions returns the gokind options that declare Bench@1: decisions
// `deny` and `allow`, ranked in that order with deny(fallback) as the
// default when collect is false, and `collect all` with no default when
// it's true.
func WithOptions(collect bool) gokind.Options {
	o := gokind.Options{Name: "Bench", Version: 1, Input: reflect.TypeFor[Input](), Ranked: !collect, Collect: collect,
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: reflect.TypeFor[struct{}](), Reasons: []string{"fallback", "blocked"}},
			{Name: "allow", Payload: reflect.TypeFor[Payload](), Reasons: []string{"member", "oncall"}},
		}}
	if !collect {
		o.Default = &gokind.Default{Decision: "deny", Reason: "fallback"}
	}
	return o
}

// Kind builds the Bench@1 kind and its Go binding from [WithOptions],
// failing t on any error. Call it outside the timed loop.
func Kind(t testing.TB, collect bool) (*kind.Kind, *gokind.Binding) {
	t.Helper()
	k, b, errs := gokind.Build(WithOptions(collect))
	if errs != nil {
		t.Fatal(errs)
	}
	return k, b
}

// Expression traverses each input shape and a quantified list.
const Expression = `enabled and actor != "" and "deployer" in roles and labels["team"] == "payments" and all n in scores: n >= 0`

// Policy returns the source of a policy `main` with one input assert and
// rules copies of one `when` block over [Expression], so the number of
// evaluated rules grows without their shape changing. Each copy allows
// with a distinct ttl, so a collecting kind returns every one and a
// `collect one` kind, from two rules up, ends in a conflict.
func Policy(rules int) string {
	var s strings.Builder
	s.WriteString("policy main: Bench@1\nassert(\"named\", actor != \"\")\n")
	for i := range rules {
		fmt.Fprintf(&s, "when %s { allow(member, ttl: %dm) }\n", Expression, i+1)
	}
	return s.String()
}

// Composed is a bundle of three documents whose root `main` reads a pub
// let through an import and invokes a parameterized policy twice, once
// under a `when`, and checks an input assert and an outcome assert. The
// two invocations allow with different ttls, so a collecting kind returns
// both and a `collect one` kind ends in a conflict.
const Composed = `module common: Bench@1
pub let member = "deployer" in roles
---
policy grant: Bench@1
use common.{member}
param ttl: duration
when enabled and member { allow(member, ttl: ttl) }
---
policy main: Bench@1
use grant
assert("named", actor != "")
grant(ttl: 1h)
when labels["team"] == "payments" { grant(ttl: 2h) }
assert("granted", not enabled or allow in outcome)
`

// Compile compiles the policy `main` from source, one file of documents,
// against Bench@1 with its binding, failing t on any diagnostic. The
// policy is immutable, so parallel benchmarks can share it. Call it
// outside the timed loop.
func Compile(t testing.TB, source string, collect bool) *eval.Policy {
	t.Helper()
	k, b := Kind(t, collect)
	tree := bundle.New(k)
	tree.Add("bench.sigil", []byte(source))
	p, errs := tree.Compile("main", bundle.Options{Binding: b})
	if errs != nil {
		t.Fatal(errs)
	}
	return p
}
