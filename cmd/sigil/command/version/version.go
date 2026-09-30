// Package version implements the `sigil version` command. It reports the
// release version set with [WithVersion], and the commit, commit time,
// dirty state, Go version and platform from the build info the Go
// toolchain embeds in the binary.
package version

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/internal/buildinfo"
)

const unknown = buildinfo.Unknown

// info is what the version command reports.
type info = buildinfo.Info

// NewCommand returns the version command.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	return &cobra.Command{
		Use:   "version",
		Short: "Show version and build information",
		Long: `Shows the release version, plus the commit, commit time, Go version and
platform the binary was built with.

The commit details come from the version control information the Go toolchain
embeds in every build. Binaries built with go run don't carry it, so they report
those fields as unknown.`,
		Example: `# Show version information
sigil version

# Print it as JSON, e.g. to paste into a bug report
sigil version -o json`,
		Args:              usage.None(),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.OutOrStdout(), *o)
		},
	}
}

// newInfo combines the injected version with the VCS and toolchain details
// the Go toolchain embeds in the binary, as [buildinfo.New] does.
func newInfo(o options) info {
	return buildinfo.New(o.version, o.buildInfo)
}

func run(w io.Writer, o options) error {
	i := newInfo(o)

	var out string
	switch *o.output {
	case output.Text:
		commit := i.Commit
		if i.Dirty {
			commit += " (dirty)"
		}
		return pretty.New(w).KeyValues("sigil",
			pretty.KV{Key: "Version", Value: i.Version},
			pretty.KV{Key: "Commit", Value: commit},
			pretty.KV{Key: "Commit time", Value: i.CommitTime},
			pretty.KV{Key: "Go version", Value: i.GoVersion},
			pretty.KV{Key: "Platform", Value: i.Platform},
		)

	case output.JSON:
		b, err := json.Marshal(i)
		if err != nil {
			return humane.Wrap(err, "failed to encode version information as JSON", "this is a bug in sigil, please report it")
		}
		out = string(b) + "\n"

	case output.YAML:
		out = fmt.Sprintf("---\nversion: %q\ncommit: %q\ncommitTime: %q\ndirty: %t\ngoVersion: %q\nplatform: %q\n",
			i.Version, i.Commit, i.CommitTime, i.Dirty, i.GoVersion, i.Platform)

	default:
		return humane.New(
			fmt.Sprintf("unsupported output type %q", *o.output),
			"use one of: "+strings.Join(output.Formats, ", "),
		)
	}

	if _, err := io.WriteString(w, out); err != nil {
		return humane.Wrap(err, "failed to write version information", "check that stdout is writable (e.g. not a closed pipe)")
	}
	return nil
}
