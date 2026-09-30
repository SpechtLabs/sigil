// Package format implements the `sigil fmt` command. It isn't named fmt so it
// doesn't shadow the standard library package.
//
// fmt rewrites policy, module and kind documents in the one canonical
// style. For a single file or stdin it prints the result; for a directory
// or several files it prints a unified diff of every file that would
// change, since the sources would otherwise run together. --diff picks
// the diff anywhere, --write rewrites the files in place, and --check
// lists the files that aren't formatted. A file that doesn't parse is
// reported and left alone. [File] is the record it prints as JSON and
// YAML.
package format

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aymanbagabas/go-udiff"
	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/format"
)

// stdinName is how stdin is named in messages and in the --check list.
const stdinName = "<stdin>"

// mode is what fmt does with a formatted file.
type mode int

const (
	modeAuto  mode = iota // no flag: print a single file, diff several
	modePrint             // print it
	modeWrite             // write it back when it changed
	modeCheck             // list it when it changed
	modeDiff              // print a diff when it changed
)

// source is one input: a file's path, or stdin.
type source struct {
	path  string
	stdin bool
}

// File is one file fmt read, as JSON and YAML print it. A file that
// doesn't parse has its syntax errors in Diagnostics and nothing else set.
type File struct {
	File        string              `json:"file" yaml:"file"`                                   // the path, or <stdin>
	Formatted   bool                `json:"formatted" yaml:"formatted"`                         // it was already in the canonical style
	Written     bool                `json:"written,omitempty" yaml:"written,omitempty"`         // --write rewrote it
	Source      string              `json:"source,omitempty" yaml:"source,omitempty"`           // the formatted source, when fmt prints it
	Diff        string              `json:"diff,omitempty" yaml:"diff,omitempty"`               // the unified diff to the formatted source, in diff mode when it changed
	Diagnostics []output.Diagnostic `json:"diagnostics,omitempty" yaml:"diagnostics,omitempty"` // its syntax errors
}

// NewCommand returns the fmt command.
func NewCommand(opts ...Option) *cobra.Command {
	text := output.Text
	o := &options{output: &text}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:     "fmt [PATH...]",
		Aliases: []string{"format"},
		Short:   "Rewrite Sigil files in the canonical style",
		Long: `Rewrites Sigil files into the one canonical style, like gofmt. It needs
nothing but the files themselves. In a file that holds several documents, fmt
separates them with a "---" line, so a bundle has one canonical form too.

Sigil's grammar is whitespace-insensitive, so styles drift between teams unless
one tool owns the layout. Every PATH is a file, a directory, whose .sigil files
are formatted recursively, or "-" for stdin; with no paths, fmt formats the
current directory. Entries whose names start with "." are skipped.

For a single file or stdin, fmt prints the formatted source. For a directory or
several files, it prints a unified diff of every file that would change and a
line counting them; --diff prints the diff for a single file too. --write
updates the files in place instead. In CI, --check lists the files that aren't
formatted and fails when there are any. A file that doesn't parse is reported
and left alone.`,
		Example: `# Show what fmt would change in the current directory
sigil fmt

# Print the formatted version of a policy
sigil fmt deploy/production.sigil

# Format every .sigil file in the repository in place
sigil fmt --write .

# Fail when a file isn't formatted, e.g. in CI
sigil fmt --check .`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			write, _ := cmd.Flags().GetBool("write")
			check, _ := cmd.Flags().GetBool("check")
			diff, _ := cmd.Flags().GetBool("diff")
			m := modeAuto
			switch {
			case write:
				m = modeWrite
			case check:
				m = modeCheck
			case diff:
				m = modeDiff
			}
			return run(cmd.OutOrStdout(), cmd.InOrStdin(), args, m, *o.output)
		},
	}

	cmd.Flags().BoolP("write", "w", false, "Write the result back to the files instead of printing it")
	cmd.Flags().Bool("check", false, "Only report files that aren't formatted, and fail if there are any")
	cmd.Flags().BoolP("diff", "d", false, "Print a diff of every file that would change, even for a single file")
	cmd.MarkFlagsMutuallyExclusive("write", "check", "diff")

	return cmd
}

