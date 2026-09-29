// Package parser turns Sigil source into an AST.
//
// The parser sits between the lexer and the type checker. [ParseFile]
// parses a file of policy, module and kind documents into an [ast.File];
// [ParseExpr] parses a single expression. Both return every diagnostic as a
// [diag.ErrorList], lexical errors included, sorted by position.
//
// Statements are parsed by recursive descent and expressions by a Pratt
// parser, as the grammar at https://sigil.specht-labs.de/reference/grammar/ plans.
// The parser is hand-written so that every error can say what was expected at that point and, where one
// is obvious, how to fix it. It checks syntax only: names, types and the
// rules of a kind are the checker's job.
//
// The parser reads tokens from the lexer on demand, skipping comments, and
// keeps one token of lookahead beyond the current one. That's all the
// grammar needs: one token everywhere, and a second only at the start of a
// call argument. The parser also peeks at the second token in a few other
// places to give a better error, or to stop a dotted name before the `.{`
// of a selective import.
//
// Parsing has no shared state, so separate calls may run concurrently.
package parser

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/token"
)

// parser holds the state of one parse.
type parser struct {
	file string
	lex  *lexer.Lexer
	// lostBody is the `{` of a rule body that a condition missing its
	// last operand read as a map literal, for parseWhen to resume the
	// body from. See missingOperand.
	lostBody *token.Token
	buf      []token.Token // tokens already read past tok, comments removed
	errs     diag.ErrorList
	tok      token.Token // the current token
	// lastSync is the offset where sync last stopped, so a statement that
	// fails at its first token isn't retried forever. See sync.
	lastSync int
	prev     token.Kind // the kind of the token before tok
	// after is the operator whose right operand is being parsed, or
	// OpInvalid at the start of an expression. Prefix forms that bind looser
	// than that operator use it to name the operator in their error.
	after ast.Op
	// afterPos is where the operator in after starts.
	afterPos token.Pos
}

// bailout is the panic value that abandons a parse after an error, the way
// go/parser does it. Using a panic keeps the parse functions free of error
// returns, and run recovers it, so it never leaves the package.
type bailout struct{}

func newParser(file string, src []byte) *parser {
	p := &parser{file: file, lex: lexer.New(src), lastSync: -1}
	p.next()
	return p
}

// scan reads the next non-comment token from the lexer.
func (p *parser) scan() token.Token {
	for {
		t := p.lex.Next()
		if t.Kind != token.Comment {
			return t
		}
	}
}

// next advances to the next token.
func (p *parser) next() {
	p.prev = p.tok.Kind
	if len(p.buf) > 0 {
		p.tok = p.buf[0]
		p.buf = p.buf[1:]
		return
	}
	p.tok = p.scan()
}

// peek returns the token after the current one without consuming it.
func (p *parser) peek() token.Token {
	if len(p.buf) == 0 {
		p.buf = append(p.buf, p.scan())
	}
	return p.buf[0]
}

// run calls fn and recovers the bailout an error raises. Any other panic is
// a bug and propagates.
func (p *parser) run(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r) //nolint:nopanic // not ours: re-raise it unchanged
			}
		}
	}()
	fn()
}

// errors returns every diagnostic in source order: the parser's own and the
// lexer's. The lexer is drained first, so lexical errors after the point
// where the parser gave up are reported too.
func (p *parser) errors() diag.ErrorList {
	for p.tok.Kind != token.EOF {
		p.next()
	}
	if len(p.errs)+len(p.lex.Errors()) == 0 {
		return nil
	}
	all := make(diag.ErrorList, 0, len(p.errs)+len(p.lex.Errors()))
	all = append(all, p.errs...)
	for _, e := range p.lex.Errors() {
		e.File = p.file
		all = append(all, e)
	}
	all.Sort()
	return all
}

// report records a diagnostic for the span pos to end and carries on. A
// second diagnostic at the position of the last one is dropped: it's a
// consequence of the first, not new information.
func (p *parser) report(pos, end token.Pos, msg, help string) {
	if n := len(p.errs); n > 0 && p.errs[n-1].Pos == pos {
		return
	}
	p.errs = append(p.errs, &diag.Error{File: p.file, Msg: msg, Help: help, Pos: pos, End: end})
}

// errorAt records a diagnostic for the span pos to end and abandons the
// parse.
func (p *parser) errorAt(pos, end token.Pos, msg, help string) {
	p.report(pos, end, msg, help)
	panic(bailout{}) //nolint:nopanic // internal control flow, recovered by run
}

// errorTok records a diagnostic covering t and abandons the parse.
func (p *parser) errorTok(t token.Token, msg, help string) {
	p.errorAt(t.Pos, t.End, msg, help)
}

// unexpected fails at the current token with "expected want, found ...".
// An Illegal token is skipped over silently, because the lexer has already
// reported what's wrong with it.
func (p *parser) unexpected(want, help string) {
	if p.tok.Kind == token.Illegal {
		panic(bailout{}) //nolint:nopanic // internal control flow, recovered by run
	}
	p.errorTok(p.tok, fmt.Sprintf("expected %s, found %s", want, found(p.tok)), help)
}

// expect consumes a token of the given kind and returns it, or fails.
func (p *parser) expect(kind token.Kind, help string) token.Token {
	if p.tok.Kind != kind {
		p.unexpected(fmt.Sprintf("`%s`", kind), help)
	}
	t := p.tok
	p.next()
	return t
}

// expectClosing consumes the delimiter that closes open, pointing at the
// opening one when it's missing.
func (p *parser) expectClosing(kind token.Kind, open token.Token) token.Token {
	return p.expect(kind, fmt.Sprintf("to close the `%s` at %s", open.Text, open.Pos))
}

// found describes the current token for an "expected X, found Y" message.
func found(t token.Token) string {
	if t.Kind == token.EOF {
		return "end of file"
	}
	return "`" + t.Text + "`"
}

// span returns the source range of a token.
func span(t token.Token) ast.Span {
	return ast.Span{From: t.Pos, To: t.End}
}

// ParseExpr parses src as one expression, as tests need, and as the Go kind
// builder does to read a payload field's default from a struct tag. The
// file name only appears in diagnostics. The
// whole of src must be the expression: anything after it is an error. On
// failure the expression is nil and the list holds every diagnostic; on
// success the list is nil.
func ParseExpr(file string, src []byte) (ast.Expr, diag.ErrorList) { //nolint:returninterface // an AST root is any expression node
	p := newParser(file, src)
	var x ast.Expr
	p.run(func() {
		x = p.parseExpr(lowest)
		switch p.tok.Kind {
		case token.EOF:
		case token.Separator:
			p.errorTok(p.tok, "`---` separates documents and can't appear inside an expression",
				"if you meant arithmetic, put spaces between the minus signs")
		default:
			p.unexpected("end of the expression", "")
		}
	})
	if errs := p.errors(); len(errs) > 0 {
		return nil, errs
	}
	return x, nil
}
