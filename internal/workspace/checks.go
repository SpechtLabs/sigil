package workspace

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

// Checks configures [Project.Diagnose]: what `sigil check` reads from its
// flags and the configuration file.
type Checks struct {
	Lints    map[string]lint.Level // the level of each lint named; the others keep their defaults
	Patterns []string              // --policy: check only the policies they match, and what those use
	Require  []Requirement         // the policies each root must invoke
	// Strict says the run reads every policy the requirements' roots
	// name, as a run over the whole repository does, so a requirement
	// declared in a file whose policy or roots are missing is an error.
	Strict bool
}

// Requirement is one policy check enforces.
type Requirement struct {
	Policy  string   // the required policy's name
	Where   string   // where it's declared, such as sigil.yaml:3:5, for messages; empty for --require
	Trusted []string // files and directories it must come from; empty reads it from the paths
	Roots   []string // name patterns of the policies it applies to; empty for every root of its kind that no other policy invokes
}

// enforced is what the requirements ask of the roots: the roots, in
// order, and the required policies each one must invoke.
type enforced struct {
	requires map[string][]string // by root
	roots    []string
}

// Diagnose runs what `sigil check` does over the project and returns
// every diagnostic in scope, errors and lint findings, each naming its
// document: it checks every document against its kind, and, when that
// finds no error, compiles the policies in scope, checks each root
// against the policies it must invoke, and runs the lints at c's levels.
// A requirement that can't be enforced as written, or a pattern that
// matches no policy, fails it instead.
func (p *Project) Diagnose(c Checks) (diag.ErrorList, humane.Error) {
	p.Check()
	s := p.ScopeOf(nil)
	if len(c.Patterns) > 0 {
		selected, err := Match(p.Policies(), c.Patterns)
		if err != nil {
			return nil, err
		}
		s = p.ScopeOf(selected)
	}
	errs := s.Keep(p.Errors())
	if errs != nil {
		return errs, nil
	}
	// Lints read what the checker learned, so they run on a project
	// that checks, even when a compile or a requirement then fails.
	e, err := p.enforce(c.Require, s.Selected(), c.Strict)
	if err != nil {
		return nil, err
	}
	errs = s.Keep(p.compileAll(s, e))
	required := make([]string, len(c.Require))
	for i, r := range c.Require {
		required[i] = r.Policy
	}
	var findings diag.ErrorList
	for _, g := range p.Groups() {
		for _, f := range lint.Run(g.Bundle, lint.Options{Kind: g.Kind.Model, Levels: c.Lints, Required: required}) {
			findings = append(findings, f.Error)
		}
	}
	return append(errs, s.Keep(findings)...), nil
}

// compileAll compiles the scope's policies, so errors only a compile
// finds, such as an invocation argument out of its param's bounds, fail
// the check, and checks each root against the policies it must invoke.
func (p *Project) compileAll(s *Scope, e *enforced) diag.ErrorList {
	var errs diag.ErrorList
	seen := map[string]bool{}
	add := func(list diag.ErrorList) {
		for _, e := range list {
			key := fmt.Sprintf("%s:%d:%s", e.File, e.Pos.Offset, e.Msg)
			if !seen[key] {
				seen[key] = true
				errs = append(errs, e)
			}
		}
	}
	for _, name := range s.Policies() {
		_, list := s.Bundle(p.Group(name)).Compile(name, bundle.Options{Static: true})
		add(list)
	}
	if errs != nil {
		return errs
	}
	for _, root := range e.roots {
		_, list := s.Bundle(p.Group(root)).Compile(root, bundle.Options{Static: true, Require: e.requires[root]})
		add(list)
	}
	return errs
}

// enforce works out which roots each requirement applies to: those its
// roots: or --policy patterns match, or else every policy no other one
// invokes, apart from the required ones, and only roots of the required
// policy's own kind. A required name no document defines applies to
// every root, which then reports that it doesn't invoke it; from the
// file it's declared in it's an error at the entry instead, unless the run
// isn't strict and the entry has no trusted: paths, when the entry is
// skipped, its policy being among paths this run doesn't read. When
// strict, a roots: pattern that matches nothing is an error too, and so
// is an entry whose roots: match no policy of its kind; otherwise its
// roots: pick among the policies the run read. selected, the policies
// --policy matched, narrows every requirement's roots to those among
// them, so a requirement whose roots it leaves out is skipped.
func (p *Project) enforce(reqs []Requirement, selected []string, strict bool) (*enforced, humane.Error) {
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
		fromFile := r.Where != ""
		if owner == nil && fromFile {
			if strict || len(r.Trusted) > 0 {
				return nil, undefined(p, r)
			}
			continue // read from the paths, and not among the part of them this run reads
		}
		if err := origin(owner, r); err != nil {
			return nil, err
		}
		roots := uninvoked
		if len(r.Roots) > 0 {
			var err humane.Error
			if roots, err = matchRoots(p, r, strictRoots); err != nil {
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
				fmt.Sprintf("%s: roots of %s match no %s policy", r.Where, r.Policy, kind),
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
func matchRoots(p *Project, r Requirement, strict bool) ([]string, humane.Error) {
	var roots []string
	for _, pattern := range r.Roots {
		matched, err := Match(p.Policies(), []string{pattern})
		if err != nil && strict {
			return nil, humane.New(fmt.Sprintf("%s: roots of %s: %s", r.Where, r.Policy, err.Error()), err.Advice()...)
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
func defaultRoots(p *Project, requires []string) []string {
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
func undefined(p *Project, r Requirement) humane.Error {
	advice := []string{"check reads a required policy from the entry's trusted: paths, or else from its paths"}
	if near, ok := diag.Nearest(r.Policy, p.Names()); ok {
		advice = append([]string{fmt.Sprintf("did you mean %q?", near)}, advice...)
	}
	return humane.New(fmt.Sprintf("%s: %s is required, but no policy %s was found", r.Where, r.Policy, r.Policy), advice...)
}

// origin checks that a requirement with trusted paths finds its policy
// there, as the host's policy.From does: in the trusted source, below
// one of this entry's own paths. Without it, a team could delete the
// platform's guardrails and define a harmless policy of the same name,
// and the check would pass. g owns the required policy.
func origin(g *Group, r Requirement) humane.Error {
	if g == nil || len(r.Trusted) == 0 {
		return nil
	}
	d := g.Bundle.Document(r.Policy)
	if d.Trusted && below(d.File, r.Trusted) {
		return nil
	}
	where := "--require " + r.Policy
	if r.Where != "" {
		where = r.Where
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
