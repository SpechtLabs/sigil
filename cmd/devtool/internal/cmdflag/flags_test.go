package cmdflag

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
)

var names = Names{Things: "benchmarks", Unit: "sample", CPU: "GOMAXPROCS for every sample"}

func TestRun(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Run
	}{
		{
			name: "defaults",
			want: Run{Time: "200ms", CPU: 2, Timeout: 3 * time.Minute, Results: "benchmark-results"},
		},
		{
			name: "every flag",
			args: []string{"-f", "Lexer", "--time", "1s", "--cpu", "4", "--timeout", "1m", "-v", "--results", "out"},
			want: Run{Filter: "Lexer", Time: "1s", CPU: 4, Timeout: time.Minute, Verbose: true, Results: "out"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Run{Time: "200ms", CPU: 2, Timeout: 3 * time.Minute, Results: "benchmark-results"}
			cmd := command()
			r.Register(cmd, names)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if r != tt.want {
				t.Errorf("flags = %+v, want %+v", r, tt.want)
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	var r Run
	cmd := command()
	r.Register(cmd, names)
	tests := []struct {
		flag, shorthand, usage string
	}{
		{flag: "filter", shorthand: "f", usage: "Regular expression selecting the benchmarks by name"},
		{flag: "time", usage: "Time or iterations per sample, e.g. 1s or 100x"},
		{flag: "cpu", usage: "GOMAXPROCS for every sample"},
		{flag: "timeout", usage: "Timeout for each go test process"},
		{flag: "verbose", shorthand: "v", usage: "Print go test's output instead of a status line"},
		{flag: "results", usage: "Directory for the logs, the summary and the run's metadata"},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flag)
			if f == nil || f.Shorthand != tt.shorthand || f.Usage != tt.usage {
				t.Errorf("--%s = %+v, want -%s and %q", tt.flag, f, tt.shorthand, tt.usage)
			}
		})
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    List
		wantErr string
	}{
		{name: "defaults", want: List{Format: output.Text}},
		{name: "every flag", args: []string{"-f", "Fuzz", "--packages", "-o", "json"}, want: List{Filter: "Fuzz", Packages: true, Format: output.JSON}},
		{name: "unknown format", args: []string{"-o", "xml"}, wantErr: "unsupported output type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l List
			cmd := command()
			l.Register(cmd, names)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if l != tt.want {
				t.Errorf("flags = %+v, want %+v", l, tt.want)
			}
			if got := cmd.Flags().Lookup("packages").Usage; got != "List each package once instead of each of its benchmarks" {
				t.Errorf("--packages usage = %q", got)
			}
		})
	}
}

func command() *cobra.Command {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	return cmd
}
