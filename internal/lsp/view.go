package lsp

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// view is what completion, hover and definition answer from: one file's
// current source, its documents checked against a loaded project, and the
// project for everything else: the kinds and the other documents. The
// project can be nil, for a file whose project couldn't be loaded, where
// only what the tokens say is offered.
type view struct {
	proj  *workspace.Project
	file  string             // the name the project reads the file by
	src   []byte             // the file's current source
	docs  []*bundle.Document // the file's policies and modules, checked as src has them
	kinds []*ast.KindDoc     // the file's kind documents
}

// newView parses src, the current source of file, and checks each of its
// policies and modules against its kind and the documents of the project
// it imports, as the project's latest load has them, recording the
// scopes completion, hover and definition read. A document whose kind
// the project doesn't know is left unchecked.
func newView(p *workspace.Project, file string, src []byte) *view {
	v := &view{proj: p, file: file, src: src}
	f, _ := parser.ParseFile(file, src)
	for _, node := range f.Docs {
		if k, ok := node.(*ast.KindDoc); ok {
			v.kinds = append(v.kinds, k)
			continue
		}
		name, kindName := docName(node), headerKind(node)
		if name == nil || kindName == nil {
			continue
		}
		d := &bundle.Document{Node: node, Name: name.String(), File: file}
		if k := v.kindNamed(kindName.Name); k != nil {
			c := check.New(file)
			c.Scopes = true
			c.Resolver = v.resolver(k.Model.Name)
			switch n := node.(type) {
			case *ast.PolicyDoc:
				c.Policy(n, k.Model)
			case *ast.ModuleDoc:
				c.Module(n, k.Model)
			}
			d.Info, d.Exported = c.Info(), c.Exported()
		}
		v.docs = append(v.docs, d)
	}
	return v
}

// resolver resolves a `use` the way the bundle of the kind called name
// does, against the project's latest load.
func (v *view) resolver(name string) check.Resolver {
	g := v.groupOf(name)
	return func(doc string) (*check.Exported, bool) {
		if g == nil {
			return nil, false
		}
		d := g.Bundle.Document(doc)
		switch {
		case d == nil:
			return nil, false
		case d.Kind:
			return &check.Exported{Name: doc, Kind: true}, true
		case d.Exported == nil:
			return nil, false
		}
		return d.Exported, true
	}
}

// sourceOf returns a file's source: the view's own for its file, and the
// project's for any other.
func (v *view) sourceOf(file string) []byte {
	if file == v.file {
		return v.src
	}
	if v.proj == nil {
		return nil
	}
	return v.proj.SourceOf(file)
}

// docAt returns the policy or module at offset: the one that holds it,
// or, past the end of one, the last that starts before it, where a
// statement still being written goes. A kind document in between ends
// the search. It returns nil when there's none.
func (v *view) docAt(offset int) *bundle.Document {
	var out *bundle.Document
	for _, d := range v.docs {
		if d.Node.Pos().Offset <= offset {
			out = d
		}
	}
	if out == nil {
		return nil
	}
	for _, k := range v.kinds {
		if out.Node.Pos().Offset < k.Pos().Offset && k.Pos().Offset <= offset {
			return nil
		}
	}
	return out
}

// docStarting returns the policy or module whose header starts at
// offset, or nil.
func (v *view) docStarting(offset int) *bundle.Document {
	for _, d := range v.docs {
		if d.Node.Pos().Offset == offset {
			return d
		}
	}
	return nil
}

// kindNamed returns the kind called name, or nil.
func (v *view) kindNamed(name string) *workspace.Kind {
	if v.proj == nil {
		return nil
	}
	for _, k := range v.proj.Kinds() {
		if k.Model.Name == name {
			return k
		}
	}
	return nil
}

// groupOf returns the group of the kind called name, or nil when no
// document of it was read.
func (v *view) groupOf(name string) *workspace.Group {
	if v.proj == nil {
		return nil
	}
	for _, g := range v.proj.Groups() {
		if g.Kind.Model.Name == name {
			return g
		}
	}
	return nil
}

// document returns the document called name, trusted or not, as its
// kind's bundle holds it, or nil.
func (v *view) document(name string) *bundle.Document {
	if v.proj == nil {
		return nil
	}
	if g := v.proj.Group(name); g != nil {
		return g.Bundle.Document(name)
	}
	return nil
}

// scopeOf returns the names visible at offset in d: the checker's scope
// there, or the kind's names alone when d wasn't checked, such as a
// document whose header doesn't parse yet. kindName is the kind its header
// names. It returns nil when the kind isn't known.
func (v *view) scopeOf(d *bundle.Document, kindName string, offset int) *check.Env {
	if d != nil && d.Info != nil {
		if env := d.Info.ScopeAt(offset); env != nil {
			return env
		}
	}
	if k := v.kindNamed(kindName); k != nil {
		return check.NewEnv(k.Model)
	}
	return nil
}

// typeOf types src, an expression, in env, or returns nil when it
// doesn't parse.
func (v *view) typeOf(src []byte, env *check.Env) types.Type { //nolint:returninterface // a type is any of five kinds
	x, errs := parser.ParseExpr(v.file, src)
	if errs != nil || env == nil {
		return nil
	}
	return check.New(v.file).Expr(x, env)
}

// headerKind returns the name of the kind d's header names.
func headerKind(d ast.Doc) *ast.Ident {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return d.Kind
	case *ast.ModuleDoc:
		return d.Kind
	}
	return nil
}

// kindOf returns the kind a document is checked against, or nil.
func (v *view) kindOf(d *bundle.Document) *kind.Kind {
	if d == nil {
		return nil
	}
	if k := headerKind(d.Node); k != nil {
		if known := v.kindNamed(k.Name); known != nil {
			return known.Model
		}
	}
	return nil
}

// within reports whether offset falls inside n, its end included, so a
// cursor right after a name is on it.
func within(n ast.Node, offset int) bool {
	return n != nil && n.Pos().IsValid() && n.Pos().Offset <= offset && offset <= n.End().Offset
}

// span is a node's range, as offsets.
func span(n ast.Node) (int, int) { return n.Pos().Offset, n.End().Offset }
