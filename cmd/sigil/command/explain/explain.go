// Package explain implements the `sigil explain` command.
package explain

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
)

// NewCommand returns the explain command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
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
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFile, _ := cmd.Flags().GetString("kind")
			pattern, _ := cmd.Flags().GetString("policy")
			recursive, _ := cmd.Flags().GetBool("recursive")
			format := output.Text
			if f := cmd.Flags().Lookup("output"); f != nil {
				format = output.Format(f.Value.String())
			}
			return run(cmd.OutOrStdout(), cmd.InOrStdin(), kindFile, pattern, recursive, args, format)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against (required)")
	cmd.Flags().StringP("policy", "p", "", "Name or pattern of the policies to explain; every policy in the bundle when omitted")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("kind")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)

	return cmd
}

// Explanation is one policy flattened: every rule and assert it can
// reach, with the conditions and call chain of each.
type Explanation struct {
	Policy    string  `json:"policy"`
	Documents int     `json:"documents"`
	Rules     []Entry `json:"rules"`
}

// Entry is one rule or assert of an explanation.
type Entry struct {
	Kind       string   `json:"kind"` // "decision" or "assert"
	Decision   string   `json:"decision,omitempty"`
	Reason     string   `json:"reason"`
	Phase      string   `json:"phase,omitempty"` // input or outcome, for an assert
	Chain      []string `json:"chain"`           // policy:line, outermost call first, the rule last
	Conditions []string `json:"conditions"`      // every `when` on the way, outermost first
	Check      string   `json:"check,omitempty"` // an assert's own condition
	Payload    []string `json:"payload,omitempty"`
}

func run(out io.Writer, stdin io.Reader, kindFile, pattern string, recursive bool, paths []string, format output.Format) error {
	src, err := os.ReadFile(kindFile) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return humane.Wrap(err, "the kind file couldn't be read", "pass the exported kind file with --kind")
	}
	k, errs := check.LoadKind(kindFile, src)
	if errs != nil {
		return humane.New(errs.Error(), "fix the kind file, or regenerate it from the host's Schema()")
	}
	b := bundle.New(k)
	if err := b.LoadPaths(paths, recursive, stdin); err != nil {
		return err
	}
	b.Check()
	if errs := b.Errors(); errs != nil {
		return humane.New(b.Render(errs), "fix the documents above; explain needs a bundle that checks")
	}
	roots, serr := select_(b.Policies(), pattern)
	if serr != nil {
		return serr
	}
	var explanations []Explanation
	for _, root := range roots {
		prog, errs := b.Compile(root, bundle.Options{Static: true})
		if errs != nil {
			return humane.New(b.Render(errs), "fix the documents above; explain needs a policy that compiles")
		}
		explanations = append(explanations, explain(prog))
	}
	switch format {
	case output.JSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(explanations)
	case output.YAML:
		return humane.New("YAML output isn't supported by explain yet", "use -o text or -o json")
	}
	var text strings.Builder
	for i, e := range explanations {
		if i > 0 {
			text.WriteString("\n")
		}
		writeText(&text, e)
	}
	if _, err := io.WriteString(out, text.String()); err != nil {
		return humane.Wrap(err, "the explanation couldn't be written", "check where the output is going")
	}
	return nil
}

// select_ picks the policies to explain: every one, the one named, or
// the ones matching a pattern, where `*` matches any run of characters.
func select_(policies []string, pattern string) ([]string, error) {
	if pattern == "" {
		if len(policies) == 0 {
			return nil, humane.New("the bundle holds no policies", "name a file or directory that holds one")
		}
		return policies, nil
	}
	re := regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$")
	var out []string
	for _, p := range policies {
		if re.MatchString(p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, humane.New(fmt.Sprintf("no policy matches %q", pattern), "the bundle defines: "+strings.Join(policies, ", "))
	}
	return out, nil
}

// Entry kinds.
const (
	kindDecision = "decision"
	kindAssert   = "assert"
)

// explain flattens a compiled policy.
func explain(p *eval.Policy) Explanation {
	if p == nil {
		return Explanation{}
	}
	names := shortNames(p.Instances())
	e := Explanation{Policy: p.Name, Documents: len(p.Instances())}
	for _, r := range p.Rules() {
		entry := Entry{Kind: kindDecision, Decision: r.Decision.Name, Reason: r.Reason, Chain: chain(names, r.Chain, r.Policy, r.Pos.Line), Conditions: conds(r.Conds)}
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
		e.Rules = append(e.Rules, Entry{Kind: kindAssert, Reason: a.Reason, Phase: phase, Chain: chain(names, a.Chain, a.Policy, a.Pos.Line), Conditions: conds(a.Conds), Check: a.Text})
	}
	return e
}

// shortNames names each document by its last segment, or its full name
// when another document shares the segment.
func shortNames(docs []string) map[string]string {
	count := map[string]int{}
	for _, d := range docs {
		count[path.Ext("." + d)[1:]]++
	}
	out := map[string]string{}
	for _, d := range docs {
		short := path.Ext("." + d)[1:]
		if count[short] > 1 {
			short = d
		}
		out[d] = short
	}
	return out
}

// chain renders a call chain as `payments:7 → guardrails:8`.
func chain(names map[string]string, sites []eval.Site, policy string, line int) []string {
	out := make([]string, 0, len(sites)+1)
	for _, s := range sites {
		out = append(out, fmt.Sprintf("%s:%d", short(names, s.Policy), s.Pos.Line))
	}
	return append(out, fmt.Sprintf("%s:%d", short(names, policy), line))
}

func short(names map[string]string, doc string) string {
	if s, ok := names[doc]; ok {
		return s
	}
	return doc
}

func conds(cs []*eval.Cond) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out
}

// writeText prints an explanation in the layout the documentation shows.
func writeText(b *strings.Builder, e Explanation) {
	docs := "policy"
	if e.Documents != 1 {
		docs = "policies"
	}
	fmt.Fprintf(b, "%s: %d rules from %d %s\n", e.Policy, len(e.Rules), e.Documents, docs)
	for _, r := range e.Rules {
		b.WriteString("\n")
		head := r.Decision
		reason := r.Reason
		if r.Kind == kindAssert {
			head = kindAssert
			reason += " (" + r.Phase + ")"
		}
		fmt.Fprintf(b, "%-8s %-17s %s\n", head, reason, strings.Join(r.Chain, " → "))
		if len(r.Conditions) == 0 && r.Kind == kindDecision {
			b.WriteString("         always\n")
		}
		for i, c := range r.Conditions {
			if i > 0 {
				c = "and " + c
			}
			b.WriteString("         " + c + "\n")
		}
		if r.Check != "" {
			b.WriteString("         check " + r.Check + "\n")
		}
		for _, p := range r.Payload {
			b.WriteString("         " + p + "\n")
		}
	}
}
