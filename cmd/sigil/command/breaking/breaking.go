// Package breaking implements the `sigil breaking` command. It loads two
// versions of a kind file, the way `sigil check` loads a kind file,
// compares them with package compat, and prints every change with its
// class, then the header rules the new kind breaks, and a summary line.
// [Breaking] is the record it prints as JSON and YAML. The command fails
// when either kind file doesn't load, or when the new kind's `version` or
// `accepts` breaks a rule; a breaking change that `accepts` covers is
// reported and doesn't fail.
package breaking

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/compat"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
)

// stdinName is how a kind file read from stdin is named.
const stdinName = "<stdin>"

// The labels a change is printed with, by its class and whether
// `accepts` covers it.
const (
	labelBreaking   = "breaking"   // breaking, or breaking in behavior, and `accepts` doesn't cover it
	labelCovered    = "covered"    // breaking, or breaking in behavior, and `accepts` covers it
	labelCompatible = "compatible" // compatible
)

// Breaking is what breaking found, as JSON and YAML print it.
type Breaking struct {
	Old      Kind      `json:"old" yaml:"old"` // the old kind file and its header
	New      Kind      `json:"new" yaml:"new"` // the new kind file and its header
	OK       bool      `json:"ok" yaml:"ok"`   // whether the new header breaks no rule; the exit status follows it
	Changes  []Change  `json:"changes" yaml:"changes"`
	Problems []Problem `json:"problems" yaml:"problems"` // the header rules the new kind breaks, then the notes
	// MinVersion and MinAccepts are the lowest `version` and `accepts` the
	// new kind may declare.
	MinVersion int `json:"minVersion" yaml:"minVersion"`
	MinAccepts int `json:"minAccepts" yaml:"minAccepts"`
}

// Kind is one of the two kind files and its header.
type Kind struct {
	File    string `json:"file" yaml:"file"`
	Kind    string `json:"kind" yaml:"kind"` // the kind's name
	Version int    `json:"version" yaml:"version"`
	Accepts int    `json:"accepts" yaml:"accepts"`
}

// Change is one change between the kinds.
type Change struct {
	Change  string `json:"change" yaml:"change"`               // added, removed, changed, reordered or ambiguous
	Class   string `json:"class" yaml:"class"`                 // compatible, breaking or behavior
	Covered bool   `json:"covered" yaml:"covered"`             // a breaking or behavior change the new `accepts` covers
	Path    string `json:"path" yaml:"path"`                   // the declaration, like `decision deny reason no_release`
	Old     string `json:"old,omitempty" yaml:"old,omitempty"` // as the old kind file declares it
	New     string `json:"new,omitempty" yaml:"new,omitempty"` // as the new kind file declares it
	Message string `json:"message" yaml:"message"`
	Help    string `json:"help,omitempty" yaml:"help,omitempty"` // why it breaks, and the fix unless it's covered
}

// Problem is a header rule the new kind breaks, or a note about its
// header.
type Problem struct {
	Rule     string `json:"rule" yaml:"rule"`         // version_decreased, version_unchanged, accepts_not_raised, accepts_lowered or version_only
	Severity string `json:"severity" yaml:"severity"` // error, or note for one that doesn't fail
	Message  string `json:"message" yaml:"message"`
	Help     string `json:"help,omitempty" yaml:"help,omitempty"`
}

// file is one of the two kind files, as read and loaded.
type file struct {
	kind *kind.Kind // nil when it doesn't load
	name string
	src  []byte
	errs diag.ErrorList
}

// NewCommand returns the breaking command, configured by opts. Without
// [WithOutput] it prints text.
func NewCommand(opts ...Option) *cobra.Command {
	text := output.Text
	o := &options{output: &text}
	for _, opt := range opts {
		opt(o)
	}

	return &cobra.Command{
		Use:        "breaking OLD_KIND_FILE NEW_KIND_FILE",
		SuggestFor: []string{"compat", "compatible", "diff"},
		Short:      "Detect kind changes that break existing policies",
		Long: `Compares two versions of a kind file and classifies every change by the
compatibility table: compatible, breaking because a policy written against the
old kind may stop compiling, or breaking in behavior because every policy still
compiles but decisions may change. A rename is a removal and an addition.

It also checks the new kind's header against the old one: every change bumps
version, a breaking change raises accepts to the new version, and neither
number goes down. A breaking change that accepts covers is listed and doesn't
fail.

Either file may be "-" for stdin, so the old kind can come straight from git.
Run it in CI on every change to a kind, against the kind file on the main
branch. It exits non-zero when either kind file doesn't load or the new header
breaks a rule.`,
		Example: `# Compare the kind on main with the working copy
git show main:policies/deploy_approval.sigil | sigil breaking - policies/deploy_approval.sigil

# Compare two files, and read the result as JSON
sigil breaking -o json old/deploy_approval.sigil deploy_approval.sigil`,
		Args:              usage.Exactly("OLD_KIND_FILE", "NEW_KIND_FILE"),
		ValidArgsFunction: complete.SigilFilesUpTo(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.InOrStdin(), cmd.OutOrStdout(), *o.output, args[0], args[1])
		},
	}
}

