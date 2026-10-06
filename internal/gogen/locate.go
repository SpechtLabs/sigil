package gogen

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
)

// span is where a declaration sits in the kind file.
type span struct{ start, end token.Pos }

// spans maps a [kind.Locator] key to where the kind file declares it.
type spans map[string]span

// locate returns a [kind.Locator] for the kind document doc, which finds
// the declarations Generate's diagnostics are about: `kind`, `enum E`,
// `type T`, `precedence`, `default.arg F` and
// `conflict.arg F`, keyed as [kind.Locator] documents. A nil doc locates
// nothing.
func locate(doc *ast.KindDoc) kind.Locator {
	loc := spans{}
	if doc != nil {
		loc.set("kind", doc.Name)
		for _, d := range doc.Decls {
			loc.decl(d)
		}
	}
	return func(key string) (token.Pos, token.Pos, bool) {
		s, ok := loc[key]
		return s.start, s.end, ok
	}
}

// set records where n is, under key.
func (loc spans) set(key string, n ast.Node) {
	loc[key] = span{n.Pos(), n.End()}
}

// decl records where the declaration d, or the part of it a diagnostic
// points at, is.
func (loc spans) decl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.EnumDecl:
		loc.set("enum "+d.Name.Name, d.Name)
	case *ast.TypeDecl:
		loc.set("type "+d.Name.Name, d.Name)
	case *ast.PrecedenceDecl:
		if d.Scope == nil {
			loc.set("precedence", d)
		}
	case *ast.DefaultDecl:
		loc.args(d.Call, "default")
	case *ast.ConflictDecl:
		loc.args(d.Call, "conflict")
	}
}

// args records where the named arguments of a default or conflict
// constructor are, under prefix.arg F.
func (loc spans) args(call *ast.CallStmt, prefix string) {
	if call == nil {
		return
	}
	for _, a := range call.Args {
		if a.Name != nil {
			loc.set(prefix+".arg "+a.Name.Name, a.Name)
		}
	}
}
