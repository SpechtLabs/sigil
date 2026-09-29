// Package explain implements the `sigil explain` command. It compiles each
// selected policy of a bundle without an input and flattens it into every
// rule and assert it can reach, each with the `when` conditions and the
// chain of invocations that lead to it. [Explanation] and [Entry] are the
// records it prints as JSON and YAML.
package explain

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/eval"
)

// NewCommand returns the explain command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] every run needs
// --kind.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "explain PATH...",
		SuggestFor: []string{"flatten", "expand", "show"},
		Short:      "Flatten a policy into the guarded decisions it can produce",
		Long: `Flattens a policy into one list of guarded decisions. Every policy invocation
is inlined, the when blocks around it are pushed down into each of the invoked
rules' conditions, and params show as the values they are bound to. The result
answers what a policy actually does without reading every document it invokes.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents. All documents from all paths form one bundle, indexed by the names in
their headers. A directory contributes the .sigil files directly inside it, or
every one below it with --recursive. --policy names the policy to explain, or
a pattern such as 'payments.*' to explain several; without it, explain explains
every policy in the bundle, one after another.

A policy is explained with its params as declared, so a policy whose params
have no defaults is explained only through the policies that invoke it.

explain needs only the kind file, not implementations of the host functions it
declares.`,
		Example: `# List every decision a team policy can produce, and under which conditions
sigil explain --kind deploy_approval.sigil --policy payments.production deploy/ payments/

# Review every policy in a ConfigMap's bundle at once
sigil explain --kind deploy_approval.sigil policies.sigil`,
		Args:              usage.AtLeast(1, "PATH"),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFile, _ := cmd.Flags().GetString("kind")
			pattern, _ := cmd.Flags().GetString("policy")
			recursive, _ := cmd.Flags().GetBool("recursive")
			src := project.Sources{Paths: args, Recursive: recursive, Stdin: cmd.InOrStdin()}
			return run(cmd.OutOrStdout(), o, kindFile, pattern, src)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against; optional in a binary with the kind linked in")
	cmd.Flags().StringP("policy", "p", "", "Name or pattern of the policies to explain; every policy in the bundle when omitted")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)

	return cmd
}

// Explanation is one policy flattened: every rule and assert it can
// reach, with the conditions and call chain of each. Policies counts the
// policy itself and every one it invokes, Modules every module they use.
type Explanation struct {
	Policy   string  `json:"policy" yaml:"policy"`
	Policies int     `json:"policies" yaml:"policies"`
	Modules  int     `json:"modules" yaml:"modules"`
	Rules    []Entry `json:"rules" yaml:"rules"` // every rule, then every assert
}

// Entry is one rule or assert of an explanation.
type Entry struct {
	Kind       string   `json:"kind" yaml:"kind"`                             // "decision" or "assert"
	Decision   string   `json:"decision,omitempty" yaml:"decision,omitempty"` // the decision a rule returns; empty for an assert
	Reason     string   `json:"reason" yaml:"reason"`                         // a rule's reason, or an assert's name
	Phase      string   `json:"phase,omitempty" yaml:"phase,omitempty"`       // input or outcome, for an assert
	Chain      []string `json:"chain" yaml:"chain"`                           // policy:line, outermost call first, the rule last
	Conditions []string `json:"conditions" yaml:"conditions"`                 // every `when` on the way, outermost first
	Check      string   `json:"check,omitempty" yaml:"check,omitempty"`       // an assert's own condition
	Payload    []string `json:"payload,omitempty" yaml:"payload,omitempty"`   // a rule's payload arguments, as `name = expression`
}

func run(out io.Writer, o *options, kindFile, pattern string, src project.Sources) humane.Error {
	k, err := project.LoadKind(kindFile, o.kinds)
	if err != nil {
		return err
	}
	b, err := k.Bundle(src)
	if err != nil {
		return err
	}
	b.Check()
	if errs := b.Errors(); errs != nil {
		return pretty.Diagnose(b.Resolve(errs), b.SourceOf, "the bundle doesn't check, so nothing was explained", "fix the errors above; sigil check reports every problem in a bundle at once")
	}
	roots, serr := selectRoots(b.Policies(), pattern)
	if serr != nil {
		return serr
	}
	var explanations []Explanation
	for _, root := range roots {
		prog, errs := b.Compile(root, bundle.Options{Static: true})
		if errs != nil {
			return pretty.Diagnose(b.Resolve(errs), b.SourceOf, "policy "+root+" doesn't compile, so it wasn't explained", "fix the errors above; sigil check reports every problem in a bundle at once")
		}
		explanations = append(explanations, explain(prog, b))
	}
	var werr error
	switch *o.output {
	case output.JSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		werr = enc.Encode(explanations)
	case output.YAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		werr = enc.Encode(explanations)
	}
	if werr != nil {
		return humane.Wrap(werr, "the explanation couldn't be written", "check where the output is going")
	}
	if *o.output != output.Text {
		return nil
	}
	p := pretty.New(out)
	var text strings.Builder
	for i, e := range explanations {
		if i > 0 {
			text.WriteString("\n")
		}
		writeText(&text, p.Theme(), e)
	}
	return p.Print(text.String())
}