// run compares the kind file oldPath names with the one nextPath names,
// either of which may be "-" for stdin, and reports what changed.
func run(stdin io.Reader, out io.Writer, format output.Format, oldPath, nextPath string) humane.Error {
	if oldPath == "-" && nextPath == "-" {
		return humane.New("OLD_KIND_FILE and NEW_KIND_FILE can't both come from stdin", "name one of them as a file, such as `git show main:deploy_approval.sigil | sigil breaking - deploy_approval.sigil`")
	}
	files := make([]file, 2)
	for i, path := range []string{oldPath, nextPath} {
		f, err := read(stdin, path)
		if err != nil {
			return err
		}
		files[i] = f
	}
	var errs diag.ErrorList
	for i := range files {
		// The same loader as check's, so both report the same errors.
		files[i].kind, files[i].errs = check.LoadKind(files[i].name, files[i].src)
		errs = append(errs, files[i].errs...)
	}
	if errs != nil {
		return fail(out, format, errs, files)
	}
	old, next := files[0], files[1]
	r := compat.Compare(old.kind, next.kind)
	rec := record(r, old.name, next.name)
	if format != output.Text {
		if err := output.Encode(out, format, rec); err != nil {
			return err
		}
	} else if err := report(pretty.New(out), r, rec); err != nil {
		return err
	}
	if !rec.OK {
		return pretty.Fail(next.name+": "+summary(r), advice(r)...)
	}
	return nil
}

// read reads the kind file path names, or stdin for "-".
func read(stdin io.Reader, path string) (file, humane.Error) {
	if path == "-" {
		src, err := io.ReadAll(stdin)
		if err != nil {
			return file{}, humane.Wrap(err, "stdin couldn't be read", "pipe a kind file in, such as `git show main:deploy_approval.sigil | sigil breaking - deploy_approval.sigil`")
		}
		return file{name: stdinName, src: src}, nil
	}
	src, err := os.ReadFile(path) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return file{}, humane.Wrap(err, path+" couldn't be read", "check the file's path and permissions")
	}
	return file{name: path, src: src}, nil
}

// fail reports the diagnostics of kind files that don't load: as text
// in the error, as check prints them, and as JSON or YAML the records
// check prints.
func fail(out io.Writer, format output.Format, errs diag.ErrorList, files []file) humane.Error {
	const msg = "a kind file doesn't load, so nothing was compared"
	const advice = "fix the errors above; sigil check reports the same errors for a kind file"
	src := func(name string) []byte {
		for _, f := range files {
			if f.name == name {
				return f.src
			}
		}
		return nil
	}
	if format == output.Text {
		return pretty.Diagnose(errs, src, msg, advice)
	}
	records := make([]output.Diagnostic, 0, len(errs))
	for _, d := range errs {
		records = append(records, output.NewDiagnostic(d))
	}
	if err := output.Encode(out, format, records); err != nil {
		return err
	}
	return pretty.Fail(msg, advice)
}

// record turns the report into the record JSON and YAML print.
func record(r *compat.Report, oldName, nextName string) Breaking {
	rec := Breaking{
		Old:        Kind{File: oldName, Kind: r.Old.Name, Version: r.Old.Version, Accepts: r.Old.Accepts},
		New:        Kind{File: nextName, Kind: r.New.Name, Version: r.New.Version, Accepts: r.New.Accepts},
		OK:         r.OK(),
		Changes:    make([]Change, 0, len(r.Changes)),
		Problems:   make([]Problem, 0, len(r.Problems)),
		MinVersion: r.MinVersion,
		MinAccepts: r.MinAccepts,
	}
	for _, c := range r.Changes {
		ch := Change{Change: string(c.Op), Class: c.Class.String(), Path: c.Path, Old: c.Old, New: c.New, Message: c.Message}
		switch {
		case c.Class == compat.Compatible:
		case r.Covered():
			ch.Covered, ch.Help = true, c.Why
		default:
			ch.Help = c.Help(r.MinAccepts)
		}
		rec.Changes = append(rec.Changes, ch)
	}
	for _, p := range r.Problems {
		rec.Problems = append(rec.Problems, Problem{Rule: string(p.Rule), Severity: p.Severity.String(), Message: p.Message, Help: p.Help})
	}
	return rec
}

