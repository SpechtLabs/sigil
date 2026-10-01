// Package complete holds shell completion functions shared by sigil commands.
// Each one is a [cobra.CompletionFunc] a command sets as its
// ValidArgsFunction or registers for a flag.
package complete

import (
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/payload"
)

// names collects policy names for a completion: those that start with
// prefix, each once.
type names struct {
	seen   map[string]bool
	prefix string
	out    []cobra.Completion
}

// SigilFiles completes .sigil files and directories for every argument.
func SigilFiles(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return []cobra.Completion{"sigil"}, cobra.ShellCompDirectiveFilterFileExt
}

// SigilFilesUpTo completes .sigil files for the first n arguments and nothing
// after that, for commands that take a fixed number of files.
func SigilFilesUpTo(n int) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) >= n {
			return cobra.NoFileCompletions(cmd, args, toComplete)
		}
		return SigilFiles(cmd, args, toComplete)
	}
}

// Policies completes a --policy flag with the names of the policies in
// the command's paths, the current directory when it has none, apart from
// the files under its --trusted paths, which the command doesn't read as
// its own.
func Policies(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return policies(args, trusted(cmd), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// Required completes a --require flag with the names of the policies in
// the command's --trusted paths, where required policies come from, or
// in its paths when it has none.
func Required(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if t := trusted(cmd); len(t) > 0 {
		args = t
	}
	return policies(args, nil, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// Compiled completes a --policy flag of a compiled binary with the names
// of the policies compiled into it, b's, apart from its trusted files,
// which the commands don't read as its own. Like [Policies], it reads only
// the headers, so it needs no kind and stays fast.
func Compiled(b *payload.Bundle) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		n := names{prefix: toComplete, seen: map[string]bool{}}
		for _, f := range b.Paths {
			n.add(f.Name, []byte(f.Source))
		}
		return n.sorted(), cobra.ShellCompDirectiveNoFileComp
	}
}

// policies returns the names of the policies in paths, apart from those
// under skip, that start with prefix, sorted. It only parses: a header
// names its policy whatever its kind, so completing needs no kind and
// stays fast. Anything that can't be read or parsed is left out, since a
// completion has nowhere to report it.
func policies(paths, skip []string, prefix string) []cobra.Completion {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files, err := project.Expand(paths, project.IsSigil)
	if err != nil {
		return nil
	}
	skipped := map[string]bool{}
	if under, err := project.Expand(skip, project.IsSigil); err == nil {
		for _, f := range under {
			skipped[f] = true
		}
	}
	n := names{prefix: prefix, seen: map[string]bool{}}
	for _, f := range files {
		if f == "-" || skipped[f] {
			continue
		}
		src, err := os.ReadFile(f) //nolint:gosec // the path comes from the command line, which is the point
		if err != nil {
			continue
		}
		n.add(f, src)
	}
	return n.sorted()
}

// add collects the names of the policies in one file's headers that
// start with the prefix, each once.
func (n *names) add(file string, src []byte) {
	parsed, _ := parser.ParseFile(file, src)
	for _, doc := range parsed.Docs {
		d, ok := doc.(*ast.PolicyDoc)
		if !ok || d.Name == nil {
			continue
		}
		if name := d.Name.String(); strings.HasPrefix(name, n.prefix) && !n.seen[name] {
			n.seen[name] = true
			n.out = append(n.out, name)
		}
	}
}

// sorted returns the names collected, sorted.
func (n *names) sorted() []cobra.Completion {
	sort.Strings(n.out)
	return n.out
}

// trusted returns the command's --trusted paths, or nil when it has no
// such flag.
func trusted(cmd *cobra.Command) []string {
	paths, _ := cmd.Flags().GetStringSlice("trusted")
	return paths
}
