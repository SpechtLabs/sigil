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

// view is what completion, hover and definition answer from: a loaded
// project, one of its files, and that file's source as the load read it.
// The project can be nil, for a file no project read, where only what the
// tokens say is offered.
type view struct {
	proj *workspace.Project
	file string // the name the project reads the file by
	src  []byte
}

// docAt returns the policy or module at offset: the one that holds it,
// or, past the end of one, the last that starts before it, where a
// statement still being written goes. A kind document in between ends
// the search. It returns nil when there's none.
func (v *view) docAt(offset int) *bundle.Document {
	if v.proj == nil {
		return nil
	}
	var out *bundle.Document
	for _, d := range v.proj.DocumentsIn(v.file) {
		if d.Node.Pos().Offset <= offset {
			out = d
		}
	}
	if out == nil {
		return nil
	}
	for _, k := range v.kindDocs() {
		if out.Node.Pos().Offset < k.Pos().Offset && k.Pos().Offset <= offset {
			return nil
		}
	}
	return out
}

// docStarting returns the policy or module whose header starts at
// offset, or nil.
func (v *view) docStarting(offset int) *bundle.Document {
	if v.proj == nil {
		return nil
	}
	for _, d := range v.proj.DocumentsIn(v.file) {
		if d.Node.Pos().Offset == offset {
			return d
		}
	}
	return nil
}

// kindDocs returns the kind documents of the file, parsed from its
// source: the project indexes them only by kind.
func (v *view) kindDocs() []*ast.KindDoc {
	f, _ := parser.ParseFile(v.file, v.src)
	var out []*ast.KindDoc
	for _, d := range f.Docs {
		if k, ok := d.(*ast.KindDoc); ok {
			out = append(out, k)
		}
	}
	return out
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