// report prints every change and problem against the new kind file, the
// one to fix, then the summary line:
//
//	deploy_approval.sigil: breaking: precedence changed
//	  - deny > review > approve
//	  + deny > approve > review
//	  = help: every policy still compiles, but ...; raise `accepts` to 4, ...
//	deploy_approval.sigil: error: 1 breaking change, but `accepts` is 2
//	  = help: raise `accepts` to 4, ...
//
//	✗ DeployApproval 3 → 4: 1 breaking change
func report(p *pretty.Printer, r *compat.Report, rec Breaking) humane.Error {
	t := p.Theme()
	var b strings.Builder
	for i, c := range rec.Changes {
		label, style := labelCompatible, t.Ok
		switch {
		case c.Covered:
			label, style = labelCovered, t.Info
		case c.Class != compat.Compatible.String():
			label, style = labelBreaking, t.Fail
		}
		entry(&b, t, rec.New.File, style(label), c.Message)
		if op := r.Changes[i].Op; (op == compat.Changed || op == compat.Reordered) && c.Old != "" && c.New != "" {
			b.WriteString("  " + t.Fail("- "+c.Old) + "\n")
			b.WriteString("  " + t.Ok("+ "+c.New) + "\n")
		}
		hint(&b, t, c.Covered, c.Help)
	}
	for _, pr := range rec.Problems {
		style := t.Fail
		if pr.Severity == compat.Note.String() {
			style = t.Info
		}
		entry(&b, t, rec.New.File, style(pr.Severity), pr.Message)
		hint(&b, t, false, pr.Help)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	if err := p.Print(b.String()); err != nil {
		return err
	}
	if rec.OK {
		return p.Ok(summary(r))
	}
	return p.Fail(summary(r))
}

// entry writes the first line of a change or problem: `file: label: what`.
func entry(b *strings.Builder, t pretty.Theme, file, label, msg string) {
	b.WriteString(t.Location(file) + ": " + label + ": " + t.Bold(msg) + "\n")
}

// hint writes the help under a change or problem, as `= help:`, or as
// `= note:` for a covered change, which needs nothing done.
func hint(b *strings.Builder, t pretty.Theme, note bool, text string) {
	if text == "" {
		return
	}
	label := "= help:"
	if note {
		label = "= note:"
	}
	b.WriteString("  " + t.Help(label) + " " + text + "\n")
}

// summary sums the comparison up in one line, such as
// `DeployApproval 3 → 4: 2 breaking changes and 1 compatible change`.
func summary(r *compat.Report) string {
	head := r.New.Name + " " + fmt.Sprint(r.New.Version)
	switch {
	case r.Old.Name != r.New.Name:
		head = fmt.Sprintf("%s %d → %s %d", r.Old.Name, r.Old.Version, r.New.Name, r.New.Version)
	case r.Old.Version != r.New.Version:
		head = fmt.Sprintf("%s %d → %d", r.New.Name, r.Old.Version, r.New.Version)
	}
	breaking := r.Breaking()
	var parts []string
	if breaking > 0 {
		s := count(breaking, "breaking change")
		if r.Covered() {
			s += fmt.Sprintf(" that `accepts: %d` covers", r.New.Accepts)
		}
		parts = append(parts, s)
	}
	if n := len(r.Changes) - breaking; n > 0 {
		parts = append(parts, count(n, "compatible change"))
	}
	if len(parts) == 0 {
		return head + ": no changes"
	}
	return head + ": " + strings.Join(parts, " and ")
}

// advice is the help of every header rule the new kind breaks, for the
// error the command fails with.
func advice(r *compat.Report) []string {
	var out []string
	for _, p := range r.Problems {
		if p.Severity == compat.Error {
			out = append(out, p.Help)
		}
	}
	return out
}

// count spells `1 compatible change` or `2 compatible changes`.
func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
