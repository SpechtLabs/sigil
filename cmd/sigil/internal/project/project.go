// Package project loads what the policy commands work on: every document
// the command line names, read and parsed once, and grouped by the kind
// each one's header names.
//
// [Load] reads the paths, finds the kinds among the linked ones, the
// --kind files and the inputs themselves, and returns a [Project] with one
// [Group] per kind: the kind and a bundle of that kind's documents. A name
// has one definition across the whole project, across kinds too. [Match]
// and [Root] pick the policies a command works on, by name or by pattern.
package project

import (
	"fmt"
	"io"
	"sort"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
)

// Kind is the contract a command checks and evaluates against.
type Kind struct {
	Model   *kind.Kind      // the kind as the checker sees it
	Binding *gokind.Binding // its Go types and host functions
	// Host is set for a kind linked into a host binary, whose host
	// functions are implemented. A kind loaded from a file has every
	// function bound to one that fails when it's called.
	Host bool
}

// Linked is a kind a host linked into its own sigil binary, with
// github.com/spechtlabs/sigil/pkg/cli.WithKind.
type Linked struct {
	Model   *kind.Kind      // the kind as the checker sees it
	Binding *gokind.Binding // the host's Go types and host functions
}

// Sources are what a command reads: the paths it was given, the trusted
// paths, and kind files from outside the paths.
type Sources struct {
	Stdin     io.Reader // what a "-" path reads
	Paths     []string  // files, directories, or "-" for stdin
	Trusted   []string  // read into each kind's trusted bundle, as policy.From does
	Kinds     []string  // kind files the paths don't hold, such as --kind names
	Recursive bool      // directories contribute the .sigil files below them, not only those directly inside
}

// Project is every document a command read, grouped by kind.
type Project struct {
	groups   map[string]*Group           // by kind name
	owners   map[string]*Group           // by the name of a policy or module
	names    map[string]*bundle.Document // every policy and module, by name, first definition only
	docs     []*bundle.Document          // the same, in read order, for naming diagnostics
	sources  map[string][]byte           // every file read, by name
	kindDocs map[string][]ast.Node       // the kind documents of every file read, by file
	errs     diag.ErrorList              // what loading found: parse and kind errors, unknown kinds, names defined twice
	files    int                         // files read from the paths
	checked  bool
}

// Group is one kind's share of a project: the kind, and a bundle of the
// documents written against it, with the trusted ones behind
// [bundle.Bundle.Trust].
type Group struct {
	Kind    *Kind
	Bundle  *bundle.Bundle
	trusted *bundle.Bundle
}

// Load reads the sources and groups their documents by kind. Kinds come
// from, in this order: the kinds linked into the binary, the kind
// documents in s.Kinds, the kind documents among the paths, and those
// among the trusted paths. The same kind from two sources must be
// identical, and the later of two that differ is the error. A kind
// file's other documents are read only when the file is also among the
// paths, and a file under a trusted path is read as trusted only.
//
// Parse errors, kind documents that don't check or don't agree, documents
// naming a kind nobody provides and names defined twice are diagnostics,
// for [Project.Errors]; only a path that can't be read, or a kind file that
// doesn't match the kind linked into this binary, fails Load.
func Load(s Sources, linked []Linked) (*Project, humane.Error) {
	l := &loader{
		p:      &Project{groups: map[string]*Group{}, owners: map[string]*Group{}, names: map[string]*bundle.Document{}, sources: map[string][]byte{}, kindDocs: map[string][]ast.Node{}},
		kinds:  newKinds(linked),
		stdin:  s.Stdin,
		parsed: map[string]*file{},
	}
	for _, k := range s.Kinds {
		if err := l.kindFile(k); err != nil {
			return nil, err
		}
	}
	trusted, err := expand(s.Trusted, true)
	if err != nil {
		return nil, err
	}
	regular, err := expand(s.Paths, s.Recursive)
	if err != nil {
		return nil, err
	}
	regular = without(regular, trusted)
	// The paths are read before the trusted paths, so where a kind
	// document of each disagrees, the trusted one is reported.
	rfiles, err := l.readAll(regular)
	if err != nil {
		return nil, err
	}
	tfiles, err := l.readAll(trusted)
	if err != nil {
		return nil, err
	}
	l.p.files = len(rfiles)
	l.group(tfiles, true)
	for _, g := range l.p.groups {
		if len(g.trusted.Documents()) > 0 {
			g.Bundle.Trust(g.trusted)
		}
	}
	l.group(rfiles, false)
	l.indexKinds()
	return l.p, nil
}

