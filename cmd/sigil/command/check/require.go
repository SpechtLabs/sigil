package check

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/diag"
)

// enforced is what the requirements ask of the roots: the roots, in
// order, and the required policies each one must invoke.
type enforced struct {
	roots    []string
	requires map[string][]string // by root
}

// configure reads the configuration, from configFile or the nearest
// configuration file, and applies it to src: its kind files, and the trusted
// paths of the requirements the run enforces, which it returns: the
// flags' when --require is given, and otherwise the file's. --trusted
// without --require is an error: it says where required policies come
// from, and on its own would read documents as trusted that no
// requirement vouches for.
func configure(configFile string, src *project.Sources, patterns, requires []string) (*config.Config, []config.Require, humane.Error) {
	if len(src.Trusted) > 0 && len(requires) == 0 {
		return nil, nil, humane.New("--trusted names where required policies come from; pass --require, or set require: in the configuration file", "the require: entries of "+config.Names+" take the trusted paths of each required policy, as trusted:")
	}
	cfg, err := config.Load(configFile, ".")
	if err != nil {
		return nil, nil, err
	}
	kinds, err := cfg.KindFiles()
	if err != nil {
		return nil, nil, err
	}
	src.Kinds = append(src.Kinds, kinds...)
	reqs := cfg.Requirements(requires, src.Trusted, patterns)
	if src.Trusted, err = cfg.Trusted(src.Trusted, reqs); err != nil {
		return nil, nil, err
	}
	return cfg, reqs, nil
}

// enforce works out which roots each requirement applies to: those its
// roots: or --policy patterns match, or else every policy no other one
// invokes, apart from the required ones, and only roots of the required
// policy's own kind. A required name no document defines applies to
// every root, which then reports that it doesn't invoke it; from the
// configuration file it's an error at the entry instead, unless the run
// isn't strict and the entry has no trusted: paths, when the entry is
// skipped, its policy being among paths this run doesn't read. When
// strict, a roots: pattern that matches nothing is an error too, and so
// is an entry whose roots: match no policy of its kind; otherwise its
// roots: pick among the policies the run read. selected, the policies
// --policy matched, narrows every requirement's roots to those among
// them, so a requirement whose roots it leaves out is skipped.
func enforce(p *project.Project, cfg *config.Config, reqs []config.Require, selected []string, strict bool) (*enforced, humane.Error) {
	// --policy narrows the roots, so a roots: pattern for policies it
	// leaves out isn't an error.
	strictRoots := strict && selected == nil
	e := &enforced{requires: map[string][]string{}}
	names := make([]string, len(reqs))
	for i, r := range reqs {
		names[i] = r.Policy
	}
	uninvoked := defaultRoots(p, names)
	for _, r := range reqs {
		owner := p.Group(r.Policy)
		fromFile := r.Pos.Line > 0
		if owner == nil && fromFile {
			if strict || len(r.Trusted) > 0 {
				return nil, undefined(p, cfg, r)
			}
			continue // read from the paths, and not among the part of them this run reads
		}
		if err := origin(cfg, owner, r); err != nil {
			return nil, err
		}
		roots := uninvoked
		if len(r.Roots) > 0 {
			var err humane.Error
			if roots, err = matchRoots(p, cfg, r, strictRoots); err != nil {
				return nil, err
			}
		}
		if selected != nil {
			roots = slices.DeleteFunc(slices.Clone(roots), func(root string) bool { return !slices.Contains(selected, root) })
		}
		applied := false
		for _, root := range roots {
			if owner == nil || p.Group(root) == owner {
				e.add(root, r.Policy)
				applied = true
			}
		}
		if !applied && len(r.Roots) > 0 && fromFile && strictRoots {
			kind := owner.Kind.Model.Name
			return nil, humane.New(
				fmt.Sprintf("%s: roots of %s match no %s policy", cfg.At(r.Pos), r.Policy, kind),
				fmt.Sprintf("a required policy applies to the roots of its own kind; name %s policies in roots:", kind),
			)
		}
	}
	return e, nil
}

