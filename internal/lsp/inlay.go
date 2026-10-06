package lsp

import (
	"encoding/json"
	"slices"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
	"github.com/spechtlabs/sigil/internal/types"
)

// hint is an inlay hint: a type shown right after the name it's the type
// of, at offset.
type hint struct {
	label  string
	offset int
}

// hints returns the types of the names the document declares without
// writing one, between offsets from and to: each let's, and each
// quantifier's and filter's variable's, in source order. A name whose
// type the checker didn't work out has none.
func (v *view) hints(from, to int) []hint {
	var out []hint
	add := func(name *ast.Ident, t types.Type) {
		if s := typeName(t); s != "" && from <= name.End().Offset && name.End().Offset <= to {
			out = append(out, hint{label: ": " + s, offset: name.End().Offset})
		}
	}
	for _, d := range v.docs {
		if d.Info == nil {
			continue
		}
		for _, l := range docLets(d.Node) {
			add(l.Name, d.Info.TypeOf(l.Value))
		}
		for x := range d.Info.Types {
			var name *ast.Ident
			var over ast.Expr
			switch x := x.(type) {
			case *ast.QuantExpr:
				name, over = x.Var, x.Range
			case *ast.FilterExpr:
				name, over = x.Var, x.Range
			default:
				continue
			}
			if l, ok := d.Info.TypeOf(over).(*types.List); ok {
				add(name, l.Elem)
			}
		}
	}
	slices.SortFunc(out, func(a, b hint) int { return a.offset - b.offset })
	return out
}

// inlayHints answers textDocument/inlayHint with the types of the
// document's lets and variables in the range.
func (s *Server) inlayHints(raw json.RawMessage) ([]protocol.InlayHint, *jsonrpc.Error) {
	p, bad := decode[protocol.InlayHintParams](raw)
	if bad != nil {
		return nil, bad
	}
	out := []protocol.InlayHint{}
	v := s.viewOf(p.TextDocument.URI)
	if v == nil {
		return out, nil
	}
	l := newLines(v.src, s.encoding)
	for _, h := range v.hints(l.offset(p.Range.Start), l.offset(p.Range.End)) {
		out = append(out, protocol.InlayHint{Label: h.label, Position: l.position(h.offset), Kind: protocol.InlayHintType})
	}
	return out, nil
}

// docLets returns every let of a document, those in `when` bodies too.
func docLets(d ast.Doc) []*ast.LetStmt {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return letsIn(d.Stmts)
	case *ast.ModuleDoc:
		return d.Lets
	}
	return nil
}

// letsIn returns the lets among stmts, those in `when` bodies too.
func letsIn(stmts []ast.Stmt) []*ast.LetStmt {
	var out []*ast.LetStmt
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			out = append(out, s)
		case *ast.WhenStmt:
			out = append(out, letsIn(s.Body)...)
		}
	}
	return out
}
