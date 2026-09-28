// Package project loads what the policy commands work on: the kind, from
// a kind file or linked into a host's own sigil binary, and the bundle of
// documents named on the command line.
package project

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Kind is the contract a command checks and evaluates against.
type Kind struct {
	Model   *kind.Kind
	Binding *gokind.Binding
	// Host is set for a kind linked into a host binary, whose host
	// functions are implemented. A kind loaded from a file has every
	// function bound to one that fails when it's called.
	Host bool
}

// Linked is a kind a host linked into its own sigil binary.
type Linked struct {
	Model   *kind.Kind
	Binding *gokind.Binding
}

// Sources are the command-line paths a bundle is read from.
type Sources struct {
	Stdin     io.Reader
	Paths     []string
	Trusted   []string // read into a trusted bundle, as policy.From does
	Recursive bool
}

// LoadKind returns the kind a command works against. A kind file named
// with --kind that matches a linked kind by name must match it exactly,
// which catches an export that wasn't regenerated, and then evaluates
// with the host's functions. Any other kind file is loaded on its own.
// Without a file, the binary's one linked kind is used.
func LoadKind(file string, linked []Linked) (*Kind, humane.Error) {
	if file == "" {
		switch len(linked) {
		case 0:
			return nil, humane.New("no kind file given", "pass the kind file the policies are written against with --kind")
		case 1:
			return &Kind{Model: linked[0].Model, Binding: linked[0].Binding, Host: true}, nil
		}
		names := make([]string, len(linked))
		for i, l := range linked {
			names[i] = l.Model.Name
		}
		return nil, humane.New("this binary links several kinds", "name the kind file with --kind; linked: "+strings.Join(names, ", "))
	}
	src, err := os.ReadFile(file) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return nil, humane.Wrap(err, "the kind file couldn't be read", "pass the exported kind file with --kind")
	}
	k, errs := check.LoadKind(file, src)
	if errs != nil {
		return nil, humane.New(errs.Error(), "fix the kind file, or regenerate it from the host's Schema()")
	}
	for _, l := range linked {
		if l.Model.Name != k.Name {
			continue
		}
		if l.Model.Source() != k.Source() {
			return nil, humane.New(
				fmt.Sprintf("%s doesn't match the kind %s linked into this binary", file, k.Name),
				"regenerate the kind file with this binary's `export "+k.Name+" --out "+file+"`, or rebuild the binary from the host's current code",
			)
		}
		return &Kind{Model: l.Model, Binding: l.Binding, Host: true}, nil
	}
	return &Kind{Model: k, Binding: gokind.Synthesize(k)}, nil
}

// Bundle reads the sources into a bundle for k. Parse errors stay in the
// bundle, for the command to report with everything else; only paths
// that can't be read fail here.
func (k *Kind) Bundle(s Sources) (*bundle.Bundle, humane.Error) {
	b := bundle.New(k.Model)
	if len(s.Trusted) > 0 {
		t := bundle.New(k.Model)
		if err := t.LoadPaths(s.Trusted, true, s.Stdin); err != nil {
			return nil, err
		}
		b.Trust(t)
	}
	if err := b.LoadPaths(s.Paths, s.Recursive, s.Stdin); err != nil {
		return nil, err
	}
	return b, nil
}

// Match returns the policies matching any of the patterns, in the order
// given. A pattern is a name, or a name with `*` matching any run of
// characters, dots included. Every pattern must match something.
func Match(policies, patterns []string) ([]string, humane.Error) {
	seen := map[string]bool{}
	var out []string
	for _, pattern := range patterns {
		matched := matching(policies, pattern)
		if len(matched) == 0 {
			help := "the bundle defines no policies"
			if len(policies) > 0 {
				help = "the bundle defines: " + strings.Join(policies, ", ")
			}
			return nil, humane.New(fmt.Sprintf("no policy matches %q", pattern), help)
		}
		for _, p := range matched {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// matching returns the policies one pattern matches.
func matching(policies []string, pattern string) []string {
	re := regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$")
	var out []string
	for _, p := range policies {
		if re.MatchString(p) {
			out = append(out, p)
		}
	}
	return out
}

// Root picks the one policy eval evaluates: the one named, or the
// bundle's only policy.
func Root(policies []string, name string) (string, humane.Error) {
	if name != "" {
		roots, err := Match(policies, []string{name})
		if err != nil {
			return "", err
		}
		if len(roots) > 1 {
			return "", humane.New(fmt.Sprintf("%q matches %d policies", name, len(roots)), "name one policy: "+strings.Join(roots, ", "))
		}
		return roots[0], nil
	}
	switch len(policies) {
	case 0:
		return "", humane.New("the bundle holds no policies", "name a file or directory that holds one")
	case 1:
		return policies[0], nil
	}
	return "", humane.New("the bundle holds several policies", "name the one to evaluate with --policy: "+strings.Join(policies, ", "))
}