// matchRoots returns the policies an entry's roots: patterns match. When
// strict, a pattern that matches nothing is an error at the entry;
// otherwise it picks nothing. --policy patterns matched before, for the
// scope, so only a roots: entry can fail to match here.
func matchRoots(p *project.Project, cfg *config.Config, r config.Require, strict bool) ([]string, humane.Error) {
	var roots []string
	for _, pattern := range r.Roots {
		matched, err := project.Match(p.Policies(), []string{pattern})
		if err != nil && strict {
			return nil, humane.New(fmt.Sprintf("%s: roots of %s: %s", cfg.At(r.Pos), r.Policy, err.Error()), err.Advice()...)
		}
		roots = append(roots, matched...)
	}
	return roots, nil
}

// add records that root must invoke required.
func (e *enforced) add(root, required string) {
	if slices.Contains(e.requires[root], required) {
		return // two roots: patterns matched it
	}
	if _, ok := e.requires[root]; !ok {
		e.roots = append(e.roots, root)
	}
	e.requires[root] = append(e.requires[root], required)
}

// defaultRoots returns every policy no other one invokes, apart from the
// required ones.
func defaultRoots(p *project.Project, requires []string) []string {
	invoked := map[string]bool{}
	for _, g := range p.Groups() {
		for _, d := range g.Bundle.Documents() {
			if d.Info == nil {
				continue
			}
			for _, target := range d.Info.Invocations {
				invoked[target] = true
			}
		}
	}
	var out []string
	for _, name := range p.Policies() {
		if !invoked[name] && !slices.Contains(requires, name) {
			out = append(out, name)
		}
	}
	return out
}

// undefined is the error for a policy the configuration file requires
// that no document defines.
func undefined(p *project.Project, cfg *config.Config, r config.Require) humane.Error {
	advice := []string{"check reads a required policy from the entry's trusted: paths, or else from its paths"}
	if near, ok := diag.Nearest(r.Policy, p.Names()); ok {
		advice = append([]string{fmt.Sprintf("did you mean %q?", near)}, advice...)
	}
	return humane.New(fmt.Sprintf("%s: %s is required, but no policy %s was found", cfg.At(r.Pos), r.Policy, r.Policy), advice...)
}

// whole reports whether the run reads the whole repository the
// configuration file configures: one of the paths is the file's directory
// or above it. A run that reads part of it, such as one team's directory,
// doesn't hold every policy the file's roots: name.
func whole(cfg *config.Config, paths []string) bool {
	dir, err := filepath.Abs(filepath.Dir(cfg.File))
	if err != nil {
		return true
	}
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil || path == "-" {
			continue
		}
		if rel, err := filepath.Rel(abs, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// origin checks that a requirement with trusted paths finds its policy
// there, as the host's policy.From does: in the trusted source, below
// one of this entry's own paths. Without it, a team could delete the
// platform's guardrails and define a harmless policy of the same name,
// and the check would pass. g owns the required policy.
func origin(cfg *config.Config, g *project.Group, r config.Require) humane.Error {
	if g == nil || len(r.Trusted) == 0 {
		return nil
	}
	d := g.Bundle.Document(r.Policy)
	if d.Trusted && below(d.File, r.Trusted) {
		return nil
	}
	where := "--require " + r.Policy
	if r.Pos.Line > 0 {
		where = cfg.At(r.Pos)
	}
	return humane.New(
		fmt.Sprintf("%s: %s must come from %s, but it's defined at %s", where, r.Policy, strings.Join(r.Trusted, ", "), d.File),
		"the host reads a required policy only from its trusted source, as policy.From does; define it there, and remove the other definition",
	)
}

// below reports whether file is one of paths or below one of them.
func below(file string, paths []string) bool {
	f, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	for _, p := range paths {
		dir, err := filepath.Abs(p)
		if err == nil && (f == dir || strings.HasPrefix(f, dir+string(filepath.Separator))) {
			return true
		}
	}
	return false
}
