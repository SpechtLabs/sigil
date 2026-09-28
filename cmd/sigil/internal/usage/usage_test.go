package usage_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/usage"
)

// root builds a command tree like sigil's, with every kind of usage
// error reachable: a required flag, an exclusive pair, a typed value and
// a parent with subcommands.
func root() *cobra.Command {
	r := &cobra.Command{Use: "sigil"}
	format := output.Text
	r.PersistentFlags().VarP(&format, "output", "o", "Output format")
	check := &cobra.Command{Use: "check PATH...", Args: usage.AtLeast(1, "PATH"), RunE: func(*cobra.Command, []string) error { return nil }}
	check.Flags().StringP("kind", "k", "", "Kind file to check against")
	check.Flags().String("bare", "", "")
	evalCmd := &cobra.Command{Use: "eval PATH...", Args: usage.AtLeast(1, "PATH"), RunE: func(*cobra.Command, []string) error { return nil }}
	evalCmd.Flags().StringP("input", "i", "", "Input document (required)")
	evalCmd.Flags().String("policy", "", "Policy to evaluate")
	_ = evalCmd.MarkFlagRequired("input")
	_ = evalCmd.MarkFlagRequired("policy")
	fmtCmd := &cobra.Command{Use: "fmt [PATH...]", Args: cobra.ArbitraryArgs, RunE: func(*cobra.Command, []string) error { return nil }}
	fmtCmd.Flags().Bool("write", false, "")
	fmtCmd.Flags().Bool("check", false, "")
	fmtCmd.MarkFlagsMutuallyExclusive("write", "check")
	gen := &cobra.Command{Use: "gen", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	gen.AddCommand(&cobra.Command{Use: "go KIND_FILE", Args: usage.Exactly("KIND_FILE"), RunE: func(*cobra.Command, []string) error { return nil }})
	export := &cobra.Command{Use: "export [KIND]", Args: usage.AtMost(1, "KIND"), RunE: func(*cobra.Command, []string) error { return nil }}
	version := &cobra.Command{Use: "version", Args: usage.None(), RunE: func(*cobra.Command, []string) error { return nil }}
	breaking := &cobra.Command{Use: "breaking OLD NEW", Args: usage.Exactly("OLD", "NEW"), RunE: func(*cobra.Command, []string) error { return nil }}
	r.AddCommand(check, evalCmd, fmtCmd, gen, export, version, breaking)
	r.SilenceUsage, r.SilenceErrors = true, true
	return r
}

// execute runs args through the tree and humanizes the error the way
// Execute does.
func execute(args ...string) humane.Error {
	r := root()
	r.SetArgs(args)
	r.SetOut(&strings.Builder{})
	r.SetErr(&strings.Builder{})
	return usage.Humanize(r, args, r.Execute())
}

func TestHumanize(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantMsg    string
		wantAdvice []string // each must appear, in order
	}{
		{name: "ok", args: []string{"check", "p.sigil"}},
		{name: "too few", args: []string{"check"}, wantMsg: "check needs at least one PATH", wantAdvice: []string{"usage: sigil check PATH... [flags]", "run `sigil check --help` for details"}},
		{name: "too many", args: []string{"export", "a", "b"}, wantMsg: "export takes at most one KIND, got 2"},
		{name: "none", args: []string{"version", "x", "y"}, wantMsg: "version takes no arguments, got 2"},
		{name: "exactly one", args: []string{"gen", "go"}, wantMsg: "gen go needs KIND_FILE, got 0"},
		{name: "exactly two", args: []string{"breaking", "a"}, wantMsg: "breaking needs OLD and NEW, got 1"},
		{name: "unknown flag", args: []string{"check", "--kidn", "k", "p"}, wantMsg: "unknown flag --kidn", wantAdvice: []string{"did you mean --kind?", "usage: sigil check PATH... [flags]"}},
		{name: "unknown flag without a close match", args: []string{"check", "--verbose", "p"}, wantMsg: "unknown flag --verbose", wantAdvice: []string{"usage: sigil check PATH... [flags]"}},
		{name: "unknown shorthand", args: []string{"check", "-x", "p"}, wantMsg: "unknown flag -x"},
		{name: "flag needs a value", args: []string{"check", "p", "--kind"}, wantMsg: "--kind needs a value", wantAdvice: []string{"--kind takes the kind file to check against"}},
		{name: "shorthand needs a value", args: []string{"check", "p", "-k"}, wantMsg: "--kind needs a value"},
		{name: "flag without usage text", args: []string{"check", "p", "--bare"}, wantMsg: "--bare needs a value", wantAdvice: []string{"--bare takes a string"}},
		{name: "invalid value with advice", args: []string{"check", "-o", "xml", "p"}, wantMsg: `"xml" isn't a valid value for --output`, wantAdvice: []string{"use one of: text, json, yaml"}},
		{name: "required flag", args: []string{"eval", "--policy", "p", "x"}, wantMsg: "--input is required", wantAdvice: []string{"--input takes the input document", "usage: sigil eval PATH... [flags]"}},
		{name: "required flags", args: []string{"eval", "x"}, wantMsg: "--input and --policy are required"},
		{name: "exclusive flags", args: []string{"fmt", "--write", "--check"}, wantMsg: "--check and --write can't be combined", wantAdvice: []string{"pass one of them"}},
		{name: "unknown command with a suggestion", args: []string{"chekc"}, wantMsg: `unknown command "chekc"`, wantAdvice: []string{"did you mean sigil check?", "run `sigil --help` to list the commands"}},
		{name: "unknown command without a suggestion", args: []string{"frobnicate"}, wantMsg: `unknown command "frobnicate"`, wantAdvice: []string{"run `sigil --help` to list the commands"}},
		{name: "unknown subcommand", args: []string{"gen", "rust"}, wantMsg: `sigil gen has no command "rust"`, wantAdvice: []string{"run `sigil gen --help` to list the commands"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := execute(tt.args...)
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("Humanize() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantMsg {
				t.Fatalf("Humanize() = %v, want %q", err, tt.wantMsg)
			}
			advice := strings.Join(err.Advice(), "\n")
			last := -1
			for _, want := range tt.wantAdvice {
				i := strings.Index(advice, want)
				if i < 0 || i < last {
					t.Errorf("advice %q is missing %q in order", advice, want)
				}
				last = i
			}
		})
	}
}

