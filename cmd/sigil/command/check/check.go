// Package check implements the `sigil check` command. It parses,
// type-checks and compiles every document against its kind, checks the root
// policies against --require, runs the lints at the levels sigil.yaml
// sets, and prints the diagnostics with a summary line. As JSON or YAML it
// prints one [output.Diagnostic] per problem. The command fails when any
// diagnostic is an error, including a lint set to error.
package check

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

// NewCommand returns the check command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] the kinds come from
// the paths and --kind.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "check [PATH...]",
		SuggestFor: []string{"validate", "verify", "lint"},
		Short:      "Check policies against their kinds and lint them",
		Long: `Parses and type-checks policies and modules against their kinds, resolves
imports and policy invocations, detects let, import and invocation cycles,
compiles every policy, and reports lints.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents; with no PATH, check reads the current directory. A directory
contributes every .sigil file below it. All documents from all paths are
checked together as one bundle, indexed by the names in their headers, so a
name defined twice is an error.

--require names a policy that every root policy must invoke unconditionally,
the same check a host makes with policy.Require. Repeat it to require several.
--trusted reads required policies, and everything they use, from separate paths,
the same way policy.From does; the bundle may then not define any name the
trusted paths define. The roots are the policies matching --policy, a name or a
pattern such as 'payments.*'. Without --policy they are every bundle policy that
no other policy invokes, apart from required ones; CI should name its roots.

Each document's header names its kind, and the kind is found among the
inputs: a kind file among the paths, or a kind document in the same file as the
policies. --kind adds a kind file the paths don't hold, and a host binary has
its kinds linked in. The same kind from two sources must be identical, which
catches a stale export.
Documents of several kinds are checked in one run, each against its own kind,
and a required policy applies to the roots of its own kind. The kind documents
are checked too. A policy whose params have no defaults is checked with the
params unbound, the way explain shows it.

Lints are warnings unless the repository's sigil.yaml promotes them to errors
or turns them off. check reads the nearest sigil.yaml at or above the working
directory, or the file --config names:

  lints:
    gated-deny: error
    qualified-imports: warn

check needs only the kind files, not implementations of the host functions it
declares, so it is the command a policy repository runs in CI. It exits
non-zero when there is an error, including a lint set to error.`,
		Example: `# Type-check one policy against a kind file kept elsewhere
sigil check --kind deploy_approval.sigil deploy/production.sigil

# Check every document in a policy repository, every kind in it, as CI would
sigil check

# Check a self-contained file that holds its kind and its policies
sigil check bundle.sigil

# Check that every team policy invokes the guardrails unconditionally
sigil check --require deploy.guardrails --trusted deploy/ --policy 'payments.*' deploy_approval.sigil payments/`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFiles, _ := cmd.Flags().GetStringSlice("kind")
			patterns, _ := cmd.Flags().GetStringSlice("policy")
			trusted, _ := cmd.Flags().GetStringSlice("trusted")
			requires, _ := cmd.Flags().GetStringSlice("require")
			configFile, _ := cmd.Flags().GetString("config")
			src := project.Sources{Paths: args, Trusted: trusted, Kinds: kindFiles, Stdin: cmd.InOrStdin()}
			return run(cmd.OutOrStdout(), o, configFile, src, patterns, requires)
		},
	}

	addFlags(cmd)
	return cmd
}

// addFlags declares the check command's flags.
func addFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceP("kind", "k", nil, "Kind file the paths don't hold; the policies' kinds are found among the paths and the kinds linked in (repeatable)")
	// -R read subdirectories before every command did; it stays so scripts
	// that pass it keep working.
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories too; always on")
	_ = cmd.Flags().MarkDeprecated("recursive", "directories are always read recursively")
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

func run(out io.Writer, o *options, configFile string, src project.Sources, patterns, requires []string) humane.Error {
	if len(src.Paths) == 0 {
		src.Paths = []string{"."}
	}
	cfg, err := config.Load(configFile, ".")
	if err != nil {
		return err
	}
	p, err := project.Load(src, o.kinds)
	if err != nil {
		return err
	}
	p.Check()
	errs := p.Errors()
	var findings []lint.Finding
	if errs == nil {
		// Lints read what the checker learned, so they run on a project
		// that checks, even when a compile or --require then fails.
		var rs []string
		if len(requires) > 0 {
			if rs, err = roots(p, patterns, requires); err != nil {
				return err
			}
		}
		errs = compileAll(p, rs, requires)
		for _, g := range p.Groups() {
			findings = append(findings, lint.Run(g.Bundle, lint.Options{Kind: g.Kind.Model, Levels: cfg.Lints, Required: requires})...)
		}
	}
	return report(out, p, errs, findings, *o.output)
}

