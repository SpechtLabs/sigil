// Package complete holds shell completion functions shared by sigil commands.
package complete

import "github.com/spf13/cobra"

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
