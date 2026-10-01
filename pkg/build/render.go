package build

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/format"
	"github.com/spechtlabs/sigil/internal/parser"
)

// refNode reads a pub let of another document.
type refNode struct {
	target Importable
	name   string
	s      Site
}

// imports collects the `use` statements a document needs, from its
// Ref and Invoke calls.
type imports struct {
	paths   map[string]*use
	invoked map[string]bool // the paths the document invokes, found before lowering
}

// use is the import of one path: a whole import when the document
// invokes it, otherwise the pub lets it reads.
type use struct {
	items map[string]Site // the pub lets read, with the first Ref of each
	site  Site            // the first Ref or Invoke
}

// span is the lines a statement of a rendered document spans, and the
// builder call that made it.
type span struct {
	site     Site
	from, to int
}

// lower reads the let as its import binds it: by its own name, or
// qualified by the document when that's imported whole.
func (n *refNode) lower(l *lowerer) ast.Expr {
	path, ok := l.target(n.target, n.s)
	if !ok {
		return &ast.BadExpr{}
	}
	if msg := identError(n.name); msg != "" {
		l.errorf(n.s, "pub let %s", msg)
		return &ast.BadExpr{}
	}
	if d := n.target.built(); d != nil {
		if found, names := d.pub(n.name); !found {
			exports := "it exports no pub lets"
			if len(names) > 0 {
				exports = "it exports: " + strings.Join(names, ", ")
			}
			l.errorf(n.s, "%s has no pub let %s; %s", path, n.name, exports)
			return &ast.BadExpr{}
		}
	}
	l.uses.ref(path, n.name, n.s)
	if l.uses.invoked[path] {
		return &ast.SelectorExpr{X: &ast.Ident{Name: last(path)}, Sel: &ast.Ident{Name: n.name}}
	}
	return &ast.Ident{Name: n.name}
}

// target checks a document the one being lowered imports, and returns
// its path.
func (l *lowerer) target(t Importable, s Site) (string, bool) {
	if isNil(t) {
		l.errorf(s, "the document is nil")
		return "", false
	}
	if e, ok := t.(*ExternDoc); ok && e.err != nil {
		l.errs = append(l.errs, e.err)
		return "", false
	}
	path, d := t.Name(), t.built()
	switch {
	case d == l.doc || path == l.doc.name:
		l.errorf(s, "%s can't import itself", path)
		return "", false
	case d != nil && d.contract != nil && d.contract.Model.Name != l.model.Name:
		l.errorf(s, "%s is built for kind %s, and %s for kind %s; a document imports documents of its own kind", path, d.contract.Model.Name, l.doc.name, l.model.Name)
		return "", false
	}
	return path, true
}

// invocation records an invocation of t and returns the name the import
// binds it to.
func (l *lowerer) invocation(t Invocable, s Site) string {
	path, ok := l.target(t, s)
	if !ok {
		return "<error>"
	}
	l.uses.invoke(path, s)
	return last(path)
}

// source renders the document, or returns every error.
func (d *document) source() ([]byte, error) {
	out, _, errs := d.render()
	if errs != nil {
		return nil, errs
	}
	return out, nil
}

// render renders the document and records where each statement landed.
// The text is built with line breaks where they help, then formatted by
// `sigil fmt`'s printer, which keeps them.
func (d *document) render() ([]byte, []span, Errors) {
	if d.contract == nil {
		return nil, nil, d.errs
	}
	l := &lowerer{doc: d, model: d.contract.Model, binding: d.contract.Binding, uses: &imports{paths: map[string]*use{}, invoked: map[string]bool{}}}
	l.uses.scan(d.top)
	r := &renderer{l: l}
	r.stmts(d.top, 0)
	uses, useSites, useErrs := l.uses.render(d)

	errs := append(append(append(Errors{}, d.errs...), l.errs...), useErrs...)
	if len(errs) > 0 {
		slices.SortStableFunc(errs, func(a, b *Error) int {
			return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
		})
		return nil, nil, errs
	}

	var src strings.Builder
	if d.header != "" {
		for line := range strings.SplitSeq(d.header, "\n") {
			src.WriteString(strings.TrimRight("// "+line, " \t\r") + "\n")
		}
		src.WriteString("\n")
	}
	keyword := "policy"
	if d.module {
		keyword = "module"
	}
	fmt.Fprintf(&src, "%s %s: %s@%d\n", keyword, d.name, d.contract.Model.Name, d.contract.Model.Version)
	for _, u := range uses {
		src.WriteString("\n" + u)
	}
	src.WriteString(r.b.String())
	src.WriteString("\n")

	out, perrs := format.Source(d.path, []byte(src.String()))
	if perrs != nil {
		return nil, nil, Errors{d.site.errorf("the rendered source doesn't parse, which is a bug in pkg/build: %s\n%s", perrs[0].Error(), src.String())}
	}
	return out, d.spans(out, useSites), nil
}