func run(out io.Writer, stdin io.Reader, paths []string, m mode, f output.Format) humane.Error {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	m = resolve(m, paths)
	sources, err := collect(paths)
	if err != nil {
		return err
	}
	if m == modeWrite && slices.ContainsFunc(sources, func(s source) bool { return s.stdin }) {
		return humane.New("--write can't write back to stdin", "drop --write to print the result, or name the files to rewrite")
	}

	p := pretty.New(out)
	var changed []string
	var broken diag.ErrorList
	records := []File{}
	files := map[string][]byte{}
	for _, s := range sources {
		src, name, err := read(s, stdin)
		if err != nil {
			return err
		}
		formatted, errs := format.Source(name, src)
		if errs != nil {
			files[name] = src
			broken = append(broken, errs...)
			records = append(records, File{File: name, Diagnostics: diagnostics(errs)})
			continue
		}
		differs := !bytes.Equal(src, formatted)
		if differs {
			changed = append(changed, name)
		}
		if f == output.Text {
			err = emit(p, s, name, src, formatted, differs, m)
		} else {
			var rec File
			rec, err = record(s, name, src, formatted, differs, m)
			records = append(records, rec)
		}
		if err != nil {
			return err
		}
	}
	if f != output.Text {
		return encode(out, f, m, records, changed, broken)
	}
	return summarize(p, m, len(sources), changed, broken, func(file string) []byte { return files[file] })
}

// summarize prints the syntax errors and, unless fmt printed the
// formatted sources, one line that sums the run up, and fails when a
// file has syntax errors or, with --check, isn't formatted.
func summarize(p *pretty.Printer, m mode, total int, changed []string, broken diag.ErrorList, src diag.Sources) humane.Error {
	if err := p.Diagnostics(broken, src); err != nil {
		return err
	}
	if n := brokenFiles(broken); n > 0 {
		if err := p.Fail(fmt.Sprintf("%d %s syntax errors", n, plural(n, "file has", "files have"))); err != nil {
			return err
		}
		return failure(m, changed, broken)
	}
	n := len(changed)
	if total == 0 {
		return p.Warning("no .sigil files found, so nothing was formatted", "name the files, or a directory that holds them")
	}
	switch m {
	case modeCheck:
		if n > 0 {
			msg := fmt.Sprintf("%d of %d %s %s not formatted", n, total, plural(total, "file", "files"), plural(n, "is", "are"))
			if err := p.Fail(msg); err != nil {
				return err
			}
			return failure(m, changed, broken)
		}
		return p.Ok(fmt.Sprintf("%d %s formatted", total, plural(total, "file is", "files are")))
	case modeWrite:
		if n > 0 {
			return p.Ok(fmt.Sprintf("reformatted %d %s, %d left unchanged", n, plural(n, "file", "files"), total-n), changed...)
		}
		return p.Ok(fmt.Sprintf("%d %s already formatted", total, plural(total, "file is", "files are")))
	case modeDiff:
		if n > 0 {
			msg := fmt.Sprintf("%d of %d %s would be reformatted", n, total, plural(total, "file", "files"))
			return p.Note(msg, "run `sigil fmt -w` on the same paths to rewrite "+plural(n, "it", "them"))
		}
		return p.Ok(fmt.Sprintf("%d %s already formatted", total, plural(total, "file is", "files are")))
	default:
		return nil
	}
}

// encode prints the records as JSON or YAML, and fails the way the text
// summary does.
func encode(out io.Writer, f output.Format, m mode, records []File, changed []string, broken diag.ErrorList) humane.Error {
	if err := output.Encode(out, f, records); err != nil {
		return err
	}
	if err := failure(m, changed, broken); err != nil {
		return err
	}
	return nil
}

// failure is the error a run ends with: files with syntax errors, or,
// with --check, files that aren't formatted. It's nil when there are
// neither.
func failure(m mode, changed []string, broken diag.ErrorList) *pretty.Failed {
	if n := brokenFiles(broken); n > 0 {
		return pretty.Fail(fmt.Sprintf("%d %s syntax errors", n, plural(n, "file has", "files have")), "fix the syntax errors; fmt only formats files that parse")
	}
	if n := len(changed); m == modeCheck && n > 0 {
		return pretty.Fail(fmt.Sprintf("%d %s not formatted", n, plural(n, "file is", "files are")), "run `sigil fmt --write` on them")
	}
	return nil
}

// brokenFiles counts the files the syntax errors are in.
func brokenFiles(errs diag.ErrorList) int {
	files := map[string]bool{}
	for _, e := range errs {
		files[e.File] = true
	}
	return len(files)
}

