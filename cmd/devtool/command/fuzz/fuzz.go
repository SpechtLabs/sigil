// Package fuzz implements `devtool fuzz`: fuzzing every Go fuzz target one
// at a time, listing them, sharing the inputs fuzzing found, and reporting
// a failed extended campaign.
//
// `fuzz run` restores the corpus from the fuzz-corpus branch, then runs go
// test -fuzz on each target for --time, one after the other, and stops at
// the first failure; go test saves the failing input under the package's
// testdata/fuzz directory. `fuzz corpus pull` and `fuzz corpus push` move
// the corpus between go test's cache and the branch. `fuzz list` shows what
// `fuzz run` would fuzz, and with --packages -o json feeds CI's matrix.
// `fuzz report` opens or comments on the GitHub issue for a failed
// scheduled or manually dispatched campaign on main.
package fuzz

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
)

// envPrefix starts the environment variable each flag can be set with.
const envPrefix = "FUZZ"

var (
	// names describes the fuzz commands' flags.
	names = cmdflag.Names{
		Things: "fuzz targets",
		Unit:   "target",
		CPU:    "CPUs for every target, as fuzzing workers",
	}

	// Command groups for `devtool fuzz --help`: what a developer runs, and
	// what only CI does.
	groupDev = &cobra.Group{ID: "dev", Title: "Commands"}
	groupCI  = &cobra.Group{ID: "ci", Title: "CI commands"}
)

// NewCommand returns the fuzz command with its subcommands.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:   "fuzz",
		Short: "Run the Go fuzz targets",
		Long: `Every Fuzz function in the module is a fuzz target. fuzz run fuzzes them one
at a time, starting from the inputs on the fuzz-corpus branch, fuzz corpus
shares the inputs a run found, fuzz list shows what it would run, and fuzz
report files the issue for a failed extended campaign in CI.

Every flag can also be set with an environment variable: FUZZ_ followed by
the flag's name in capitals, e.g. FUZZ_TIME=1m for --time 1m.`,
	}
	cmd.AddGroup(groupDev, groupCI)
	run, list, corpus, report := newRunCommand(*o), newListCommand(*o), newCorpusCommand(*o), newReportCommand(*o)
	run.GroupID, list.GroupID, corpus.GroupID, report.GroupID = groupDev.ID, groupDev.ID, groupDev.ID, groupCI.ID
	cmd.AddCommand(run, list, corpus, report)
	return cmd
}