// spans maps the statements of the rendered source back to the builder
// calls that made them: the header to the document's, each `use` to the
// first Ref or Invoke of its path, and each statement to its own. A
// statement's span comes before those of the statements nested in it.
func (d *document) spans(out []byte, useSites []Site) []span {
	f, _ := parser.ParseFile(d.path, out) // the formatter's output parses, with the header first
	doc := f.Docs[0]
	spans := []span{{site: d.site, from: doc.Pos().Line, to: doc.Pos().Line}}
	var uses []*ast.UseStmt
	var stmts []ast.Stmt
	switch doc := doc.(type) {
	case *ast.ModuleDoc:
		uses = doc.Uses
		for _, l := range doc.Lets {
			stmts = append(stmts, l)
		}
	case *ast.PolicyDoc:
		uses, stmts = doc.Uses, doc.Stmts
	}
	for i, u := range uses {
		if i < len(useSites) {
			spans = append(spans, span{site: useSites[i], from: u.Pos().Line, to: u.End().Line})
		}
	}
	return match(d.top, stmts, spans)
}

// ref records a Ref of the pub let name of path.
func (u *imports) ref(path, name string, s Site) {
	p := u.use(path, s)
	if _, ok := p.items[name]; !ok {
		p.items[name] = s
	}
}

// invoke records an invocation of path.
func (u *imports) invoke(path string, s Site) {
	u.use(path, s)
}

// use returns the import of path, starting it at s.
func (u *imports) use(path string, s Site) *use {
	p, ok := u.paths[path]
	if !ok {
		p = &use{items: map[string]Site{}, site: s}
		u.paths[path] = p
	}
	return p
}

// scan finds the paths a body invokes, before anything is lowered, so a
// Ref to an invoked policy reads through the whole import.
func (u *imports) scan(b *body) {
	for _, s := range b.stmts {
		switch s := s.(type) {
		case *callStmt:
			if e, extern := s.target.(*ExternDoc); s.target != nil && (!extern || e.err == nil) {
				u.invoked[s.target.Name()] = true
			}
		case *whenStmt:
			u.scan(s.inner)
		}
	}
}

// render returns the `use` statements, sorted by path, with the builder
// call each one comes from. Two imports that bind one name, or an import
// that takes the name of one of the document's lets or params, are
// errors: an import is never renamed silently.
func (u *imports) render(d *document) (lines []string, sites []Site, errs Errors) {
	bound := map[string]string{} // name to the path that binds it
	bind := func(name, path string, s Site) {
		if prev, ok := bound[name]; ok {
			errs = append(errs, s.errorf("importing %s from %s collides with %s imported from %s; every name in a document means one thing", name, path, name, prev))
			return
		}
		if decl, ok := d.names[name]; ok {
			errs = append(errs, s.errorf("importing %s from %s collides with the let or param %s declared at %s; rename the let or param", name, path, name, decl.at()))
			return
		}
		bound[name] = path
	}
	for _, path := range slices.Sorted(maps.Keys(u.paths)) {
		p := u.paths[path]
		sites = append(sites, p.site)
		if u.invoked[path] {
			bind(last(path), path, p.site)
			lines = append(lines, "use "+path)
			continue
		}
		items := slices.Sorted(maps.Keys(p.items))
		for _, name := range items {
			bind(name, path, p.items[name])
		}
		lines = append(lines, "use "+path+".{"+strings.Join(items, ", ")+"}")
	}
	return lines, sites, errs
}

// match pairs the statements of b, comments left out, with the parsed
// statements they rendered as, appending each one's span to spans.
func match(b *body, parsed []ast.Stmt, spans []span) []span {
	i := 0
	for _, s := range b.stmts {
		if isComment(s) || i >= len(parsed) {
			continue
		}
		n := parsed[i]
		i++
		spans = append(spans, span{site: s.site(), from: n.Pos().Line, to: n.End().Line})
		if w, ok := s.(*whenStmt); ok {
			if pw, ok := n.(*ast.WhenStmt); ok {
				spans = match(w.inner, pw.Body, spans)
			}
		}
	}
	return spans
}

// at returns the builder call of the innermost statement that spans
// line, or false when none does.
func at(spans []span, line int) (Site, bool) {
	var site Site
	found := false
	for _, s := range spans {
		if s.from <= line && line <= s.to {
			site, found = s.site, true
		}
	}
	return site, found
}

// last returns the last segment of a dotted name.
func last(path string) string {
	return path[strings.LastIndexByte(path, '.')+1:]
}
