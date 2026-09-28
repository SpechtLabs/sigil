// Package check implements the `sigil check` command.
package check

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

// NewCommand returns the check command.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "check PATH...",
		SuggestFor: []string{"validate", "verify", "lint"},
		Short:      "Check policies against a kind and lint them",
		Long: `Parses and type-checks policies and modules against a kind, resolves imports
and policy invocations, detects let, import and invocation cycles, compiles
every policy, and reports lints.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents. All documents from all paths are checked together as one bundle,
indexed by the names in their headers, so a name defined twice is an error. A
directory contributes the .sigil files directly inside it, or every one below
it with --recursive.

--require names a policy that every root policy must invoke unconditionally,
the same check a host makes with policy.Require. Repeat it to require several.
--trusted reads required policies, and everything they use, from separate paths,
the same way policy.From does; the bundle may then not define any name the
trusted paths define. The roots are the policies matching --policy, a name or a
pattern such as 'payments.*'. Without --policy they are every bundle policy that
no other policy invokes, apart from required ones; CI should name its roots.

A kind document among the inputs must match the --kind file exactly, which
catches a stale export. A policy whose params have no defaults is checked with
the params unbound, the way explain shows it.

Lints are warnings unless the repository's sigil.yaml promotes them to errors
or turns them off. check reads the nearest sigil.yaml at or above the working
directory, or the file --config names:

  lints:
    gated-deny: error
    qualified-imports: warn

check needs only the kind file, not implementations of the host functions it
declares, so it is the command a policy repository runs in CI. It exits
non-zero when there is an error, including a lint set to error.`,
		Example: `# Type-check one policy
sigil check --kind deploy_approval.sigil deploy/production.sigil

# Check every document in a policy repository, as CI would
sigil check --kind deploy_approval.sigil --recursive .

# Check the bundle a kustomize ConfigMap is built from
sigil check --kind deploy_approval.sigil deploy/*.sigil payments/*.sigil

# Check that every team policy invokes the guardrails unconditionally
sigil check --kind deploy_approval.sigil --require deploy.guardrails --trusted deploy/ --policy 'payments.*' payments/`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFile, _ := cmd.Flags().GetString("kind")
			recursive, _ := cmd.Flags().GetBool("recursive")
			patterns, _ := cmd.Flags().GetStringSlice("policy")
			trusted, _ := cmd.Flags().GetStringSlice("trusted")
			requires, _ := cmd.Flags().GetStringSlice("require")
			configFile, _ := cmd.Flags().GetString("config")
			src := project.Sources{Paths: args, Trusted: trusted, Recursive: recursive, Stdin: cmd.InOrStdin()}
			return run(cmd.OutOrStdout(), o, configFile, kindFile, src, patterns, requires)
		},
	}

	addFlags(cmd)
	return cmd
}

// addFlags declares the check command's flags.
func addFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("kind", "k", "", "Kind file to check the policies against; optional in a binary with the kind linked in")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	cmd.Flags().StringSliceP("policy", "p", nil, "Root policy name or pattern for --require checks, such as 'payments.*' (repeatable)")
	cmd.Flags().StringSlice("trusted", nil, "File or directory to read required policies from, as policy.From does (repeatable)")
	cmd.Flags().StringSlice("require", nil, "Policy that every checked policy must invoke unconditionally (repeatable)")
	cmd.Flags().String("config", "", "Configuration file with lint levels; the nearest "+config.FileName+" when omitted")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("config", "yaml")
	_ = cmd.RegisterFlagCompletionFunc("require", cobra.NoFileCompletions)
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)
	_ = cmd.MarkFlagFilename("trusted", "sigil")
}

// Diagnostic is one error or lint finding, as JSON and YAML print it.
type Diagnostic struct {
	Severity string `json:"severity" yaml:"severity"` // error or warning
	Lint     string `json:"lint,omitempty" yaml:"lint,omitempty"`
	File     string `json:"file,omitempty" yaml:"file,omitempty"`
	Document string `json:"document,omitempty" yaml:"document,omitempty"`
	Message  string `json:"message" yaml:"message"`
	Help     string `json:"help,omitempty" yaml:"help,omitempty"`
	Line     int    `json:"line,omitempty" yaml:"line,omitempty"`
	Column   int    `json:"column,omitempty" yaml:"column,omitempty"`
}

func run(out io.Writer, o *options, configFile, kindFile string, src project.Sources, patterns, requires []string) humane.Error {
	cfg, err := config.Load(configFile, ".")
	if err != nil {
		return err
	}
	k, err := project.LoadKind(kindFile, o.kinds)
	if err != nil {
		return err
	}
	b, err := k.Bundle(src)
	if err != nil {
		return err
	}
	b.Check()
	errs := b.Errors()
	var findings []lint.Finding
	if errs == nil {
		// Lints read what the checker learned, so they run on a bundle
		// that checks, even when a compile or --require then fails.
		var rs []string
		if len(requires) > 0 {
			if rs, err = roots(b, patterns, requires); err != nil {
				return err
			}
		}
		errs = compileAll(b, rs, requires)
		findings = lint.Run(b, lint.Options{Kind: k.Model, Levels: cfg.Lints, Required: requires})
	}
	return report(out, b, errs, findings, *o.output)
}

