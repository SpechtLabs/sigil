package check

import (
	"sort"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/parser"
)

// scope is what one check covers: every document, or with --policy the
// matching policies and every document they use, directly or through
// others.
type scope struct {
	p        *project.Project
	selected []string                          // the policies --policy matches; nil without it
	policies []string                          // the policies to compile, sorted
	docs     map[string]bool                   // the documents in scope by name; nil for every document
	files    map[string]bool                   // the files that hold them
	bundles  map[*project.Group]*bundle.Bundle // what each group's policies compile in, built on first use
	kinds    map[string][]ast.Span             // the kind documents of each file a diagnostic is in, by file
}

// everything is the scope of a check without --policy.
func everything(p *project.Project) *scope {
	return &scope{p: p, policies: p.Policies()}
}

// scopeOf is the scope of the selected policies: they and every document
// they use, followed through each `use`, trusted ones included.
func scopeOf(p *project.Project, selected []string) *scope {
	s := &scope{p: p, selected: selected, docs: map[string]bool{}, files: map[string]bool{}, bundles: map[*project.Group]*bundle.Bundle{}}
	queue := append([]string{}, selected...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if s.docs[name] {
			continue
		}
		g := p.Group(name)
		if g == nil {
			continue // a use of a name nobody defines, which the checker reports in the document that uses it
		}
		d := g.Bundle.Document(name)
		s.docs[name] = true
		s.files[d.File] = true
		for _, u := range uses(d.Node) {
			if u.Path != nil {
				queue = append(queue, u.Path.String())
			}
		}
	}
	for _, name := range p.Policies() {
		if s.docs[name] {
			s.policies = append(s.policies, name)
		}
	}
	return s
}

// keep returns the diagnostics in scope, each naming its document, or
// nil when there are none. A diagnostic is in scope when its document
// is, when it's in a kind document, since every policy is checked
// against a kind and a kind file that doesn't check or doesn't match must
// fail the run, and when it's outside every document, such as a parse
// error, in a file that holds a document in scope.
func (s *scope) keep(errs diag.ErrorList) diag.ErrorList {
	if len(errs) == 0 {
		return nil
	}
	named := s.p.Resolve(errs)
	if s.docs == nil {
		return named
	}
	var out diag.ErrorList
	for _, e := range named {
		if s.docs[e.Doc] || (e.Doc == "" && (s.files[e.File] || s.inKind(e))) {
			out = append(out, e)
		}
	}
	return out
}

// bundle returns the bundle g's policies compile in: g's own, or with
// --policy one holding only g's documents in scope. A compile fails on
// any error in its bundle, so without it an error outside the scope
// would stop the policies in scope from compiling, and from being
// checked against --require.
func (s *scope) bundle(g *project.Group) *bundle.Bundle {
	if s.docs == nil {
		return g.Bundle
	}
	if b, ok := s.bundles[g]; ok {
		return b
	}
	var names []string
	for name := range s.docs {
		if s.p.Group(name) == g {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var own, trusted []*bundle.Document
	for _, name := range names {
		if d := g.Bundle.Document(name); d.Trusted {
			trusted = append(trusted, d)
		} else {
			own = append(own, d)
		}
	}
	b := bundle.New(g.Kind.Model)
	if len(trusted) > 0 {
		// Trust comes before the bundle's own documents, as it expects.
		b.Trust(s.index(bundle.New(g.Kind.Model), trusted))
	}
	s.bundles[g] = s.index(b, own)
	return s.bundles[g]
}

// index adds the documents to b, and returns b.
func (s *scope) index(b *bundle.Bundle, docs []*bundle.Document) *bundle.Bundle {
	for _, d := range docs {
		b.Index(d.File, s.p.SourceOf(d.File), []ast.Doc{d.Node})
	}
	return b
}

// uses returns a policy's or module's imports.
func uses(doc ast.Doc) []*ast.UseStmt {
	switch d := doc.(type) {
	case *ast.PolicyDoc:
		return d.Uses
	case *ast.ModuleDoc:
		return d.Uses
	}
	return nil
}

// inKind reports whether a diagnostic is inside a kind document of its
// file. The project doesn't record where its kind documents are, so the
// file is parsed again, once, the first time one of its diagnostics asks.
func (s *scope) inKind(e *diag.Error) bool {
	if e.File == "" || !e.Pos.IsValid() {
		return false
	}
	if s.kinds == nil {
		s.kinds = map[string][]ast.Span{}
	}
	spans, ok := s.kinds[e.File]
	if !ok {
		parsed, _ := parser.ParseFile(e.File, s.p.SourceOf(e.File))
		for _, doc := range parsed.Docs {
			if k, isKind := doc.(*ast.KindDoc); isKind {
				spans = append(spans, k.Span)
			}
		}
		s.kinds[e.File] = spans
	}
	for _, span := range spans {
		if span.Pos().Offset <= e.Pos.Offset && e.Pos.Offset < span.End().Offset {
			return true
		}
	}
	return false
}
