// Package golang implements the `sigil gen go` command. It isn't named go
// because that is a keyword.
//
// The command generates Go code from a kind file with package gogen: the
// enums, struct types, input struct and per-decision payload types, the
// decision and reason handles, and a constructor that builds the kind, so
// a Go service that doesn't import the defining host consumes decisions
// with typed values. It prints the code, or writes the --out file, which
// --check only compares. [Generated] is the record it prints as JSON and
// YAML.
package golang

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gogen"
)

// What gen go found or did with the --out file.
const (
	statusCurrent = "current" // it already matched, so it was left alone
	statusWritten = "written" // it was missing or stale, and was rewritten
	statusStale   = "stale"   // --check found it stale
)

// stdinName is what the kind file read from stdin is called in
// diagnostics.
const stdinName = "<stdin>"

// Generated is what gen go printed or did, as JSON and YAML print it.
type Generated struct {
	Kind    string `json:"kind" yaml:"kind"`                         // the kind's name
	Package string `json:"package" yaml:"package"`                   // the generated code's package name
	File    string `json:"file,omitempty" yaml:"file,omitempty"`     // the --out file
	Status  string `json:"status,omitempty" yaml:"status,omitempty"` // with --out: current, written or stale
	Code    string `json:"code" yaml:"code"`                         // the generated Go source
	Version int    `json:"version" yaml:"version"`                   // the kind's version
}

// request is what one run of gen go was asked for on the command line.
type request struct {
	path  string // KIND_FILE, or - for stdin
	pkg   string // --package; empty for the kind's name in lower case
	out   string // --out; empty to print the code
	check bool   // --check
}

// NewCommand returns the gen go command.
func NewCommand(opts ...Option) *cobra.Command {
	text := output.Text
	o := &options{output: &text}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:     "go KIND_FILE",
		Aliases: []string{"golang"},
		Short:   "Generate typed Go code from a kind file",
		Long: `Generates Go code from a kind file, so a Go service that doesn't import the
host that defines the kind can build it and read results with typed values: a
named string type and constants for every enum, a struct for every struct type
and for the input, a payload struct of its own for every decision, a variable
for every decision and reason, Funcs for the host functions, and NewKind, which
builds the kind. Read a result with a type switch on res.Value() and a switch on
res.Why().

The kind NewKind builds exports the kind file it was generated from, byte for
byte, so it loads policies from a bundle that holds that file. A kind file a host
exported generates as it is; a hand-written one may need its declarations
reordered first, and gen go says how. KIND_FILE may be - for stdin.

The code is printed unless --out names a file to write it to; gen go creates the
file's directory, and leaves the file untouched when it's current. --check only
compares, and fails when the file is stale, which is how CI notices a kind
change that wasn't regenerated. --package defaults to the kind's name in lower
case.`,
		Example: `# Print the generated code
sigil gen go deploy_approval.sigil

# Write it into the approval package, e.g. from go generate
sigil gen go --package approval --out approval/kind.go deploy_approval.sigil

# Fail in CI when the generated file is stale
sigil gen go --check --package approval --out approval/kind.go deploy_approval.sigil`,
		Args:              usage.Exactly("KIND_FILE"),
		ValidArgsFunction: complete.SigilFilesUpTo(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, _ := cmd.Flags().GetString("package")
			out, _ := cmd.Flags().GetString("out")
			check, _ := cmd.Flags().GetBool("check")
			r := request{path: args[0], pkg: pkg, out: out, check: check}
			return run(cmd.OutOrStdout(), cmd.InOrStdin(), r, *o.output)
		},
	}

	cmd.Flags().StringP("package", "p", "", "Go package name of the generated code (defaults to the kind's name in lower case)")
	cmd.Flags().String("out", "", "File to write the generated code to (defaults to stdout)")
	cmd.Flags().Bool("check", false, "Only compare with the --out file, and fail when it's stale")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.RegisterFlagCompletionFunc("package", cobra.NoFileCompletions)
	_ = cmd.MarkFlagFilename("out", "go")

	return cmd
}

