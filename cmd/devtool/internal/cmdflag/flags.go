// Package cmdflag declares the flags devtool's commands share, so every run
// command and every list command takes the same flags, spelled the same
// way, with the same help.
package cmdflag

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
)

// Run holds the flags every run command takes.
type Run struct {
	Filter  string
	Time    string
	CPU     int
	Timeout time.Duration
	Verbose bool
	Results string
}

// List holds the flags every list command takes.
type List struct {
	Filter   string
	Packages bool
	Format   output.Format
}

// Names says what a command's flags apply to, for their help.
type Names struct {
	// Things names what the command runs, e.g. "benchmarks".
	Things string
	// Unit is what --time applies to, e.g. "sample".
	Unit string
	// CPU says what --cpu sets, e.g. "CPUs for every sample, as GOMAXPROCS".
	CPU string
}

// Register adds the run flags to cmd, with r's values as their defaults.
func (r *Run) Register(cmd *cobra.Command, n Names) {
	f := cmd.Flags()
	f.StringVarP(&r.Filter, "filter", "f", r.Filter, "Regular expression selecting the "+n.Things+" by name")
	f.StringVar(&r.Time, "time", r.Time, "Time or iterations per "+n.Unit+", e.g. 1s or 100x")
	f.IntVar(&r.CPU, "cpu", r.CPU, n.CPU)
	f.DurationVar(&r.Timeout, "timeout", r.Timeout, "Timeout for each go test process")
	f.BoolVarP(&r.Verbose, "verbose", "v", r.Verbose, "Print go test's output instead of a status line")
	f.StringVar(&r.Results, "results", r.Results, "Directory for the logs, the summary and the run's metadata")
}

// Register adds the list flags to cmd.
func (l *List) Register(cmd *cobra.Command, n Names) {
	l.Format = output.Text
	f := cmd.Flags()
	f.StringVarP(&l.Filter, "filter", "f", "", "Regular expression selecting the "+n.Things+" by name")
	f.BoolVar(&l.Packages, "packages", false, "List each package once instead of each of its "+n.Things)
	f.VarP(&l.Format, "output", "o", "Output format: "+strings.Join(output.Formats, ", "))
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(output.Formats, cobra.ShellCompDirectiveNoFileComp))
}