// compileAll compiles every policy of the project, so errors only a
// compile finds, such as an invocation argument out of its param's
// bounds, fail the check, and checks the roots against --require.
func compileAll(p *project.Project, roots, requires []string) diag.ErrorList {
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
	for _, name := range p.Policies() {
		_, list := p.Group(name).Bundle.Compile(name, bundle.Options{Static: true})
		add(list)
	}
	if len(requires) == 0 || errs != nil {
		return errs
	}
	for _, root := range roots {
		g := p.Group(root)
		_, list := g.Bundle.Compile(root, bundle.Options{Static: true, Require: required(p, g, requires)})
		add(list)
	}
	return errs
}

// required returns the required policies that apply to g's roots: those
// of g's kind, and those no document defines, so a root still reports
// that it doesn't invoke them.
func required(p *project.Project, g *project.Group, requires []string) []string {
	var out []string
	for _, r := range requires {
		if owner := p.Group(r); owner == nil || owner == g {
			out = append(out, r)
		}
	}
	return out
}

// roots returns the policies --require applies to: those matching
// --policy, or every policy no other one of its kind invokes, apart from
// the required ones.
func roots(p *project.Project, patterns, requires []string) ([]string, humane.Error) {
	if len(patterns) > 0 {
		return project.Match(p.Policies(), patterns)
	}
	invoked := map[string]bool{}
	for _, g := range p.Groups() {
		for _, d := range g.Bundle.Documents() {
			if d.Info == nil {
				continue
			}
			for _, target := range d.Info.Invocations {
				invoked[target] = true
			}
		}
	}
	var out []string
	for _, name := range p.Policies() {
		if !invoked[name] && !slices.Contains(requires, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// report prints the diagnostics, then a summary, and fails when there's
// an error: a compiler error, or a lint the configuration set to error.
func report(out io.Writer, p *project.Project, errs diag.ErrorList, findings []lint.Finding, format output.Format) humane.Error {
	diags := p.Resolve(collect(errs, findings))
	failed := 0
	for _, d := range diags {
		if d.Severity == diag.SeverityError {
			failed++
		}
	}
	if format == output.Text {
		return reportText(out, p, diags, failed)
	}
	records := make([]output.Diagnostic, 0, len(diags))
	for _, d := range diags {
		records = append(records, output.NewDiagnostic(d))
	}
	if err := output.Encode(out, format, records); err != nil {
		return err
	}
	if failed > 0 {
		return pretty.Fail(fmt.Sprintf("check found %s", problems(failed, len(diags)-failed)), advice(p, diags, "each diagnostic says where the problem is and how to fix it")...)
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
func reportText(out io.Writer, proj *project.Project, diags diag.ErrorList, failed int) humane.Error {
	p := pretty.New(out)
	if err := p.Diagnostics(diags, proj.SourceOf); err != nil {
		return err
	}
	if proj.Files() == 0 && failed == 0 {
		return p.Warning("no .sigil files found, so nothing was checked", "name the files or directories that hold the policies")
	}
	files := fmt.Sprintf("checked %d %s, ", proj.Files(), plural(proj.Files(), "file", "files"))
	warnings := len(diags) - failed
	switch {
	case failed > 0:
		if err := p.Fail(files + problems(failed, warnings)); err != nil {
			return err
		}
		return pretty.Fail(fmt.Sprintf("check found %s", problems(failed, warnings)), advice(proj, diags, "each diagnostic above says where the problem is and how to fix it")...)
	case warnings > 0:
		return p.Warning(files + problems(failed, warnings))
	}
	return p.Ok(files + "no problems found")
}

// advice is what a failed check suggests: the general hint, and when an
// error sits in a kind document, how to fix a kind file, including the
// rewrite of the decision syntax the language dropped.
func advice(p *project.Project, diags diag.ErrorList, general string) []string {
	for _, d := range diags {
		if d.Severity == diag.SeverityError && p.InKind(d) {
			return []string{general, "fix the kind file, or regenerate it from the host's Schema(); `sigil fmt --write` rewrites the old decision syntax"}
		}
	}
	return []string{general}
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
