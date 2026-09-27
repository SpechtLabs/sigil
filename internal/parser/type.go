package parser

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// parseType parses a type expression: `?T`, `list<T>`, `map<K, V>` or a
// name. `list` and `map` are ordinary identifiers to the lexer; only their
// position after a type keyword or `:` makes them types, and only a
// following `<` makes them generic.
func (p *parser) parseType() ast.Type {
	switch p.tok.Kind {
	case token.Coalesce:
		p.errorTok(p.tok, "optional types don't nest", "write `?T` with a single `?`")
	case token.Question:
		q := p.tok
		p.next()
		if p.tok.Kind == token.Question || p.tok.Kind == token.Coalesce {
			p.errorAt(q.Pos, p.tok.End, "optional types don't nest", "write `?T` with a single `?`")
		}
		return &ast.OptionalType{QPos: q.Pos, Elem: p.parseBaseType()}
	}
	return p.parseBaseType()
}

func (p *parser) parseBaseType() ast.Type {
	name := p.expectIdent("a type", "")
	switch name.Name {
	case "list":
		open := p.expect(token.Lt, "a list type is written `list<T>`")
		elem := p.parseType()
		end := p.closeTypeArgs(open)
		return &ast.ListType{Elem: elem, Span: ast.Span{From: name.Pos(), To: end}}
	case "map":
		open := p.expect(token.Lt, "a map type is written `map<K, V>`")
		key := p.parseType()
		p.expect(token.Comma, "a map type is written `map<K, V>`")
		value := p.parseType()
		end := p.closeTypeArgs(open)
		return &ast.MapType{Key: key, Value: value, Span: ast.Span{From: name.Pos(), To: end}}
	}
	return &ast.NamedType{Name: name}
}

// closeTypeArgs consumes the `>` that closes a type argument list and
// returns the position after it. The lexer takes `>=` as one token, which
// is wrong in `param m: map<string, int>= {}`; here, and only here, that
// token is split into `>` and a following `=`.
func (p *parser) closeTypeArgs(open token.Token) token.Pos {
	switch p.tok.Kind {
	case token.Gt:
		end := p.tok.End
		p.next()
		return end
	case token.GtEq:
		t := p.tok
		gtEnd := token.Pos{Offset: t.Pos.Offset + 1, Line: t.Pos.Line, Column: t.Pos.Column + 1}
		p.tok = token.Token{Kind: token.Assign, Text: "=", Pos: gtEnd, End: t.End}
		return gtEnd
	}
	p.expectClosing(token.Gt, open)
	return token.Pos{}
}
