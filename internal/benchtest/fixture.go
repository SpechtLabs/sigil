// Package benchtest supplies deterministic workloads for layer benchmarks.
// CI copies this package and *_bench_test.go files to both revisions.
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

// Input includes scalar, list and map fields exercised by the workloads.
type Input struct {
	Labels  map[string]string `policy:"labels"`
	Actor   string            `policy:"actor"`
	Roles   []string          `policy:"roles"`
	Scores  []int64           `policy:"scores"`
	Enabled bool              `policy:"enabled"`
}

// Payload is the typed decision data returned to a host.
type Payload struct {
	TTL time.Duration `policy:"ttl,default=1h"`
}

// Value returns independent input data for a successful evaluation.
func Value() Input {
	return Input{Actor: "ada", Roles: []string{"reader", "deployer"}, Scores: []int64{1, 2, 3, 4, 5, 6, 7, 8}, Labels: map[string]string{"team": "payments", "region": "eu"}, Enabled: true}
}

// WithOptions builds the same contract through the host binding layer.
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

// Kind builds a validated kind and its Go bindings outside a timed loop.
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

// Policy grows the number of evaluated rules without changing their shape.
// Distinct payloads also exercise collecting and conflicting outcomes.
func Policy(rules int) string {
	var s strings.Builder
	s.WriteString("policy main: Bench@1\nassert(\"named\", actor != \"\")\n")
	for i := range rules {
		fmt.Fprintf(&s, "when %s { allow(member, ttl: %dm) }\n", Expression, i+1)
	}
	return s.String()
}

// Composed reads a shared import and invokes a parameterized policy twice.
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

// Compile prepares an immutable policy for evaluation benchmarks.
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
