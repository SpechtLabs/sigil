// Package bundle loads a set of documents, indexes them by the names in
// their headers, checks them against a kind in import order and compiles
// a root policy with everything it invokes and imports linked. The Go
// API and the CLI both load through it, so a bundle resolves the same
// way whether it comes from an fs.FS or from command-line paths.
package bundle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/token"
)

// Bundle is a set of documents against one kind.
type Bundle struct {
	kind    *kind.Kind
	Sources map[string][]byte // file name to source
	docs    map[string]*Document
	trusted *Bundle // documents from a trusted source, resolved before this bundle's
	order   []string
	errs    diag.ErrorList
	checked bool
}

// Document is one policy or module: where it came from, its tree, and
// what the checker learned about it.
type Document struct {
	Node     ast.Doc
	Info     *check.Info
	Exported *check.Exported
	Name     string
	File     string
	Kind     bool // a kind document, indexed only so `use` of its name is an error
	Trusted  bool
	failed   bool // checked with errors, or in an import cycle
}

// Module reports whether the document is a module.
func (d *Document) Module() bool {
	_, ok := d.Node.(*ast.ModuleDoc)
	return ok
}

// New returns an empty bundle for k.
func New(k *kind.Kind) *Bundle {
	return &Bundle{kind: k, Sources: map[string][]byte{}, docs: map[string]*Document{}}
}

// Trust adds a trusted bundle: required policies resolve there first,
// and a document here that takes a trusted name is an error.
func (b *Bundle) Trust(t *Bundle) {
	b.trusted = t
	for _, d := range t.docs {
		d.Trusted = true
	}
}

// Add parses src as file and indexes its documents. A file that fails
// to parse still contributes every document that parsed.
func (b *Bundle) Add(file string, src []byte) {
	b.Sources[file] = src
	f, errs := parser.ParseFile(file, src)
	b.errs = append(b.errs, errs...)
	for _, doc := range f.Docs {
		b.index(file, doc)
	}
}

// index records doc by its header name, or reports the name as defined
// twice. A kind document with the bundle's kind name must match the
// contract; one for another kind is ignored.
func (b *Bundle) index(file string, doc ast.Doc) {
	var name string
	var pos ast.Node
	switch d := doc.(type) {
	case *ast.PolicyDoc:
		name, pos = d.Name.String(), d.Name
	case *ast.ModuleDoc:
		name, pos = d.Name.String(), d.Name
	case *ast.KindDoc:
		if d.Name.Name == b.kind.Name {
			c := check.New(file)
			if loaded := c.Kind(d); loaded != nil && loaded.Source() != b.kind.Source() {
				c.KindMismatch(d, b.kind.Name)
			}
			b.errs = append(b.errs, c.Errors()...)
		}
		if _, taken := b.docs[d.Name.Name]; !taken {
			b.docs[d.Name.Name] = &Document{Node: doc, Name: d.Name.Name, File: file, Kind: true}
		}
		return
	default:
		return
	}
	if prev := b.lookup(name); prev != nil && !prev.Kind {
		where := "first defined at " + at(prev.File, prev.Node.Pos())
		if prev.Trusted {
			where = "the name belongs to the trusted source, defined at " + at(prev.File, prev.Node.Pos())
		}
		b.errs = append(b.errs, &diag.Error{
			File: file, Pos: pos.Pos(), End: pos.End(),
			Msg:  fmt.Sprintf("%s %s is defined twice", describe(doc), name),
			Help: where + "; documents resolve by name, so each name has one definition",
		})
		return
	}
	b.docs[name] = &Document{Node: doc, Name: name, File: file}
	b.order = append(b.order, name)
}

// lookup finds a document by name, in the trusted bundle first.
func (b *Bundle) lookup(name string) *Document {
	if b.trusted != nil {
		if d, ok := b.trusted.docs[name]; ok && !d.Kind {
			return d
		}
	}
	if d, ok := b.docs[name]; ok {
		return d
	}
	if b.trusted != nil {
		if d, ok := b.trusted.docs[name]; ok {
			return d
		}
	}
	return nil
}

// Document returns the document called name, or nil.
func (b *Bundle) Document(name string) *Document { return b.lookup(name) }

// Policies lists the bundle's own policies, in the order they were read.
func (b *Bundle) Policies() []string {
	var out []string
	for _, n := range b.order {
		if !b.docs[n].Module() {
			out = append(out, n)
		}
	}
	return out
}

// Check checks every document against the kind, in import order, so
// each one sees the exports of the documents it uses. Import cycles are
// reported once, at the import that closes them, and the documents in
// them aren't checked. Checking twice is a no-op.
func (b *Bundle) Check() {
	if b.checked {
		return
	}
	b.checked = true
	if b.trusted != nil {
		b.trusted.Check()
		b.errs = append(b.errs, b.trusted.errs...)
		b.trusted.errs = nil
	}
	for _, name := range b.topological() {
		b.checkDoc(b.docs[name])
	}
}