func TestValidatorsOnOtherShapes(t *testing.T) {
	r := &cobra.Command{Use: "prog", Args: usage.None(), RunE: func(*cobra.Command, []string) error { return nil }}
	r.SetArgs([]string{"x"})
	r.SilenceUsage, r.SilenceErrors = true, true
	if err := r.Execute(); err == nil || err.Error() != "prog takes no arguments, got 1" {
		t.Errorf("root None() = %v", err)
	}
	two := &cobra.Command{Use: "two PATH PATH...", Args: usage.AtLeast(2, "PATH"), RunE: func(*cobra.Command, []string) error { return nil }}
	two.SetArgs([]string{"a"})
	two.SilenceUsage, two.SilenceErrors = true, true
	if err := two.Execute(); err == nil || err.Error() != "two needs at least 2 PATHs" {
		t.Errorf("AtLeast(2) = %v", err)
	}
}

func TestHumanizePassesThrough(t *testing.T) {
	r := root()
	if got := usage.Humanize(r, nil, nil); got != nil {
		t.Errorf("Humanize(nil) = %v", got)
	}
	he := humane.New("mine", "keep it")
	if got := usage.Humanize(r, []string{"check"}, he); got != he {
		t.Errorf("Humanize(humane) = %v, want the same error", got)
	}
	got := usage.Humanize(r, []string{"check", "p"}, errors.New("something else"))
	if got == nil || got.Error() != "something else" || len(got.Advice()) != 2 || !strings.Contains(got.Advice()[1], "sigil check --help") {
		t.Errorf("Humanize(plain) = %v with advice %v", got, got.Advice())
	}
	if got := usage.Humanize(r, []string{"nope"}, errors.New("plain")); got == nil || !strings.Contains(got.Advice()[1], "`sigil --help`") {
		t.Errorf("Humanize(plain, unknown command) = %v, want root help advice", got)
	}
}

func TestInvalidValueWithoutAdvice(t *testing.T) {
	r := root()
	r.PersistentFlags().Int("n", 0, "a number")
	args := []string{"check", "--n", "x", "p"}
	r.SetArgs(args)
	r.SetOut(&strings.Builder{})
	r.SetErr(&strings.Builder{})
	err := usage.Humanize(r, args, r.Execute())
	if err == nil || err.Error() != `"x" isn't a valid value for --n` {
		t.Fatalf("Humanize() = %v", err)
	}
	if len(err.Advice()) == 0 || !strings.Contains(err.Advice()[0], "invalid syntax") {
		t.Errorf("advice = %v, want the parse error first", err.Advice())
	}
}
