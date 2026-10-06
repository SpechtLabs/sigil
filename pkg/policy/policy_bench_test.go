package policy_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/pkg/policy"
)

func BenchmarkPolicyCompile(b *testing.B) {
	k := benchmarkKind(false)
	for _, n := range []int{1, 8, 16, 32, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			src := benchtest.Policy(n)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := k.Compile(src, "main"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPolicyEval(b *testing.B) {
	for _, name := range []string{"ranked", "collect", "conflict", "assertion", "fallback", "parallel"} {
		b.Run(name, func(b *testing.B) {
			collect := name == "collect" || name == "parallel"
			src := benchtest.Policy(1)
			if collect || name == "conflict" {
				src = benchtest.Composed
			}
			p, err := benchmarkKind(collect).Compile(src, "main")
			if err != nil {
				b.Fatal(err)
			}
			in := benchtest.Value()
			if name == "assertion" {
				in.Actor = ""
			}
			if name == "fallback" {
				in.Enabled = false
			}
			ctx := context.Background()
			wantErr := name == "conflict" || name == "assertion"
			// Validate the fixture's path before measuring it. Checking error
			// types here keeps that inspection outside the timed operation.
			probe, probeErr := p.Eval(ctx, in)
			if probe == nil {
				b.Fatal("evaluation returned no result")
			}
			switch name {
			case "conflict":
				if _, ok := errors.AsType[*policy.ConflictError](probeErr); !ok {
					b.Fatalf("expected conflict, got %v", probeErr)
				}
			case "assertion":
				if _, ok := errors.AsType[*policy.AssertionError](probeErr); !ok {
					b.Fatalf("expected assertion failure, got %v", probeErr)
				}
			case "collect", "parallel":
				if len(probe.Outcome) != 2 {
					b.Fatalf("expected two collected outcomes, got %v", probe.Outcome)
				}
			case "ranked":
				if probe.Decision != "allow" || probe.Reason != "member" {
					b.Fatalf("expected member allowance, got %v", probe)
				}
			case "fallback":
				if probe.Decision != "deny" || probe.Reason != "fallback" {
					b.Fatalf("expected fallback denial, got %v", probe)
				}
			}
			check := func() {
				res, err := p.Eval(ctx, in)
				if (err != nil) != wantErr || res == nil {
					b.Errorf("evaluation: %v, %v", res, err)
				}
			}
			check()
			b.ReportAllocs()
			if name == "parallel" {
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						check()
					}
				})
				return
			}
			for b.Loop() {
				check()
			}
		})
	}
}

// BenchmarkPolicyEvalRules measures a 64-rule policy through the public
// API, trace included: every rule matching on a collecting kind, and one
// matching on a collecting and on a ranked kind.
func BenchmarkPolicyEvalRules(b *testing.B) {
	const n = 64
	for _, name := range []string{"all-matching", "one-matching", "ranked"} {
		b.Run(fmt.Sprintf("%s/rules=%d", name, n), func(b *testing.B) {
			src, want := benchtest.OneMatching(n), 1
			if name == "all-matching" {
				src, want = benchtest.Policy(n), n
			}
			p, err := benchmarkKind(name != "ranked").Compile(src, "main")
			if err != nil {
				b.Fatal(err)
			}
			in := benchtest.Value()
			ctx := context.Background()
			b.ReportAllocs()
			for b.Loop() {
				res, err := p.Eval(ctx, in)
				if err != nil || len(res.Outcome) != want || len(res.Trace.Candidates) != want {
					b.Fatalf("evaluation: %v, %v", res, err)
				}
			}
		})
	}
}

func BenchmarkCandidateLocation(b *testing.B) {
	c := policy.Candidate{Position: policy.Position{File: "grant.sigil", Document: "grant", Line: 12, Column: 3}, CallChain: []policy.Position{{File: "main.sigil", Document: "main", Line: 7, Column: 1}}}
	b.ReportAllocs()
	for b.Loop() {
		c.Location()
	}
}