// anyPolicy reports whether any of the named documents is a policy.
func (b *Bundle) anyPolicy(names []string) bool {
	for _, n := range names {
		if d, ok := b.docs[n]; ok && !d.Module() {
			return true
		}
	}
	return false
}

// uses returns the names a document imports.
func uses(d ast.Doc) []*ast.UseStmt {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return d.Uses
	case *ast.ModuleDoc:
		return d.Uses
	}
	return nil
}

// topological orders the bundle's own documents so that every document
// comes after the ones it imports, leaving out documents in an import
// cycle, which it reports.
func (b *Bundle) topological() []string {
	s := &sorter{b: b, state: map[string]int{}}
	for _, name := range b.order {
		s.visit(name, nil)
	}
	return s.out
}

// sorter is one depth-first pass over the import graph.
type sorter struct {
	b     *Bundle
	state map[string]int // unseen, visiting or done, by document name
	out   []string
}

const (
	unseen = iota
	visiting
	done
)

// visit orders name after its imports, reporting a cycle when an import
// leads back to a document on the current path.
func (s *sorter) visit(name string, path []string) {
	if s.state[name] != unseen {
		return
	}
	s.state[name] = visiting
	d := s.b.docs[name]
	for _, u := range uses(d.Node) {
		dep := u.Path.String()
		if _, own := s.b.docs[dep]; !own || s.b.docs[dep].Kind {
			continue // trusted, unknown or a kind: the checker reports it
		}
		if s.state[dep] == visiting {
			s.cycle(d, u, append(append([]string{}, path...), name, dep))
			continue
		}
		s.visit(dep, append(append([]string{}, path...), name))
	}
	s.state[name] = done
	s.out = append(s.out, name)
}

// cycle reports the import cycle that ends the path, at the import that
// closes it, and marks its documents failed with a stub export so their
// importers don't repeat the error.
func (s *sorter) cycle(d *Document, u *ast.UseStmt, cycle []string) {
	if d == nil {
		return
	}
	if u == nil {
		return
	}
	closing := cycle[len(cycle)-1]
	start := 0
	for i, n := range cycle[:len(cycle)-1] {
		if n == closing {
			start = i
		}
	}
	cycle = cycle[start:]
	help := "imports form a directed acyclic graph; move the shared lets to a module both can import"
	if s.b.anyPolicy(cycle) {
		help = "a policy can't invoke itself, directly or through other policies; imports form a directed acyclic graph"
	}
	s.b.errs = append(s.b.errs, &diag.Error{
		File: d.File, Pos: u.Pos(), End: u.End(),
		Msg:  "import cycle: " + strings.Join(cycle, " -> "),
		Help: help,
	})
	for _, n := range cycle {
		s.b.docs[n].failed = true
		s.b.docs[n].Exported = &check.Exported{Name: n, Module: s.b.docs[n].Module(), Failed: true}
	}
}

// checkDoc checks one document with the bundle as its resolver.
func (b *Bundle) checkDoc(d *Document) {
	if d == nil {
		return
	}
	if d.failed {
		return // in an import cycle, reported with a stub export
	}
	c := check.New(d.File)
	c.Resolver = b.resolve
	switch n := d.Node.(type) {
	case *ast.PolicyDoc:
		c.Policy(n, b.kind)
	case *ast.ModuleDoc:
		c.Module(n, b.kind)
	}
	if errs := c.Errors(); errs != nil {
		b.errs = append(b.errs, errs...)
		d.failed = true
	}
	d.Info, d.Exported = c.Info(), c.Exported()
}

// resolve is the checker's Resolver: what a `use` of name sees. A
// document that failed its own check still resolves, with whatever it
// exported, so one broken module doesn't hide the errors in its users.
func (b *Bundle) resolve(name string) (*check.Exported, bool) {
	d := b.lookup(name)
	switch {
	case d == nil:
		return nil, false
	case d.Kind:
		return &check.Exported{Name: name, Kind: true}, true
	case d.Exported == nil:
		// Not checked yet: it's in an import cycle, or trusted and
		// unchecked. Report as unknown rather than crash.
		return nil, false
	}
	return d.Exported, true
}

// Options configures Compile.
type Options struct {
	Params  map[string]eval.Value
	Binding *gokind.Binding
	Require []string // policies the root must invoke unconditionally
	Static  bool
}

