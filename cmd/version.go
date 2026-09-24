package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCMD = &cobra.Command{
	Use:     "version",
	Short:   "Shows version information",
	Example: "cmap version",
	Args:    cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {

		switch outputType {
		case "text":
			if len(Version) > 0 {
				fmt.Printf("Version: %s\n", Version)
			}
			fmt.Printf("Built on '%s' by %s from commit %s\n", Date, BuiltBy, Commit)

		case "json":
			fmt.Printf("{\"version\": \"%s\", \"date\": \"%s\", \"commit\": \"%s\", \"builtBy\": \"%s\"}\n", Version, Date, Commit, BuiltBy)

		case "yaml":
			fmt.Printf("---\nversion: \"%s\"\ndate: \"%s\"\ncommit\": \"%s\"\nbuiltBy: \"%s\"\n", Version, Date, Commit, BuiltBy)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCMD)
}
