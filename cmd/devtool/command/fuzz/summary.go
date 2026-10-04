package fuzz

import (
	"io"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
)

func newSummaryCommand(_ options) *cobra.Command {
	return &cobra.Command{
		Use:   "summary DIR...",
		Short: "Merge a campaign's fuzz results into one Markdown summary",
		Long: `Reads the results.json fuzz run wrote in each of a campaign's jobs, from
anywhere under the directories, and prints one Markdown summary of them: how
many targets passed, the failures with the command that replays each, and a
table of every target, failures first.

The extended fuzzing workflow adds it to the run's summary page and to the
issue for a failed campaign.`,
		Example: `devtool fuzz summary results >> "$GITHUB_STEP_SUMMARY"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return summary(cmd.OutOrStdout(), args)
		},
	}
}

func summary(w io.Writer, dirs []string) humane.Error {
	runs, err := readResults(dirs)
	if err != nil {
		return err
	}
	if _, werr := io.WriteString(w, summaryMarkdown(runs)); werr != nil {
		return humane.Wrap(werr, "can't write the summary", "check where the output goes")
	}
	return nil
}
