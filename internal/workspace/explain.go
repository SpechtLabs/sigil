package workspace

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/eval"
)

// Rule kinds, the values of [Rule.Kind].
const (
	RuleDecision = "decision"
	RuleAssert   = "assert"
)

// Explanation is one policy flattened: every rule and assert it can
// reach, with the conditions and call chain of each. Policies counts the
// policy itself and every one it invokes, Modules every module they use.
type Explanation struct { //nolint:govet // the field order is the JSON's
	Policy   string `json:"policy" yaml:"policy"`
	Policies int    `json:"policies" yaml:"policies"`
	Modules  int    `json:"modules" yaml:"modules"`
	Rules    []Rule `json:"rules" yaml:"rules"` // every rule, then every assert
}

// Rule is one rule or assert of an explanation.
type Rule struct {
	Kind       string   `json:"kind" yaml:"kind"`                             // "decision" or "assert"
	Decision   string   `json:"decision,omitempty" yaml:"decision,omitempty"` // the decision a rule returns; empty for an assert
	Reason     string   `json:"reason" yaml:"reason"`                         // a rule's reason, or an assert's name
	Phase      string   `json:"phase,omitempty" yaml:"phase,omitempty"`       // input or outcome, for an assert
	Chain      []string `json:"chain" yaml:"chain"`                           // policy:line, outermost call first, the rule last
	Conditions []string `json:"conditions" yaml:"conditions"`                 // every `when` on the way, outermost first
	Check      string   `json:"check,omitempty" yaml:"check,omitempty"`       // an assert's own condition
	Payload    []string `json:"payload,omitempty" yaml:"payload,omitempty"`   // a rule's payload arguments, as `name = expression`
}

// Explain flattens a compiled policy into the record `sigil explain`
// prints, looking up in b, the bundle it compiled in, whether each
// document it compiled in is a policy or a module.
func Explain(p *eval.Policy, b *bundle.Bundle) Explanation {
	if p == nil {
		return Explanation{}
	}
	e := Explanation{Policy: p.Name}
	for _, name := range p.Instances() {
		if d := b.Document(name); d != nil && d.Module() {
			e.Modules++
		} else {
			e.Policies++
		}
	}
	for _, r := range p.Rules() {
		entry := Rule{Kind: RuleDecision, Decision: r.Decision.Name, Reason: r.Reason, Chain: chain(r.Chain, r.Policy, r.Pos.Line), Conditions: conds(r.Conds)}
		for _, a := range r.Args {
			entry.Payload = append(entry.Payload, a.Name+" = "+a.Text)
		}
		e.Rules = append(e.Rules, entry)
	}
	for _, a := range p.Asserts() {
		phase := "input"
		if a.ReadsOutcome {
			phase = "outcome"
		}
		e.Rules = append(e.Rules, Rule{Kind: RuleAssert, Reason: a.Reason, Phase: phase, Chain: chain(a.Chain, a.Policy, a.Pos.Line), Conditions: conds(a.Conds), Check: a.Text})
	}
	return e
}

// chain renders a call chain by full document names, as
// `payments.production:7 → deploy.guardrails:8`.
func chain(sites []eval.Site, policy string, line int) []string {
	out := make([]string, 0, len(sites)+1)
	for _, s := range sites {
		out = append(out, fmt.Sprintf("%s:%d", s.Policy, s.Pos.Line))
	}
	return append(out, fmt.Sprintf("%s:%d", policy, line))
}

func conds(cs []*eval.Cond) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out
}
