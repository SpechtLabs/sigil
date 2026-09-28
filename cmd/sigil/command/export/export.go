// Package export implements the `sigil export` command.
package export

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// What export found or did with the --out file.
const (
	statusCurrent = "current" // it already matched, so it was left alone
	statusWritten = "written" // it was missing or stale, and was rewritten
	statusStale   = "stale"   // --check found it stale
)

// Export is what export printed or did, as JSON and YAML print it.
type Export struct {
	Kind    string `json:"kind" yaml:"kind"`
	Version int    `json:"version" yaml:"version"`
	File    string `json:"file,omitempty" yaml:"file,omitempty"`     // the --out file
	Status  string `json:"status,omitempty" yaml:"status,omitempty"` // with --out: current, written or stale
	Source  string `json:"source" yaml:"source"`                     // the kind file, as the binary exports it
}

// NewCommand returns the export command.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "export [KIND]",
		SuggestFor: []string{"schema"},
		Short:      "Write the kind file of a kind linked into this binary",
		Long: `Writes the kind file of a kind a host linked into its own sigil binary: the
text its Schema() returns, which a policy repository checks in so the CLI, the
editor and other services can check policies without the host's code.

The stock sigil binary links no kind; build one with the pkg/cli package. KIND
names the kind to export when the binary links several.

--out writes the file instead of printing it, and leaves it untouched when it's
current. --check only compares, and fails when the file is stale, which is how
CI notices a kind change that wasn't exported.`,
		Example: `# Print the kind file
sigil export

# Keep the policy repository's copy current, e.g. from go generate
sigil export --out ../policies/deploy_approval.sigil

# Fail in CI when the checked-in kind file is stale
sigil export --check --out ../policies/deploy_approval.sigil`,
		Args: usage.AtMost(1, "KIND"),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, _ := cmd.Flags().GetString("out")
			check, _ := cmd.Flags().GetBool("check")
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return run(cmd.OutOrStdout(), o.kinds, name, out, check, *o.output)
		},
	}

	cmd.Flags().String("out", "", "Kind file to write, instead of printing it")
	cmd.Flags().Bool("check", false, "Only compare with the --out file, and fail when it's stale")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("out", "sigil")

	return cmd
}

func run(stdout io.Writer, kinds []project.Linked, name, out string, check bool, f output.Format) humane.Error {
	k, err := pick(kinds, name)
	if err != nil {
		return err
	}
	schema := []byte(k.Model.Source())
	if check && out == "" {
		return humane.New("--check needs the file to compare", "name the checked-in kind file with --out")
	}
	rec := Export{Kind: k.Model.Name, Version: k.Model.Version, File: out, Source: string(schema)}
	if out == "" {
		if f != output.Text {
			return output.Encode(stdout, f, rec)
		}
		if _, err := stdout.Write(schema); err != nil {
			return humane.Wrap(err, "the kind file couldn't be written", "check where the output is going")
		}
		return nil
	}
	current, rerr := os.ReadFile(out) //nolint:gosec // the path comes from the command line, which is the point
	switch {
	case rerr == nil && bytes.Equal(current, schema):
		rec.Status = statusCurrent
	case check:
		rec.Status = statusStale
	default:
		if err := os.WriteFile(out, schema, 0o644); err != nil { //nolint:gosec // a kind file is meant to be read by everyone
			return humane.Wrap(err, out+" couldn't be written", "check that its directory exists and is writable")
		}
		rec.Status = statusWritten
	}
	return report(stdout, f, rec)
}

// report says what export did with the --out file, and fails when
// --check found it stale.
func report(stdout io.Writer, f output.Format, rec Export) humane.Error {
	msg := rec.File + " is stale: it doesn't match the kind " + rec.Kind + " linked into this binary"
	help := "regenerate it with this binary's `export " + rec.Kind + " --out " + rec.File + "`"
	if f != output.Text {
		if err := output.Encode(stdout, f, rec); err != nil {
			return err
		}
		if rec.Status == statusStale {
			return pretty.Fail(msg, help)
		}
		return nil
	}
	p := pretty.New(stdout)
	switch rec.Status {
	case statusCurrent:
		return p.Ok(rec.File + " is up to date")
	case statusStale:
		if err := p.Fail(msg, help); err != nil {
			return err
		}
		return pretty.Fail(msg, help)
	}
	return p.Ok("wrote " + rec.File)
}

// pick returns the linked kind to export.
func pick(kinds []project.Linked, name string) (project.Linked, humane.Error) {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = k.Model.Name
		if k.Model.Name == name {
			return k, nil
		}
	}
	switch {
	case len(kinds) == 0:
		return project.Linked{}, humane.New("no kind is linked into this binary", "the stock sigil binary reads kinds from files; a host builds a binary with its kind linked in with the pkg/cli package")
	case name == "" && len(kinds) == 1:
		return kinds[0], nil
	case name == "":
		return project.Linked{}, humane.New("this binary links several kinds", "name the one to export: "+strings.Join(names, ", "))
	}
	return project.Linked{}, humane.New("no kind "+name+" is linked into this binary", "linked: "+strings.Join(names, ", "))
}
