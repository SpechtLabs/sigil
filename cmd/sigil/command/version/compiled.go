package version

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// compiledInfo is what a compiled binary's version reports as JSON: the
// build's info, and the bundle's under bundle.
type compiledInfo struct {
	info

	Bundle *bundleInfo `json:"bundle"`
}

// bundleInfo describes the bundle compiled into a binary.
type bundleInfo struct {
	Digest     string        `json:"digest" yaml:"digest"`
	Root       string        `json:"root,omitempty" yaml:"root,omitempty"` // the policy eval and explain default to
	Kinds      []string      `json:"kinds" yaml:"kinds"`                   // Name@Version of each kind the documents are written against
	Files      int           `json:"files" yaml:"files"`                   // kind files, documents and trusted files, each once
	Require    []requirement `json:"require,omitempty" yaml:"require,omitempty"`
	CompiledBy string        `json:"compiledBy" yaml:"compiledBy"`           // the version of the sigil that compiled it
	Built      string        `json:"built,omitempty" yaml:"built,omitempty"` // RFC 3339, UTC
}

// requirement is a policy the compile enforced, and the policies it
// applies to.
type requirement struct {
	Policy string   `json:"policy" yaml:"policy"`
	Roots  []string `json:"roots,omitempty" yaml:"roots,omitempty"` // name patterns; every policy when empty
}

// newCompiledCommand returns the version command of a compiled binary,
// which describes the bundle compiled into it too.
func newCompiledCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show what was compiled into this binary, and its build information",
		Long: `Shows what was compiled into this binary: the digest that identifies the
bundle of policies, the policy eval evaluates, the kinds the policies are
written against, how many files the bundle holds, the policies it requires, and
the version of the sigil that compiled it, and when.

Above that, it shows the release version, commit, Go version and platform of the
sigil this binary is built on.`,
		Example: fmt.Sprintf(`# Show what was compiled in, and by which sigil
%[1]s version

# Print the bundle's digest, e.g. to check a deploy runs the reviewed policies
%[1]s version -o json | jq -r .bundle.digest`, o.name),
		Args:              usage.None(),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.OutOrStdout(), *o)
		},
	}
}

// newBundleInfo describes the bundle compiled into the binary. It loads
// the bundle to find its kinds.
func newBundleInfo(o options) (*bundleInfo, humane.Error) {
	b := &o.payload.Bundle
	p, err := project.LoadFiles(project.FromBundle(b), o.kinds)
	if err != nil {
		return nil, err
	}
	kinds := []string{}
	for _, g := range p.Groups() {
		kinds = append(kinds, fmt.Sprintf("%s@%d", g.Kind.Model.Name, g.Kind.Model.Version))
	}
	files := map[string]bool{}
	for _, f := range slices.Concat(b.Kinds, b.Paths, b.Trusted) {
		files[f.Name] = true
	}
	info := &bundleInfo{
		Digest:     b.Digest(),
		Root:       b.Root,
		Kinds:      kinds,
		Files:      len(files),
		CompiledBy: o.payload.Sigil,
	}
	for _, r := range b.Require {
		info.Require = append(info.Require, requirement{Policy: r.Policy, Roots: r.Roots})
	}
	if !o.payload.Built.IsZero() {
		info.Built = o.payload.Built.UTC().Format(time.RFC3339)
	}
	return info, nil
}

// rows lists the bundle for the text report.
func (b *bundleInfo) rows() []pretty.KV {
	root := b.Root
	if root == "" {
		root = "none; eval takes --policy"
	}
	rows := []pretty.KV{
		{Key: "Bundle", Value: b.Digest},
		{Key: "Root policy", Value: root},
		{Key: "Kinds", Value: strings.Join(b.Kinds, ", ")},
		{Key: "Files", Value: fmt.Sprint(b.Files)},
	}
	for _, r := range b.Require {
		roots := "every policy"
		if len(r.Roots) > 0 {
			roots = strings.Join(r.Roots, ", ")
		}
		rows = append(rows, pretty.KV{Key: "Requires", Value: r.Policy + " for " + roots})
	}
	rows = append(rows, pretty.KV{Key: "Compiled by", Value: b.CompiledBy})
	if b.Built != "" {
		rows = append(rows, pretty.KV{Key: "Built", Value: b.Built})
	}
	return rows
}

// yaml renders the bundle as the YAML report's bundle: key.
func (b *bundleInfo) yaml() string {
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	// Strings, ints and lists of them always encode, into a builder
	// that never fails to write.
	_ = enc.Encode(map[string]*bundleInfo{"bundle": b})
	return out.String()
}
