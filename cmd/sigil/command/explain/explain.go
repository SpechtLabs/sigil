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
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// NewCommand returns the explain command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] the kinds come from
// the paths and --kind.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}
	if o.payload != nil {
		return newCompiledCommand(o)
	}

	cmd := &cobra.Command{
		Use:        "explain [PATH...]",
		SuggestFor: []string{"flatten", "expand", "show"},
		Short:      "Flatten a policy into the guarded decisions it can produce",
		Long: `Flattens a policy into one list of guarded decisions. Every policy invocation
is inlined, the when blocks around it are pushed down into each of the invoked
rules' conditions, and params show as the values they are bound to. The result
answers what a policy actually does without reading every document it invokes.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents; with no PATH, explain reads the current directory. A directory
contributes every .sigil file below it. All documents from all paths form one
bundle, indexed by the names in their headers. --policy names the policy to explain, or
a pattern such as 'payments.*' to explain several; without it, explain explains
every policy in the bundle, one after another.

Each document's header names its kind, and the kind is found among the
inputs: a kind file among the paths, or a kind document in the same file as the
policies. --kind adds a kind file the paths don't hold, and so does the kinds:
list of the configuration file (the nearest sigil.yaml, sigil.json or
sigil.toml, or the file --config names); a host binary has its kinds linked in.
The same kind from two sources must be identical, which catches a stale
export. The configuration's require: trusted: paths are read too, so a policy
finds the required policies it uses.

A policy is explained with its params as declared, so a policy whose params
have no defaults is explained only through the policies that invoke it.

explain needs only the kind files, not implementations of the host functions it
declares.`,
		Example: `# List every decision a team policy can produce, and under which conditions
sigil explain --policy payments.production deploy_approval.sigil deploy/ payments/

# Review every policy in a ConfigMap's bundle against a kind file kept elsewhere
sigil explain --kind deploy_approval.sigil policies.sigil`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFiles, _ := cmd.Flags().GetStringSlice("kind")
			pattern, _ := cmd.Flags().GetString("policy")
			configFile, _ := cmd.Flags().GetString("config")
			src := project.Sources{Paths: args, Kinds: kindFiles, Stdin: cmd.InOrStdin()}
			return run(cmd.OutOrStdout(), o, configFile, pattern, src)
		},
	}

	cmd.Flags().StringSliceP("kind", "k", nil, "Kind file the paths don't hold; the policies' kinds are found among the paths and the kinds linked in (repeatable)")
	cmd.Flags().StringP("policy", "p", "", "Name or pattern of the policies to explain; every policy in the bundle when omitted")
	cmd.Flags().String("config", "", "Configuration file with kind files and trusted paths to load; the nearest "+config.Names+" when omitted")
	// -R read subdirectories before every command did; it stays so scripts
	// that pass it keep working.
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories too; always on")
	_ = cmd.Flags().MarkDeprecated("recursive", "directories are always read recursively")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("config", "yaml")
	_ = cmd.RegisterFlagCompletionFunc("policy", complete.Policies)

	return cmd
}

type (
	// Explanation is one policy flattened, as JSON and YAML print it.
	Explanation = workspace.Explanation
	// Entry is one rule or assert of an explanation.
	Entry = workspace.Rule
)

// run explains the policies pattern matches, or every policy without it,
// in the project src names, with the kind files and trusted paths of the
// configuration configFile names or the nearest configuration file.
func run(out io.Writer, o *options, configFile, pattern string, src project.Sources) humane.Error {
	if len(src.Paths) == 0 {
		src.Paths = []string{"."}
	}
	if err := config.Apply(configFile, ".", &src); err != nil {
		return err
	}
	proj, err := project.Load(src, o.kinds)
	if err != nil {
		return err
	}
	return explain(out, o, proj, pattern)
}

// explain explains the policies of proj that pattern matches, or every
// policy without it, and prints the explanations. Only those policies
// and what they use have to check.
func explain(out io.Writer, o *options, proj *project.Project, pattern string) humane.Error {
	proj.Check()
	roots, serr := selectRoots(proj.Policies(), pattern)
	if serr != nil {
		// Without roots there's no scope, and a policy may be missing
		// because its document or kind doesn't check.
		if errs := proj.Errors(); errs != nil {
			return pretty.Diagnose(proj.Resolve(errs), proj.SourceOf, "the bundle doesn't check, so nothing was explained", "fix the errors above; sigil check reports every problem in a bundle at once")
		}
		return serr
	}
	// Only the roots, what they use and the kinds count: an error in a
	// document no root uses doesn't stop the explanation.
	s := proj.ScopeOf(roots)
	if errs := s.Keep(proj.Errors()); errs != nil {
		return pretty.Diagnose(errs, proj.SourceOf, "the policies to explain don't check, so nothing was explained", "fix the errors above; sigil check reports every problem in a bundle at once")
	}
	var explanations []Explanation
	for _, root := range roots {
		b := s.Bundle(proj.Group(root))
		prog, errs := b.Compile(root, bundle.Options{Static: true})
		if errs != nil {
			return pretty.Diagnose(proj.Resolve(errs), proj.SourceOf, "policy "+root+" doesn't compile, so it wasn't explained", "fix the errors above; sigil check reports every problem in a bundle at once")
		}
		explanations = append(explanations, workspace.Explain(prog, b))
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

// writeText prints an explanation in the layout the documentation shows,
// styled by t: each rule as it's written in a policy,
// `deny(reason: not_eligible)`, with the conditions it fires under, its
// payload, and the call chain that reaches it.
//
//	payments.production: 9 rules from 3 policies and 1 module
//
//	  deny(reason: not_eligible)     payments.production:7 → deploy.guardrails:8
//	    when not eligible
//
//	  review(reason: service_owner)  payments.production:10 → deploy.production:16
//	    when service.labels["compliance"] == "pci"
//	     and cleared
//	    with approvers = ["payments-leads", "security-leads"]
//
//	  assert named_actor (input)     payments.production:21
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
		if r.Kind == workspace.RuleAssert {
			h = t.Warn(h)
		} else {
			h = t.Bold(h)
		}
		fmt.Fprintf(b, "  %s  %s\n", h, t.Location(strings.Join(r.Chain, " → ")))
		if len(r.Conditions) == 0 && r.Kind == workspace.RuleDecision {
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

// head names a rule the way a policy writes it: `deny(reason: not_eligible)`,
// or `assert named_actor (input)`.
func head(r Entry) string {
	if r.Kind == workspace.RuleAssert {
		return "assert " + r.Reason + " (" + r.Phase + ")"
	}
	return r.Decision + "(reason: " + r.Reason + ")"
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
