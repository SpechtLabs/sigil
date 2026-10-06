// Package bundle loads a set of documents, indexes them by the names in
// their headers, checks them against a kind in import order and compiles
// a root policy with everything it invokes and imports linked. The Go
// API and the CLI both load through it, so a bundle resolves the same
// way whether it comes from an [io/fs.FS] or from command-line paths.
//
// A [Bundle] drives the front of the pipeline for many files at once: it
// parses each file as it's added, runs the checker over each document
// with the bundle as its resolver, and hands a clean root to
// [github.com/spechtlabs/sigil/internal/eval.CompilePolicy], linking the
// documents it invokes and imports by name. File paths never matter to
// resolution; a name has one definition in the bundle.
//
// A host's required policies can come from a trusted source, a second
// Bundle passed to [Bundle.Trust]. A trusted document resolves before the
// bundle's own, and a bundle document that claims its name is an error,
// unless it's a byte-for-byte copy of the trusted one.
//
// File paths are names for messages, not keys: two sources read into a
// bundle, or a bundle and its trusted one, may each hold a file of the
// same path. Every document keeps the bytes it was read from, and every
// diagnostic quotes the source it was reported against.
//
// Diagnostics accumulate in the bundle rather than stopping it, so one
// run reports every error. [Bundle.Errors] returns them, and
// [Bundle.Compile] returns them with the root's own. A Bundle isn't safe
// for concurrent use.
package bundle

