package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// listEntry is how a list command encodes a target as JSON or YAML.
type listEntry struct {
	Package string `json:"package" yaml:"package"`
	Name    string `json:"name"    yaml:"name"`
}

// List prints what a list command found. As text, each package's
// directory comes with its targets indented below it (or only the
// directories, with packagesOnly), and a count at the end. As JSON or YAML,
// it's an array of {package, name} objects, or of import paths with
// packagesOnly, e.g. for a CI matrix. noun and nouns name the targets.
func List(p *pretty.Printer, format output.Format, targets []gotool.Target, packagesOnly bool, noun, nouns string) humane.Error {
	var pkgs []string
	entries := make([]listEntry, 0, len(targets))
	for _, t := range targets {
		pkgs = append(pkgs, t.Package)
		entries = append(entries, listEntry{Package: t.Package, Name: t.Name})
	}
	pkgs = slices.Compact(pkgs)

	var v any = entries //nolint:emptyinterface // either list is encoded
	if packagesOnly {
		v = pkgs
	}
	switch format {
	case output.JSON:
		b, err := json.Marshal(v)
		if err != nil {
			return humane.Wrap(err, "can't encode the list as JSON", "this is a bug in devtool")
		}
		return p.Print(string(b) + "\n")
	case output.YAML:
		b, err := yaml.Marshal(v)
		if err != nil {
			return humane.Wrap(err, "can't encode the list as YAML", "this is a bug in devtool")
		}
		return p.Print("---\n" + string(b))
	default:
		th := p.Theme()
		var b strings.Builder
		for i, t := range targets {
			if i == 0 || targets[i-1].Package != t.Package {
				b.WriteString(th.Bold(t.Dir) + "\n")
			}
			if !packagesOnly {
				b.WriteString("  " + t.Name + "\n")
			}
		}
		b.WriteString(th.Muted(fmt.Sprintf("\n%d %s in %d %s\n",
			len(targets), Plural(len(targets), noun, nouns), len(pkgs), Plural(len(pkgs), "package", "packages"))))
		return p.Print(b.String())
	}
}
