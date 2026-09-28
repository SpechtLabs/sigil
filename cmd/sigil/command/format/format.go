// Package format implements the `sigil fmt` command. It isn't named fmt so it
// doesn't shadow the standard library package.
package format

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/format"
)

// stdinName is how stdin is named in messages and in the --check list.
const stdinName = "<stdin>"

// mode is what fmt does with a formatted file.
type mode int

const (
	modePrint mode = iota // print it
	modeWrite             // write it back when it changed
	modeCheck             // list it when it changed
)

// source is one input: a file's path, or stdin.
type source struct {
	path  string
	stdin bool
}

// NewCommand returns the fmt command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
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
current directory. Entries whose names start with "." are skipped. The result
is printed unless --write updates the files in place. In CI, --check lists the
files that aren't formatted and fails when there are any. A file that doesn't
parse is reported and left alone.`,
		Example: `# Print the formatted version of a policy
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
			m := modePrint
			switch {
			case write:
				m = modeWrite
			case check:
				m = modeCheck
			}
			return run(cmd.OutOrStdout(), cmd.InOrStdin(), args, m)
		},
	}

	cmd.Flags().BoolP("write", "w", false, "Write the result back to the files instead of printing it")
	cmd.Flags().Bool("check", false, "Only report files that aren't formatted, and fail if there are any")
	cmd.MarkFlagsMutuallyExclusive("write", "check")

	return cmd
}

func run(out io.Writer, stdin io.Reader, paths []string, m mode) humane.Error {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	sources, err := collect(paths)
	if err != nil {
		return err
	}
	if m == modeWrite && slices.ContainsFunc(sources, func(s source) bool { return s.stdin }) {
		return humane.New("--write can't write back to stdin", "drop --write to print the result, or name the files to rewrite")
	}

	var unformatted, broken []string
	for _, s := range sources {
		src, name, err := read(s, stdin)
		if err != nil {
			return err
		}
		formatted, errs := format.Source(name, src)
		if errs != nil {
			broken = append(broken, render(errs, src))
			continue
		}
		if !bytes.Equal(src, formatted) {
			unformatted = append(unformatted, name)
		}
		if err := emit(out, s, name, formatted, !bytes.Equal(src, formatted), m); err != nil {
			return err
		}
	}

	if len(broken) > 0 {
		return humane.New(strings.Join(broken, "\n"), "fix the syntax errors above; fmt only formats files that parse")
	}
	if m == modeCheck && len(unformatted) > 0 {
		return humane.New(fmt.Sprintf("%d %s not formatted", len(unformatted), plural(len(unformatted), "file is", "files are")),
			"run `sigil fmt --write` on them")
	}
	return nil
}

// emit does what the mode says with one formatted source: print it, list
// it when it changed, or write it back when it changed.
func emit(out io.Writer, s source, name string, formatted []byte, changed bool, m mode) humane.Error {
	var err error
	switch {
	case m == modePrint:
		_, err = out.Write(formatted)
	case m == modeCheck && changed:
		_, err = fmt.Fprintln(out, name)
	case m == modeWrite && changed:
		return rewrite(s.path, formatted)
	}
	if err != nil {
		return humane.Wrap(err, "the output couldn't be written", "check where the output is going")
	}
	return nil
}

// collect expands the paths into the sources to format: a directory
// contributes every .sigil file below it, in lexical order, and "-" is
// stdin. A file named on the command line is formatted whatever its
// extension.
func collect(paths []string) ([]source, humane.Error) {
	var out []source
	for _, p := range paths {
		if p == "-" {
			out = append(out, source{stdin: true})
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, humane.Wrap(err, p+" can't be read", "name a `.sigil` file, a directory or `-` for stdin")
		}
		if !info.IsDir() {
			out = append(out, source{path: p})
			continue
		}
		files, werr := walk(p)
		if werr != nil {
			return nil, werr
		}
		for _, f := range files {
			out = append(out, source{path: f})
		}
	}
	return out, nil
}

// walk returns the .sigil files below dir, skipping entries whose names
// start with ".". Symbolic links are followed, as the loader does.
func walk(dir string) ([]string, humane.Error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, humane.Wrap(err, dir+" couldn't be read", "check the directory's permissions")
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var files []string
	for _, name := range names {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil {
			return nil, humane.Wrap(err, p+" can't be read", "check the entry's permissions")
		}
		switch {
		case info.IsDir():
			sub, err := walk(p)
			if err != nil {
				return nil, err
			}
			files = append(files, sub...)
		case strings.HasSuffix(name, ".sigil"):
			files = append(files, p)
		}
	}
	return files, nil
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

// render formats a file's parse errors the way the other commands do.
func render(errs diag.ErrorList, src []byte) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = diag.Render(e, src)
	}
	return strings.TrimRight(strings.Join(parts, ""), "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