import (
	"bytes"
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

// MaxImportDepth is how long a chain of imports may be: a document that
// imports one that imports another, and so on, counting the first. Every
// pass that follows an invocation or a use, the compiler linking a
// policy's instances, the evaluator and explain, goes one level deeper
// per document, and a stack is finite, most of all in a WebAssembly host,
// so a longer chain is an error at the import that makes it too long
// rather than a crash. Real bundles stay far below it.
const MaxImportDepth = 64

// Bundle is a set of documents against one kind, indexed by name.
type Bundle struct {
	kind    *kind.Kind
	Sources map[string][]byte // file name to source; the first file read when two sources hold one path
	docs    map[string]*Document
	trusted *Bundle                // documents from a trusted source, resolved before this bundle's
	quotes  map[*diag.Error][]byte // the source each diagnostic was reported against
	order   []string
	errs    diag.ErrorList
	loaded  int // counts the sources Load has read, so a document knows which one it came from
	read    int // policies and modules added, indexed or not
	checked bool
}

// Document is one policy or module: where it came from, its tree, and
// what the checker learned about it.
type Document struct {
	Node     ast.Doc
	Info     *check.Info     // what the checker recorded; nil until checked
	Exported *check.Exported // what a `use` of the document sees; nil until checked
	Name     string          // the name in the document's header
	File     string
	src      []byte // the source of the file it was read from
	from     int    // which source Load read it from, by the count of sources loaded before it
	Kind     bool   // a kind document, indexed only so `use` of its name is an error
	Trusted  bool   // the document comes from the trusted bundle
	failed   bool   // checked with errors, or in an import cycle
}

// Module reports whether the document is a module.
func (d *Document) Module() bool {
	_, ok := d.Node.(*ast.ModuleDoc)
	return ok
}

// Clean reports whether the document checked without errors, which is
// what a lint needs before it reads the checker's Info.
func (d *Document) Clean() bool { return d.Info != nil && !d.failed }

// New returns an empty bundle for k.
func New(k *kind.Kind) *Bundle {
	return &Bundle{kind: k, Sources: map[string][]byte{}, docs: map[string]*Document{}, quotes: map[*diag.Error][]byte{}}
}

// Redefined is the diagnostic for doc, a policy or module in file, taking
// a name prev already has. A bundle reports it as it indexes; a caller
// that keeps one namespace across several bundles, as the CLI does across
// kinds, reports it the same way. It's nil for a kind document or a nil
// prev.
func Redefined(file string, doc ast.Doc, prev *Document) *diag.Error {
	if prev == nil {
		return nil
	}
	var name *ast.PolicyName
	switch d := doc.(type) {
	case *ast.PolicyDoc:
		name = d.Name
	case *ast.ModuleDoc:
		name = d.Name
	default:
		return nil
	}
	where := "first defined at " + at(prev.File, prev.Node.Pos())
	if prev.Trusted {
		where = "the name belongs to the trusted source, defined at " + at(prev.File, prev.Node.Pos())
	}
	return &diag.Error{
		File: file, Pos: name.Pos(), End: name.End(),
		Msg:  fmt.Sprintf("%s %s is defined twice", describe(doc), name.String()),
		Help: where + "; documents resolve by name, so each name has one definition",
	}
}

// Trust adds a trusted bundle: required policies resolve there first,
// and a document here that takes a trusted name is an error, unless it's
// a byte-for-byte copy of the trusted document, which is left out. Call
// it before adding this bundle's own files, since the name check runs as
// each document is indexed. t's diagnostics join this bundle's on
// [Bundle.Check].
func (b *Bundle) Trust(t *Bundle) {
	b.trusted = t
	for _, d := range t.docs {
		d.Trusted = true
	}
}

// Add parses src as file and indexes its documents by name. A file that
// fails to parse still contributes every document that parsed. Parse
// errors and names defined twice are recorded in the bundle, for
// [Bundle.Errors]. Add keeps src; the caller must not modify it after.
func (b *Bundle) Add(file string, src []byte) {
	f, errs := parser.ParseFile(file, src)
	b.report(src, errs...)
	b.add(file, src, f.Docs, true)
}

// Index adds documents already parsed from file, whose source is src, for
// a caller that parses once and splits the documents between bundles, as
// the CLI does by kind. Unlike [Bundle.Add] it records no parse errors,
// which are the caller's to report, and doesn't check a kind document
// against the bundle's kind; a kind document is still indexed, so `use` of
// its name is an error. Names defined twice are recorded as Add records
// them. Index keeps src; the caller must not modify it after.
func (b *Bundle) Index(file string, src []byte, docs []ast.Doc) {
	b.add(file, src, docs, false)
}

// Read returns how many policies and modules the bundle has been given
// so far, counting those it left out as defined twice or as copies of a
// trusted document. A caller that loads a source compares it before and
// after, to tell a source that holds none.
func (b *Bundle) Read() int { return b.read }

// add keeps src as file's source and indexes its documents, checking a
// kind document for the bundle's kind against it when checkKind is set.
// When an earlier source holds a file of the same path, Sources keeps
// that one; the documents keep their own.
func (b *Bundle) add(file string, src []byte, docs []ast.Doc, checkKind bool) {
	if _, ok := b.Sources[file]; !ok {
		b.Sources[file] = src
	}
	for _, doc := range docs {
		b.index(file, src, doc, checkKind)
	}
}

// index records doc, read from file whose source is src, by its header
// name, or reports the name as defined twice. A document that's a copy
// of one an earlier source or the trusted bundle defines, byte for byte,
// is left out instead: it's the same definition, read twice. With
// checkKind, a kind document with the bundle's kind name must match the
// contract; one for another kind is ignored.
func (b *Bundle) index(file string, src []byte, doc ast.Doc, checkKind bool) {
	var name string
	switch d := doc.(type) {
	case *ast.PolicyDoc:
		name = d.Name.String()
	case *ast.ModuleDoc:
		name = d.Name.String()
	case *ast.KindDoc:
		if checkKind && d.Name.Name == b.kind.Name {
			c := check.New(file)
			if loaded := c.Kind(d); loaded != nil && loaded.Source() != b.kind.Source() {
				c.KindMismatch(d, b.kind.Name)
			}
			b.report(src, c.Errors()...)
		}
		if _, taken := b.docs[d.Name.Name]; !taken {
			b.docs[d.Name.Name] = &Document{Node: doc, Name: d.Name.Name, File: file, Kind: true, src: src, from: b.loaded}
		}
		return
	default:
		return
	}
	b.read++
	d := &Document{Node: doc, Name: name, File: file, src: src, from: b.loaded}
	if prev := b.lookup(name); prev != nil && !prev.Kind {
		if (prev.Trusted || prev.from != d.from) && prev.src != nil && bytes.Equal(prev.text(), d.text()) {
			return
		}
		b.report(src, Redefined(file, doc, prev))
		return
	}
	b.docs[name] = d
	b.order = append(b.order, name)
}

// text is the document's source, from its header to its end.
func (d *Document) text() []byte {
	return d.src[d.Node.Pos().Offset:d.Node.End().Offset]
}

// report records errs, each quoting src.
func (b *Bundle) report(src []byte, errs ...*diag.Error) {
	for _, e := range errs {
		b.quotes[e] = src
	}
	b.errs = append(b.errs, errs...)
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

// Document returns the document called name, looking in the trusted
// bundle first, or nil.
func (b *Bundle) Document(name string) *Document { return b.lookup(name) }

// Documents lists the bundle's own policies and modules, in the order
// they were read. Trusted documents aren't the bundle's own.
func (b *Bundle) Documents() []*Document {
	out := make([]*Document, len(b.order))
	for i, n := range b.order {
		out[i] = b.docs[n]
	}
	return out
}

// All lists the bundle's own policies and modules in the order they were
// read, then the trusted bundle's, for a tool that looks at every document
// a `use` can name.
func (b *Bundle) All() []*Document {
	out := b.Documents()
	if b.trusted != nil {
		out = append(out, b.trusted.All()...)
	}
	return out
}

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
		for _, e := range b.trusted.errs {
			b.report(b.trusted.quotes[e], e)
		}
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
// cycle, which it reports, and reports a chain of imports longer than
// MaxImportDepth.
func (b *Bundle) topological() []string {
	s := &sorter{b: b, state: map[string]int{}, depth: map[string]int{}}
	for _, name := range b.order {
		s.visit(name)
	}
	return s.out
}

// sorter is one depth-first pass over the import graph. It keeps its own
// stack rather than recursing, so a chain of imports of any length is
// sorted and measured without growing the goroutine's stack.
type sorter struct {
	b     *Bundle
	state map[string]int // unseen, visiting or done, by document name
	depth map[string]int // the longest chain of imports from a document, itself included, once it's done
	out   []string
}

// frame is a document on the sorter's path, and how far through its
// imports the sorter is.
type frame struct {
	doc  *Document
	uses []*ast.UseStmt
	next int
}

const (
	unseen = iota
	visiting
	done
)

// visit orders root and every document it imports after their imports,
// reporting a cycle when an import leads back to a document on the
// current path.
func (s *sorter) visit(root string) {
	if s.state[root] != unseen {
		return
	}
	path := []*frame{s.enter(root)}
	for len(path) > 0 {
		f := path[len(path)-1]
		if f.next == len(f.uses) {
			s.leave(f)
			path = path[:len(path)-1]
			continue
		}
		u := f.uses[f.next]
		f.next++
		dep := u.Path.String()
		ok := s.own(dep)
		switch {
		case !ok:
			// trusted, unknown or a kind: the checker reports it
		case s.state[dep] == visiting:
			names := make([]string, 0, len(path)+1)
			for _, p := range path {
				names = append(names, p.doc.Name)
			}
			s.cycle(f.doc, u, append(names, dep))
		case s.state[dep] == unseen:
			path = append(path, s.enter(dep))
		}
	}
}

// own reports whether dep, the name a use imports, is one of the bundle's
// own policies or modules, the documents the sorter orders.
func (s *sorter) own(dep string) bool {
	d, ok := s.b.docs[dep]
	return ok && !d.Kind
}

// enter puts a document on the path.
func (s *sorter) enter(name string) *frame {
	s.state[name] = visiting
	d := s.b.docs[name]
	return &frame{doc: d, uses: uses(d.Node)}
}

// leave orders a document whose imports are all ordered, and measures
// the longest chain of imports from it. The document where a chain first
// grows longer than MaxImportDepth reports it, at the import that
// continues the chain, and fails with a stub export like a document in a
// cycle, so the documents that import it don't repeat the error.
func (s *sorter) leave(f *frame) {
	name := f.doc.Name
	s.state[name] = done
	s.out = append(s.out, name)
	depth, deepest := 1, (*ast.UseStmt)(nil)
	for _, u := range f.uses {
		if dep := u.Path.String(); s.own(dep) && s.state[dep] == done && s.depth[dep]+1 > depth {
			depth, deepest = s.depth[dep]+1, u
		}
	}
	s.depth[name] = depth
	if depth != MaxImportDepth+1 {
		return
	}
	s.b.report(f.doc.src, &diag.Error{
		File: f.doc.File, Pos: deepest.Pos(), End: deepest.End(),
		Msg:  fmt.Sprintf("imports nest more than %d levels deep: %s starts a chain of %d documents, each importing the next", MaxImportDepth, name, depth),
		Help: fmt.Sprintf("a chain of imports may be at most %d documents long; import the documents deep in the chain directly, from one nearer its start", MaxImportDepth),
	})
	f.doc.failed = true
	f.doc.Exported = &check.Exported{Name: name, Module: f.doc.Module(), Failed: true}
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
	s.b.report(d.src, &diag.Error{
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
		b.report(d.src, errs...)
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
	Params  map[string]eval.Value // the root's params bound by the host
	Binding *gokind.Binding       // the kind's Go types and host functions; can be nil when Static is set
	Require []string              // policies the root must invoke unconditionally
	// Static compiles the structure only, for explain, as
	// eval.Options.Static does.
	Static bool
}

// Compile checks the bundle and compiles the policy called root. The
// errors are the bundle's own and this root's, sorted by file and
// position; compiling one root never leaves errors behind for the next,
// so a tool can compile every policy of a bundle in turn. Any error in
// the bundle, even in a document root doesn't use, fails the compile.
func (b *Bundle) Compile(root string, o Options) (*eval.Policy, diag.ErrorList) {
	b.Check()
	fail := func(errs ...*diag.Error) diag.ErrorList {
		all := append(append(diag.ErrorList{}, b.Errors()...), errs...)
		sortErrors(all)
		return all
	}
	d := b.lookup(root)
	switch {
	case d == nil:
		return nil, fail(b.noRoot(root))
	case d.Module():
		return nil, fail(b.quote(d, &diag.Error{
			File: d.File, Pos: d.Node.Pos(), End: d.Node.Pos(),
			Msg:  fmt.Sprintf("%s is a module, not a policy", root),
			Help: "a module holds only lets and has no rules to evaluate; name a policy",
		}))
	}
	if errs := b.Errors(); errs != nil {
		return nil, errs
	}
	prog, err := eval.CompilePolicy(b.source(d), b.kind, o.Binding, linker{b}, eval.Options{Params: o.Params, Static: o.Static})
	if err != nil {
		return nil, fail(err)
	}
	if errs := b.require(prog, o.Require); errs != nil {
		return nil, fail(errs...)
	}
	return prog, nil
}

// require checks that the root reaches every required policy through
// top-level invocations only.
func (b *Bundle) require(prog *eval.Policy, names []string) diag.ErrorList {
	if prog == nil {
		return nil
	}
	var errs diag.ErrorList
	root := b.lookup(prog.Name)
	for _, name := range names {
		req := prog.Requirement(name)
		switch {
		case req.Unconditional:
		case req.Invoked:
			for _, site := range req.Gated {
				errs = append(errs, b.quote(b.lookup(site.Policy), &diag.Error{
					File: site.File, Doc: site.Policy, Pos: site.Pos, End: site.End,
					Msg:  fmt.Sprintf("%s must be invoked unconditionally", name),
					Help: fmt.Sprintf("the host requires %s for every %s policy; move the call to the top level", name, b.kind.Name),
				}))
			}
		default:
			errs = append(errs, b.quote(root, &diag.Error{
				File: root.File, Pos: root.Node.Pos(), End: root.Node.Pos(),
				Msg:  fmt.Sprintf("%s doesn't invoke %s", prog.Name, name),
				Help: fmt.Sprintf("the host requires %s for every %s policy; import it with `use %s` and invoke it at the top level", name, b.kind.Name, name),
			}))
		}
	}
	return errs
}

// source wraps a document for the compiler.
func (b *Bundle) source(d *Document) *eval.Source {
	if d == nil {
		return nil
	}
	return &eval.Source{Doc: d.Node, Info: d.Info, File: d.File, Src: d.src}
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
	sortErrors(b.errs)
	return b.errs
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

// Render renders every diagnostic with its source line, in the plain
// form, each quoting the source [Bundle.SourceFor] finds for it. The CLI
// renders Resolve's list itself, styled for a terminal.
func (b *Bundle) Render(errs diag.ErrorList) string {
	quoted := make(map[*diag.Error][]byte, len(errs))
	out := make(diag.ErrorList, len(errs))
	for i, e := range errs {
		named := *e
		named.Doc = b.DocumentOf(e)
		out[i] = &named
		quoted[out[i]] = b.SourceFor(e)
	}
	sortErrors(out)
	parts := make([]string, len(out))
	for i, e := range out {
		parts[i] = strings.TrimRight(diag.RenderWith(e, quoted[e], diag.Plain), "\n")
	}
	return strings.Join(parts, "\n\n")
}

// Resolve prepares diagnostics for rendering: it names the document each
// one is in, when the stage that reported it didn't, and sorts them by
// file and position. The list is a copy; errs is left alone.
func (b *Bundle) Resolve(errs diag.ErrorList) diag.ErrorList {
	out := make(diag.ErrorList, len(errs))
	for i, e := range errs {
		named := *e
		named.Doc = b.DocumentOf(e)
		out[i] = &named
	}
	sortErrors(out)
	return out
}

// SourceFor returns the source e quotes: the source the bundle, or its
// trusted one, reported e against, or for a diagnostic from elsewhere,
// such as the compiler's, the source of the document at its position.
// Unlike [Bundle.SourceOf], it tells apart two files of the same path
// from different sources.
func (b *Bundle) SourceFor(e *diag.Error) []byte {
	if e == nil {
		return nil
	}
	if src, ok := b.quoteOf(e); ok {
		return src
	}
	if d := b.documentAt(e.File, e.Pos); d != nil {
		return d.src
	}
	return b.SourceOf(e.File)
}

// DocumentOf returns the name of the document e is in: the one it names,
// or the one at its position in the source it quotes, or "".
func (b *Bundle) DocumentOf(e *diag.Error) string {
	if e == nil {
		return ""
	}
	if e.Doc != "" || !e.Pos.IsValid() {
		return e.Doc
	}
	src, ok := b.quoteOf(e)
	if !ok {
		return b.DocumentAt(e.File, e.Pos)
	}
	for _, d := range b.All() {
		if d.File == e.File && sameSource(d.src, src) && d.contains(e.Pos) {
			return d.Name
		}
	}
	return ""
}

// SourceOf returns a file's source, from this bundle or the trusted one,
// or nil for a file neither read. When both, or two sources, hold a file
// of that path, it's this bundle's, or the first one read; a diagnostic's
// own is [Bundle.SourceFor]'s.
func (b *Bundle) SourceOf(file string) []byte {
	if src, ok := b.Sources[file]; ok {
		return src
	}
	if b.trusted != nil {
		return b.trusted.SourceOf(file)
	}
	return nil
}

// DocumentAt returns the name of the document at p in file, or "". When
// this bundle and the trusted one both hold a file of that path, it looks
// in this bundle's first.
func (b *Bundle) DocumentAt(file string, p token.Pos) string {
	if d := b.documentAt(file, p); d != nil {
		return d.Name
	}
	return ""
}

// documentAt returns the document at p in file, this bundle's first, or
// nil.
func (b *Bundle) documentAt(file string, p token.Pos) *Document {
	if !p.IsValid() {
		return nil
	}
	for _, d := range b.All() {
		if d.File == file && d.contains(p) {
			return d
		}
	}
	return nil
}

// quoteOf returns the source the bundle reported e against. [Bundle.Check]
// records the trusted bundle's diagnostics here too, as it moves them.
func (b *Bundle) quoteOf(e *diag.Error) ([]byte, bool) {
	src, ok := b.quotes[e]
	return src, ok
}

// quote records that e quotes d's source, and returns e. A nil d records
// nothing.
func (b *Bundle) quote(d *Document, e *diag.Error) *diag.Error {
	if d != nil {
		b.quotes[e] = d.src
	}
	return e
}

// contains reports whether p falls inside the document.
func (d *Document) contains(p token.Pos) bool {
	return d.Node.Pos().Offset <= p.Offset && p.Offset < d.Node.End().Offset
}

// sameSource reports whether a and b are the same source: the same bytes
// in memory, not merely equal ones.
func sameSource(a, b []byte) bool {
	return len(a) > 0 && len(a) == len(b) && &a[0] == &b[0]
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
