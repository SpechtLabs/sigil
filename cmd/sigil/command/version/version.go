// Package version implements the `sigil version` command.
package version

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/pretty"
)

const unknown = "unknown"

// info is what the version command reports.
type info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	CommitTime string `json:"commitTime"`
	Dirty      bool   `json:"dirty"`
	GoVersion  string `json:"goVersion"`
	Platform   string `json:"platform"`
}

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
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.OutOrStdout(), *o)
		},
	}
}

// newInfo combines the injected version with the VCS and toolchain details
// the Go toolchain embeds in the binary. `go run` and `go test` do not embed
// VCS details, so those fields fall back to "unknown".
func newInfo(o options) info {
	i := info{
		Version:    o.version,
		Commit:     unknown,
		CommitTime: unknown,
		GoVersion:  unknown,
		Platform:   unknown,
	}

	bi := o.buildInfo
	if bi == nil {
		if i.Version == "" {
			i.Version = unknown
		}
		return i
	}

	if i.Version == "" {
		i.Version = bi.Main.Version
	}
	if bi.GoVersion != "" {
		i.GoVersion = bi.GoVersion
	}

	var goos, goarch string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			i.Commit = s.Value
		case "vcs.time":
			i.CommitTime = s.Value
		case "vcs.modified":
			i.Dirty = s.Value == "true"
		case "GOOS":
			goos = s.Value
		case "GOARCH":
			goarch = s.Value
		}
	}
	if goos != "" && goarch != "" {
		i.Platform = goos + "/" + goarch
	}

	return i
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
