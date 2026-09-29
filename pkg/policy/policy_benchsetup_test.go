package policy_test

import (
	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// benchmarkKind is the kind the benchmarks compile against. It lives in a
// *_benchsetup_test.go file on purpose: a baseline comparison copies the
// *_bench_test.go files onto the base revision but leaves this one with its
// revision, so each side builds the kind with its own API. A change to the
// options' signatures then touches only this file, and the comparison still
// runs identical workloads on both sides.
func benchmarkKind(collect bool) *policy.Kind[benchtest.Input] {
	deny := policy.NewDecision[policy.None]("deny", "fallback", "blocked")
	allow := policy.NewDecision[benchtest.Payload]("allow", "member", "oncall")
	opts := []policy.Option{policy.WithVersion(1)}
	if collect {
		opts = append(opts, policy.WithCollect(deny, allow))
	} else {
		opts = append(opts, policy.WithDecisions(deny, allow), policy.WithDefault(deny, "fallback"))
	}
	return policy.NewKind[benchtest.Input]("Bench", opts...)
}