// selectRoots picks the policies to explain: every one, the one named,
// or the ones matching a pattern, where `*` matches any run of characters.
func selectRoots(policies []string, pattern string) ([]string, humane.Error) {
	if pattern == "" {
		if len(policies) == 0 {
			return nil, humane.New("the bundle holds no policies", "name a file or directory that holds one")
		}
		return policies, nil
	}
	return project.Match(policies, []string{pattern})
}

// Entry kinds.
const (
	kindDecision = "decision"
	kindAssert   = "assert"
)

// explain flattens a compiled policy, looking up in b whether each
// document it compiled in is a policy or a module.
func explain(p *eval.Policy, b *bundle.Bundle) Explanation {
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
		entry := Entry{Kind: kindDecision, Decision: r.Decision.Name, Reason: r.Reason, Chain: chain(r.Chain, r.Policy, r.Pos.Line), Conditions: conds(r.Conds)}
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
		e.Rules = append(e.Rules, Entry{Kind: kindAssert, Reason: a.Reason, Phase: phase, Chain: chain(a.Chain, a.Policy, a.Pos.Line), Conditions: conds(a.Conds), Check: a.Text})
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

// writeText prints an explanation in the layout the documentation shows,
// styled by t: each rule as it's written in a policy, `deny(reason)`,
// with the conditions it fires under, its payload, and the call chain
// that reaches it.
//
//	payments.production: 9 rules from 3 policies and 1 module
//
//	  deny(not_eligible)           payments.production:7 → deploy.guardrails:8
//	    when not eligible
//
//	  review(service_owner)        payments.production:10 → deploy.production:16
//	    when service.labels["compliance"] == "pci"
//	     and cleared
//	    with approvers = ["payments-leads", "security-leads"]
//
//	  assert named_actor (input)   payments.production:21
//	    check actor.name != ""
func writeText(b *strings.Builder, t pretty.Theme, e Explanation) {
	summary := count(len(e.Rules), "rule", "rules") + " from " + count(e.Policies, "policy", "policies")
	if e.Modules > 0 {
		summary += " and " + count(e.Modules, "module", "modules")
	}
	fmt.Fprintf(b, "%s: %s\n", t.Accent(e.Policy), t.Bold(summary))
	heads := make([]string, len(e.Rules))
	width := 0
	for i, r := range e.Rules {
		heads[i] = head(r)
		width = max(width, len(heads[i]))
	}
	for i, r := range e.Rules {
		b.WriteString("\n")
		h := fmt.Sprintf("%-*s", width, heads[i])
		if r.Kind == kindAssert {
			h = t.Warn(h)
		} else {
			h = t.Bold(h)
		}
		fmt.Fprintf(b, "  %s  %s\n", h, t.Location(strings.Join(r.Chain, " → ")))
		if len(r.Conditions) == 0 && r.Kind == kindDecision {
			b.WriteString("    " + t.Muted("always") + "\n")
		}
		writeConditions(b, t, "    ", r.Conditions)
		if r.Check != "" {
			b.WriteString("    " + t.Muted("check") + " " + r.Check + "\n")
		}
		for _, p := range r.Payload {
			name, value, _ := strings.Cut(p, " = ")
			b.WriteString("    " + t.Muted("with") + " " + t.Key(name+" =") + " " + value + "\n")
		}
	}
}

// count renders n with the noun in singular or plural: `1 rule`, `9 rules`.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// head names a rule the way a policy writes it: `deny(not_eligible)`,
// or `assert named_actor (input)`.
func head(r Entry) string {
	if r.Kind == kindAssert {
		return "assert " + r.Reason + " (" + r.Phase + ")"
	}
	return r.Decision + "(" + r.Reason + ")"
}

// writeConditions writes the conditions a rule fires under, `when` the
// first and `and` each one after, aligned on the expressions.
func writeConditions(b *strings.Builder, t pretty.Theme, indent string, conds []string) {
	for i, c := range conds {
		if i == 0 {
			b.WriteString(indent + t.Muted("when") + " " + c + "\n")
			continue
		}
		b.WriteString(indent + " " + t.Muted("and") + " " + c + "\n")
	}
}