// compileAll compiles every policy of the bundle, so errors only a
// compile finds, such as an invocation argument out of its param's
// bounds, fail the check, and checks the roots against --require.
func compileAll(b *bundle.Bundle, roots, requires []string) diag.ErrorList {
	var errs diag.ErrorList
	seen := map[string]bool{}
	add := func(list diag.ErrorList) {
		for _, e := range list {
			key := fmt.Sprintf("%s:%d:%s", e.File, e.Pos.Offset, e.Msg)
			if !seen[key] {
				seen[key] = true
				errs = append(errs, e)
			}
		}
	}
	for _, p := range b.Policies() {
		_, list := b.Compile(p, bundle.Options{Static: true})
		add(list)
	}
	if len(requires) == 0 || errs != nil {
		return errs
	}
	for _, root := range roots {
		_, list := b.Compile(root, bundle.Options{Static: true, Require: requires})
		add(list)
	}
	return errs
}

// roots returns the policies --require applies to: those matching
// --policy, or every bundle policy no other one invokes, apart from the
// required ones.
func roots(b *bundle.Bundle, patterns, requires []string) ([]string, humane.Error) {
	if len(patterns) > 0 {
		return project.Match(b.Policies(), patterns)
	}
	invoked := map[string]bool{}
	for _, d := range b.Documents() {
		if d.Info == nil {
			continue
		}
		for _, target := range d.Info.Invocations {
			invoked[target] = true
		}
	}
	var out []string
	for _, p := range b.Policies() {
		if !invoked[p] && !slices.Contains(requires, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// report prints the errors and findings and fails when there's an error.
func report(out io.Writer, b *bundle.Bundle, errs diag.ErrorList, findings []lint.Finding, format output.Format) humane.Error {
	failed := len(errs) + promoted(findings)
	if format == output.Text {
		return reportText(out, b, errs, findings, failed)
	}
	diags := make([]Diagnostic, 0, len(errs)+len(findings))
	for _, e := range errs {
		diags = append(diags, diagnostic(b, e, "error", ""))
	}
	for _, f := range findings {
		sev := "warning"
		if f.Level == lint.Error {
			sev = "error"
		}
		diags = append(diags, diagnostic(b, f.Error, sev, f.Lint))
	}
	var err error
	if format == output.YAML {
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		err = enc.Encode(diags)
	} else {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		err = enc.Encode(diags)
	}
	if err != nil {
		return humane.Wrap(err, "the diagnostics couldn't be written", "check where the output is going")
	}
	if failed > 0 {
		return humane.New(fmt.Sprintf("check found %d %s", failed, plural(failed, "error", "errors")), "each diagnostic says where the problem is and how to fix it")
	}
	return nil
}

// reportText prints warnings to out and returns the errors, lints set to
// error among them, as the command's error.
func reportText(out io.Writer, b *bundle.Bundle, errs diag.ErrorList, findings []lint.Finding, failed int) humane.Error {
	var warnings []*diag.Error
	for _, f := range findings {
		e := *f.Error
		e.Msg += " [" + f.Lint + "]"
		if f.Level == lint.Error {
			errs = append(errs, &e)
			continue
		}
		e.Msg = "warning: " + e.Msg
		warnings = append(warnings, &e)
	}
	if len(warnings) > 0 {
		if _, err := io.WriteString(out, b.Render(warnings)+"\n"); err != nil {
			return humane.Wrap(err, "the warnings couldn't be written", "check where the output is going")
		}
	}
	if failed == 0 {
		return nil
	}
	advice := "fix the documents above"
	if promoted(findings) > 0 {
		advice = "fix the documents above; a lint set to error in sigil.yaml fails the check like any other error"
	}
	return humane.New(b.Render(errs), advice)
}

// promoted counts the findings of lints set to error.
func promoted(findings []lint.Finding) int {
	n := 0
	for _, f := range findings {
		if f.Level == lint.Error {
			n++
		}
	}
	return n
}

func diagnostic(b *bundle.Bundle, e *diag.Error, severity, lintName string) Diagnostic {
	d := Diagnostic{Severity: severity, Lint: lintName, File: e.File, Message: e.Msg, Help: e.Help, Document: e.Doc}
	if e.Pos.IsValid() {
		d.Line, d.Column = e.Pos.Line, e.Pos.Column
		if d.Document == "" {
			d.Document = b.DocumentAt(e.File, e.Pos)
		}
	}
	return d
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
