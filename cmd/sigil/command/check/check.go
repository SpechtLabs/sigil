// Package check implements the `sigil check` command. It parses,
// type-checks and compiles a bundle against a kind, checks the root
// policies against --require, runs the lints at the levels sigil.yaml
// sets, and prints the diagnostics with a summary line. As JSON or YAML it
// prints one [output.Diagnostic] per problem. The command fails when any
// diagnostic is an error, including a lint set to error.
package check

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

// NewCommand returns the check command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] every run needs
// --kind.
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
		Args:              usage.AtLeast(1, "PATH"),
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

func run(out io.Writer, o *options, configFile, kindFile string, src project.Sources, patterns, requires []string) humane.Error {
	cfg, err := config.Load(configFile, ".")
	if err != nil {
		return err
	}
	k, err := project.LoadKind(kindFile, o.kinds)
	if d, ok := errors.AsType[*pretty.Diagnostics](err); ok {
		return reportKind(out, d, *o.output)
	}
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

// report prints the diagnostics, then a summary, and fails when there's
// an error: a compiler error, or a lint the configuration set to error.
func report(out io.Writer, b *bundle.Bundle, errs diag.ErrorList, findings []lint.Finding, format output.Format) humane.Error {
	diags := b.Resolve(collect(errs, findings))
	failed := 0
	for _, d := range diags {
		if d.Severity == diag.SeverityError {
			failed++
		}
	}
	if format == output.Text {
		return reportText(out, b, diags, failed)
	}
	records := make([]output.Diagnostic, 0, len(diags))
	for _, d := range diags {
		records = append(records, output.NewDiagnostic(d))
	}
	if err := output.Encode(out, format, records); err != nil {
		return err
	}
	if failed > 0 {
		return pretty.Fail(fmt.Sprintf("check found %s", problems(failed, len(diags)-failed)), "each diagnostic says where the problem is and how to fix it")
	}
	return nil
}

// collect merges the errors and the findings into one list of
// diagnostics; a finding's diagnostic already carries its lint's name and
// level.
func collect(errs diag.ErrorList, findings []lint.Finding) diag.ErrorList {
	out := make(diag.ErrorList, 0, len(errs)+len(findings))
	out = append(out, errs...)
	for _, f := range findings {
		out = append(out, f.Error)
	}
	return out
}

// reportText prints the diagnostics in file and position order, then one
// line that sums the check up:
//
//	✓ checked 3 files, no problems found
//	! checked 3 files, 2 warnings
//	✗ checked 3 files, 1 error and 2 warnings
func reportText(out io.Writer, b *bundle.Bundle, diags diag.ErrorList, failed int) humane.Error {
	p := pretty.New(out)
	if err := p.Diagnostics(diags, b.SourceOf); err != nil {
		return err
	}
	if len(b.Sources) == 0 && failed == 0 {
		return p.Warning("no .sigil files found, so nothing was checked", "a directory contributes the files directly inside it; pass --recursive to include its subdirectories")
	}
	files := fmt.Sprintf("checked %d %s, ", len(b.Sources), plural(len(b.Sources), "file", "files"))
	warnings := len(diags) - failed
	switch {
	case failed > 0:
		if err := p.Fail(files + problems(failed, warnings)); err != nil {
			return err
		}
		return pretty.Fail(fmt.Sprintf("check found %s", problems(failed, warnings)), "each diagnostic above says where the problem is and how to fix it")
	case warnings > 0:
		return p.Warning(files + problems(failed, warnings))
	}
	return p.Ok(files + "no problems found")
}

// problems spells `1 error`, `2 warnings` or `1 error and 2 warnings`.
func problems(errs, warnings int) string {
	var parts []string
	if errs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", errs, plural(errs, "error", "errors")))
	}
	if warnings > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", warnings, plural(warnings, "warning", "warnings")))
	}
	return strings.Join(parts, " and ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// reportKind reports a kind file that doesn't load the way report does a
// bundle's diagnostics: rendered with their source lines and help, or as
// records, and a failure.
func reportKind(out io.Writer, d *pretty.Diagnostics, format output.Format) humane.Error {
	n := len(d.Errs)
	failed := pretty.Fail(fmt.Sprintf("the kind file has %s", problems(n, 0)), "fix the kind file, or regenerate it from the host's Schema(); `sigil fmt --write` rewrites the old decision syntax")
	if format != output.Text {
		records := make([]output.Diagnostic, 0, n)
		for _, e := range d.Errs {
			records = append(records, output.NewDiagnostic(e))
		}
		if err := output.Encode(out, format, records); err != nil {
			return err
		}
		return failed
	}
	p := pretty.New(out)
	if err := p.Diagnostics(d.Errs, d.Src); err != nil {
		return err
	}
	if err := p.Fail("the kind file doesn't load, so nothing was checked"); err != nil {
		return err
	}
	return failed
}