// record does what the mode says with one formatted source, for JSON and
// YAML: carry it, carry its diff when it changed, or write it back when
// it changed.
func record(s source, name string, src, formatted []byte, changed bool, m mode) (File, humane.Error) {
	rec := File{File: name, Formatted: !changed}
	switch {
	case m == modePrint:
		rec.Source = string(formatted)
	case m == modeDiff && changed:
		rec.Diff = unified(name, src, formatted)
	case m == modeWrite && changed:
		if err := rewrite(s.path, formatted); err != nil {
			return rec, err
		}
		rec.Written = true
	}
	return rec, nil
}

func diagnostics(errs diag.ErrorList) []output.Diagnostic {
	out := make([]output.Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = output.NewDiagnostic(e)
	}
	return out
}

// emit does what the mode says with one formatted source: print it,
// print its diff when it changed, list it when it changed, or write it
// back when it changed.
func emit(p *pretty.Printer, s source, name string, src, formatted []byte, changed bool, m mode) humane.Error {
	switch {
	case m == modePrint:
		return p.Print(string(formatted))
	case m == modeDiff && changed:
		return p.Print(paint(p.Theme(), unified(name, src, formatted)))
	case m == modeCheck && changed:
		return p.Print(name + "\n")
	case m == modeWrite && changed:
		return rewrite(s.path, formatted)
	}
	return nil
}

// resolve returns the mode a run without --write, --check or --diff
// takes from its paths: a single file or stdin prints its formatted
// source, and a directory or several files print a diff, since their
// sources would run together. A path that can't be read prints, and
// collect then reports it.
func resolve(m mode, paths []string) mode {
	if m != modeAuto {
		return m
	}
	if len(paths) == 1 {
		info, err := os.Stat(paths[0])
		if paths[0] == "-" || err != nil || !info.IsDir() {
			return modePrint
		}
	}
	return modeDiff
}

// unified returns the unified diff from a file's source to its formatted
// form, labeled the way gofmt -d labels it: the original as name.orig.
func unified(name string, src, formatted []byte) string {
	return udiff.Unified(name+".orig", name, string(src), string(formatted))
}

// paint colors a unified diff for a terminal: the file headers bold, the
// hunk headers as information, removed lines as failures and added lines
// as successes. The headers are the lines before the first hunk, so a
// removed line that starts with "-- " stays a removal. Off a terminal
// the theme leaves the diff as it is.
func paint(t pretty.Theme, d string) string {
	var b strings.Builder
	hunks := false
	for line := range strings.Lines(d) {
		text, nl := strings.CutSuffix(line, "\n")
		switch {
		case !hunks && !strings.HasPrefix(text, "@@"):
			text = t.Bold(text)
		case strings.HasPrefix(text, "@@"):
			hunks = true
			text = t.Info(text)
		case strings.HasPrefix(text, "-"):
			text = t.Fail(text)
		case strings.HasPrefix(text, "+"):
			text = t.Ok(text)
		}
		b.WriteString(text)
		if nl {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// collect expands the paths into the sources to format, by the rules
// every command shares ([project.Expand]): a directory contributes every
// .sigil file below it, "-" is stdin, and a file named on the command
// line is formatted whatever its extension.
func collect(paths []string) ([]source, humane.Error) {
	files, err := project.Expand(paths, project.IsSigil)
	if err != nil {
		return nil, err
	}
	out := make([]source, len(files))
	for i, f := range files {
		if f == "-" {
			out[i] = source{stdin: true}
		} else {
			out[i] = source{path: f}
		}
	}
	return out, nil
}

// read returns a source's contents and the name messages use for it.
func read(s source, stdin io.Reader) ([]byte, string, humane.Error) {
	if s.stdin {
		src, err := io.ReadAll(stdin)
		if err != nil {
			return nil, "", humane.Wrap(err, "stdin couldn't be read", "pipe a Sigil file in, or name files instead of `-`")
		}
		return src, stdinName, nil
	}
	src, err := os.ReadFile(s.path)
	if err != nil {
		return nil, "", humane.Wrap(err, s.path+" couldn't be read", "check the file's permissions")
	}
	return src, filepath.ToSlash(s.path), nil
}

// rewrite replaces a file's contents, keeping its permissions.
func rewrite(path string, src []byte) humane.Error {
	info, err := os.Stat(path)
	if err != nil {
		return humane.Wrap(err, path+" can't be read", "check the file's permissions")
	}
	if err := os.WriteFile(path, src, info.Mode().Perm()); err != nil {
		return humane.Wrap(err, path+" couldn't be written", "check the file's permissions")
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