// Check checks every group's documents against its kind, in import
// order. Checking twice is a no-op.
func (p *Project) Check() {
	if p.checked {
		return
	}
	p.checked = true
	for _, g := range p.Groups() {
		g.Bundle.Check()
	}
}

// Errors returns every diagnostic so far, sorted by file and position, or
// nil: what loading found, and after [Project.Check] what checking each
// group found.
func (p *Project) Errors() diag.ErrorList {
	all := append(diag.ErrorList{}, p.errs...)
	for _, g := range p.Groups() {
		all = append(all, g.Bundle.Errors()...)
	}
	if len(all) == 0 {
		return nil
	}
	sortErrors(all)
	return all
}

// Groups returns one group per kind that owns a document, sorted by the
// kind's name.
func (p *Project) Groups() []*Group {
	out := make([]*Group, 0, len(p.groups))
	for _, g := range p.groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind.Model.Name < out[j].Kind.Model.Name })
	return out
}

// Group returns the group owning the policy or module called name,
// trusted or not, or nil.
func (p *Project) Group(name string) *Group { return p.owners[name] }

// Policies lists the policies of every group, trusted ones apart, sorted
// by name.
func (p *Project) Policies() []string {
	var out []string
	for _, g := range p.groups {
		out = append(out, g.Bundle.Policies()...)
	}
	sort.Strings(out)
	return out
}

// Files returns how many files were read from the paths, trusted paths
// and kind files apart.
func (p *Project) Files() int { return p.files }

// SourceOf returns a file's source, or nil for a file the project didn't
// read.
func (p *Project) SourceOf(file string) []byte { return p.sources[file] }

// InKind reports whether e points into a kind document, such as a kind
// file that doesn't check or still uses a syntax the language dropped.
func (p *Project) InKind(e *diag.Error) bool {
	if !e.Pos.IsValid() {
		return false
	}
	for _, d := range p.kindDocs[e.File] {
		if d.Pos().Offset <= e.Pos.Offset && e.Pos.Offset < d.End().Offset {
			return true
		}
	}
	return false
}

// Resolve prepares diagnostics for rendering: it names the document each
// one is in, when the stage that reported it didn't, and sorts them by
// file and position. The list is a copy; errs is left alone.
func (p *Project) Resolve(errs diag.ErrorList) diag.ErrorList {
	out := make(diag.ErrorList, len(errs))
	for i, e := range errs {
		named := *e
		if named.Doc == "" {
			named.Doc = p.documentAt(e.File, e.Pos)
		}
		out[i] = &named
	}
	sortErrors(out)
	return out
}

// Render renders every diagnostic with its source line, in the plain
// form. The commands render Resolve's list themselves, styled for a
// terminal, except where a diagnostic becomes a string field.
func (p *Project) Render(errs diag.ErrorList) string {
	return diag.RenderAll(p.Resolve(errs), p.SourceOf, diag.Plain)
}

// documentAt returns the name of the policy or module at pos in file, or
// "".
func (p *Project) documentAt(file string, pos token.Pos) string {
	if !pos.IsValid() {
		return ""
	}
	for _, d := range p.docs {
		if d.File == file && d.Node.Pos().Offset <= pos.Offset && pos.Offset < d.Node.End().Offset {
			return d.Name
		}
	}
	return ""
}

// sortErrors sorts diagnostics by file and position.
func sortErrors(errs diag.ErrorList) {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].File != errs[j].File {
			return errs[i].File < errs[j].File
		}
		return errs[i].Pos.Offset < errs[j].Pos.Offset
	})
}

// header returns a policy's or module's name and the kind its header
// names, or false for a kind document or a header that didn't parse.
func header(doc ast.Doc) (name string, k *ast.Ident, ok bool) {
	switch d := doc.(type) {
	case *ast.PolicyDoc:
		if d.Name != nil && d.Kind != nil {
			return d.Name.String(), d.Kind, true
		}
	case *ast.ModuleDoc:
		if d.Name != nil && d.Kind != nil {
			return d.Name.String(), d.Kind, true
		}
	}
	return "", nil, false
}

// describe names a document's kind for a message.
func describe(doc ast.Doc) string {
	if _, ok := doc.(*ast.ModuleDoc); ok {
		return "module"
	}
	return "policy"
}

// at formats a position with its file, for a hint.
func at(file string, pos token.Pos) string { return fmt.Sprintf("%s:%s", file, pos) }
