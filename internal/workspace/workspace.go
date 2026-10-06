// Package workspace is what the policy commands work on, and what they
// report, apart from any filesystem or terminal: the sigil CLI reads its
// files from disk into it, and the WebAssembly module from the virtual
// files of a request, so both check, evaluate and explain the same way and
// print the same records.
//
// A [Loader] parses the files once each, finds the kinds among the linked
// ones, the kind files and the files themselves, and builds a [Project]
// with one [Group] per kind: the kind and a bundle of that kind's
// documents. A name has one definition across the whole project, across
// kinds too. [Match] and [Root] pick the policies a command works on, by
// name or by pattern, a [Scope] narrows a project to them and what they
// use, and [Project.Diagnose] runs `sigil check`: the requirements, the
// compiles and the lints.
//
// The records are what the commands print as JSON and YAML: a
// [Diagnostic] per problem, a [Report] per evaluation, and an
// [Explanation] per explained policy.
package workspace

import (
	"fmt"
	"sort"

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

// Project is every document a command read, grouped by kind.
type Project struct {
	groups   map[string]*Group           // by kind name
	owners   map[string]*Group           // by the name of a policy or module
	names    map[string]*bundle.Document // every policy and module, by name, first definition only
	docs     []*bundle.Document          // the same, in read order, for naming diagnostics
	sources  map[string][]byte           // every file read, by name
	kindDocs map[string][]ast.Node       // the kind documents of every file read, by file
	known    *kinds                      // every kind a source provided, by name
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

// Names lists every policy and module the project read, trusted ones
// included, sorted by name, such as for a did-you-mean over the names a
// policy can be required by.
func (p *Project) Names() []string {
	out := make([]string, 0, len(p.names))
	for name := range p.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Kinds lists every kind the project knows, linked into the binary or
// read from a kind document, sorted by name. A kind whose document
// doesn't check is left out.
func (p *Project) Kinds() []*Kind {
	if p.known == nil {
		return nil
	}
	out := make([]*Kind, 0, len(p.known.byName))
	for _, k := range p.known.byName {
		if k.kind != nil {
			out = append(out, k.kind)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model.Name < out[j].Model.Name })
	return out
}

// KindSource returns the document that declares the kind called name, the
// first one read, and its file. It returns nil and "" for a kind linked
// into the binary, which has no document, and for one the project doesn't
// know.
func (p *Project) KindSource(name string) (*ast.KindDoc, string) {
	if p.known == nil {
		return nil, ""
	}
	k, ok := p.known.byName[name]
	if !ok || k.doc == nil {
		return nil, ""
	}
	return k.doc, k.file
}

// Files returns how many files were read from the paths, trusted paths
// and kind files apart.
func (p *Project) Files() int { return p.files }

// Read returns how many files the project read: its paths, its trusted
// paths and its kind files, each once however many ways it was named.
// It's what check reports as checked, the same for one tree whether a
// file is read as trusted or not.
func (p *Project) Read() int { return len(p.sources) }

// SourceNames lists the names of every file the project read, sorted, as
// [Project.SourceOf] takes them.
func (p *Project) SourceNames() []string {
	out := make([]string, 0, len(p.sources))
	for name := range p.sources {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

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