func run(stdout io.Writer, stdin io.Reader, r request, f output.Format) humane.Error {
	if err := checkFlags(r); err != nil {
		return err
	}
	name, src, err := read(stdin, r.path)
	if err != nil {
		return err
	}
	code, k, errs := gogen.GenerateFile(name, src, gogen.Options{Package: r.pkg})
	if errs != nil {
		msg := "Go can't declare the kind as the file writes it, so no code was generated"
		if k == nil {
			msg = "the kind file doesn't load, so no code was generated"
		}
		return fail(stdout, f, errs, src, msg)
	}
	pkg := r.pkg
	if pkg == "" {
		pkg = gogen.PackageName(k.Name)
	}
	rec := Generated{Kind: k.Name, Version: k.Version, Package: pkg, File: r.out, Code: string(code)}
	if r.out == "" {
		if f != output.Text {
			return output.Encode(stdout, f, rec)
		}
		if _, err := stdout.Write(code); err != nil {
			return humane.Wrap(err, "the generated code couldn't be written", "check where the output is going")
		}
		return nil
	}
	current, rerr := os.ReadFile(r.out)
	switch {
	case rerr == nil && bytes.Equal(current, code):
		rec.Status = statusCurrent
	case r.check:
		rec.Status = statusStale
	default:
		if err := write(r.out, code); err != nil {
			return err
		}
		rec.Status = statusWritten
	}
	return report(stdout, f, rec, r)
}

// checkFlags fails for flags that can't work together or can't name what
// they should, before anything is read.
func checkFlags(r request) humane.Error {
	if r.check && r.out == "" {
		return humane.New("--check needs the file to compare", "name the generated file with --out")
	}
	if r.pkg != "" {
		if err := gogen.CheckPackage(r.pkg); err != nil {
			return humane.New("--package: "+err.Error(), err.Advice()...)
		}
	}
	if info, err := os.Stat(r.out); r.out != "" && err == nil && info.IsDir() {
		return humane.New("--out "+r.out+" is a directory", "name the file to write the code to, such as "+filepath.Join(r.out, "kind.go"))
	}
	return nil
}

// read reads the kind file, or stdin for -, and returns the name its
// diagnostics use.
func read(stdin io.Reader, path string) (string, []byte, humane.Error) {
	if path == "-" {
		src, err := io.ReadAll(stdin)
		if err != nil {
			return "", nil, humane.Wrap(err, "the kind file couldn't be read from stdin", "check what's piped in")
		}
		return stdinName, src, nil
	}
	src, err := os.ReadFile(path) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return "", nil, humane.Wrap(err, path+" can't be read", "name the kind file, such as one sigil export wrote, or - for stdin")
	}
	return path, src, nil
}

// write writes the generated code to out, creating its directory.
func write(out string, code []byte) humane.Error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // a source directory is meant to be read by everyone
		return humane.Wrap(err, "the directory of "+out+" couldn't be created", "check that its parent is writable")
	}
	if err := os.WriteFile(out, code, 0o644); err != nil { //nolint:gosec // Go source is meant to be read by everyone
		return humane.Wrap(err, out+" couldn't be written", "check that it's writable")
	}
	return nil
}

// fail reports the diagnostics of a kind file gen go can't generate. As
// text, the error carries them, as compile's does; as JSON or YAML,
// they're printed as check prints them.
func fail(w io.Writer, f output.Format, errs diag.ErrorList, src []byte, msg string) humane.Error {
	const advice = "fix the errors above; each says where the problem is and how to fix it"
	if f == output.Text {
		return pretty.Diagnose(errs, func(string) []byte { return src }, msg, advice)
	}
	records := make([]output.Diagnostic, 0, len(errs))
	for _, d := range errs {
		records = append(records, output.NewDiagnostic(d))
	}
	if err := output.Encode(w, f, records); err != nil {
		return err
	}
	return pretty.Fail(msg, advice)
}

// report says what gen go did with the --out file, and fails when
// --check found it stale.
func report(stdout io.Writer, f output.Format, rec Generated, r request) humane.Error {
	msg := rec.File + " is stale: it doesn't match the code " + r.path + " generates"
	help := "regenerate it with `sigil gen go --package " + rec.Package + " --out " + rec.File + " " + r.path + "`"
	if f != output.Text {
		if err := output.Encode(stdout, f, rec); err != nil {
			return err
		}
		if rec.Status == statusStale {
			return pretty.Fail(msg, help)
		}
		return nil
	}
	p := pretty.New(stdout)
	switch rec.Status {
	case statusCurrent:
		return p.Ok(rec.File + " is up to date")
	case statusStale:
		if err := p.Fail(msg, help); err != nil {
			return err
		}
		return pretty.Fail(msg, help)
	}
	return p.Ok("wrote " + rec.File)
}
