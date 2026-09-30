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
)

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
	seen := map[string]bool{}
	var out []cobra.Completion
	for _, f := range files {
		if f == "-" || skipped[f] {
			continue
		}
		src, err := os.ReadFile(f) //nolint:gosec // the path comes from the command line, which is the point
		if err != nil {
			continue
		}
		parsed, _ := parser.ParseFile(f, src)
		for _, doc := range parsed.Docs {
			d, ok := doc.(*ast.PolicyDoc)
			if !ok || d.Name == nil {
				continue
			}
			if name := d.Name.String(); strings.HasPrefix(name, prefix) && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// trusted returns the command's --trusted paths, or nil when it has no
// such flag.
func trusted(cmd *cobra.Command) []string {
	paths, _ := cmd.Flags().GetStringSlice("trusted")
	return paths
}