// Compile checks the bundle and compiles the policy called root.
func (b *Bundle) Compile(root string, o Options) (*eval.Policy, diag.ErrorList) {
	b.Check()
	d := b.lookup(root)
	switch {
	case d == nil:
		b.errs = append(b.errs, b.noRoot(root))
	case d.Module():
		b.errs = append(b.errs, &diag.Error{
			File: d.File, Pos: d.Node.Pos(), End: d.Node.Pos(),
			Msg:  fmt.Sprintf("%s is a module, not a policy", root),
			Help: "a module holds only lets and has no rules to evaluate; name a policy",
		})
	}
	if errs := b.Errors(); errs != nil {
		return nil, errs
	}
	prog, err := eval.CompilePolicy(b.source(d), b.kind, o.Binding, linker{b}, eval.Options{Params: o.Params, Static: o.Static})
	if err != nil {
		b.errs = append(b.errs, err)
		return nil, b.Errors()
	}
	b.require(prog, o.Require)
	if errs := b.Errors(); errs != nil {
		return nil, errs
	}
	return prog, nil
}

// require checks that the root reaches every required policy through
// top-level invocations only.
func (b *Bundle) require(prog *eval.Policy, names []string) {
	if prog == nil {
		return
	}
	root := b.lookup(prog.Name)
	for _, name := range names {
		req := prog.Requirement(name)
		switch {
		case req.Unconditional:
		case req.Invoked:
			for _, site := range req.Gated {
				b.errs = append(b.errs, &diag.Error{
					File: site.File, Pos: site.Pos, End: site.End,
					Msg:  fmt.Sprintf("%s must be invoked unconditionally", name),
					Help: fmt.Sprintf("the host requires %s for every %s policy; move the call to the top level", name, b.kind.Name),
				})
			}
		default:
			b.errs = append(b.errs, &diag.Error{
				File: root.File, Pos: root.Node.Pos(), End: root.Node.Pos(),
				Msg:  fmt.Sprintf("%s doesn't invoke %s", prog.Name, name),
				Help: fmt.Sprintf("the host requires %s for every %s policy; import it with `use %s` and invoke it at the top level", name, b.kind.Name, name),
			})
		}
	}
}

// source wraps a document for the compiler.
func (b *Bundle) source(d *Document) *eval.Source {
	if d == nil {
		return nil
	}
	src := b.Sources[d.File]
	if d.Trusted && b.trusted != nil {
		src = b.trusted.Sources[d.File]
	}
	return &eval.Source{Doc: d.Node, Info: d.Info, File: d.File, Src: src}
}

// linker resolves the compiler's links through the bundle.
type linker struct{ b *Bundle }

func (l linker) Source(name string) (*eval.Source, bool) {
	d := l.b.lookup(name)
	if d == nil || d.Kind || d.Info == nil {
		return nil, false
	}
	return l.b.source(d), true
}

// noRoot describes a root that isn't in the bundle, listing what is.
func (b *Bundle) noRoot(name string) *diag.Error {
	policies := b.Policies()
	help := "the bundle defines no policies"
	if len(policies) > 0 {
		help = "the bundle defines: " + strings.Join(policies, ", ")
	}
	return &diag.Error{Msg: fmt.Sprintf("bundle has no policy %s", name), Help: help}
}

// Errors returns every diagnostic so far, sorted by file and position,
// or nil.
func (b *Bundle) Errors() diag.ErrorList {
	if len(b.errs) == 0 {
		return nil
	}
	sort.SliceStable(b.errs, func(i, j int) bool {
		if b.errs[i].File != b.errs[j].File {
			return b.errs[i].File < b.errs[j].File
		}
		return b.errs[i].Pos.Offset < b.errs[j].Pos.Offset
	})
	return b.errs
}

// Render renders every diagnostic with its source line.
func (b *Bundle) Render(errs diag.ErrorList) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = strings.TrimRight(diag.Render(e, b.sourceOf(e.File)), "\n")
	}
	return strings.Join(parts, "\n")
}

// sourceOf returns a file's source, from this bundle or the trusted one.
func (b *Bundle) sourceOf(file string) []byte {
	if src, ok := b.Sources[file]; ok {
		return src
	}
	if b.trusted != nil {
		return b.trusted.sourceOf(file)
	}
	return nil
}

// DocumentAt returns the name of the document at p in file, or "".
func (b *Bundle) DocumentAt(file string, p token.Pos) string {
	if !p.IsValid() {
		return ""
	}
	for _, name := range b.order {
		d := b.docs[name]
		if d.File == file && d.Node.Pos().Offset <= p.Offset && p.Offset < d.Node.End().Offset {
			return name
		}
	}
	if b.trusted != nil {
		return b.trusted.DocumentAt(file, p)
	}
	return ""
}

// describe names a document's kind for a message.
func describe(d ast.Doc) string {
	if _, ok := d.(*ast.ModuleDoc); ok {
		return "module"
	}
	return "policy"
}

// at formats a position with its file, for a hint.
func at(file string, p token.Pos) string {
	if file == "" {
		return p.String()
	}
	return file + ":" + p.String()
}
