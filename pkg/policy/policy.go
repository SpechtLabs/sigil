// Package policy is Sigil's Go API: define a kind from Go types, compile
// policies against it, and evaluate them.
//
// It mirrors regexp: a kind is defined once at package level with NewKind,
// which panics on a contract that can't be exported, the way
// regexp.MustCompile panics on a pattern that can't be compiled.
//
//	var Deploy = policy.NewKind[Input]("DeployApproval",
//		policy.WithVersion(1),
//		policy.WithDecisions(Deny, Review, Approve),
//		policy.WithDefault(Deny, "no_rule_matched"),
//		policy.WithFunc("split", strings.Split),
//	)
package policy

import "github.com/spechtlabs/sigil/internal/eval"

// Policy is a compiled policy: immutable, and safe to evaluate from any
// number of goroutines at once. Replacing one at run time is a pointer
// swap.
type Policy[In any] struct {
	kind *Kind[In]
	prog *eval.Policy
	name string
}

// Name returns the policy's name, as its header declares it.
func (p *Policy[In]) Name() string { return p.name }
