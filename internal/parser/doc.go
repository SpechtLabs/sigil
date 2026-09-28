package parser

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// ParseFile parses every document in src. The file is returned even when
// there are errors, holding whatever parsed, so tools can keep working on
// a broken bundle; the list is nil only when the file is clean.
//
// Errors recover at the statement level: a broken statement is skipped up
// to the next one, so one typo reports one error and the rest of the
// document still parses. A broken header loses its document, and parsing
// resumes at the next header.
func ParseFile(name string, src []byte) (*ast.File, diag.ErrorList) {
	p := newParser(name, src)
	f := &ast.File{Name: name}

	for {
		sep, ok := p.skipSeparators()
		if p.tok.Kind == token.EOF {
			break
		}
		p.recover(func() {
			switch p.tok.Kind {
			case token.KwPolicy:
				f.Docs = append(f.Docs, p.parsePolicy())
			case token.KwModule:
				f.Docs = append(f.Docs, p.parseModule())
			case token.KwKind:
				f.Docs = append(f.Docs, p.parseKind())
			default:
				p.noHeader(sep, ok)
			}
		}, p.syncDoc)
	}
	return f, p.errors()
}

// skipSeparators consumes any run of `---` tokens and returns the last one.
func (p *parser) skipSeparators() (token.Token, bool) {
	var last token.Token
	ok := false
	for p.tok.Kind == token.Separator {
		last, ok = p.tok, true
		p.next()
	}
	return last, ok
}

// noHeader reports text where a document header should be. Right after a
// `---`, an operand is most likely arithmetic gone wrong, which the docs
// call out; anything else is a statement without a header.
func (p *parser) noHeader(sep token.Token, afterSep bool) {
	if afterSep && startsOperand(p.tok) {
		p.errorTok(sep, "`---` separates documents and can't appear inside an expression",
			"if you meant arithmetic, put spaces between the minus signs")
	}
	p.unexpected("a document header (`policy`, `module` or `kind`)",
		"every document starts with `policy name: Kind`, `module name: Kind` or `kind Name version N`")
}

// startsOperand reports whether t can begin an expression operand.
func startsOperand(t token.Token) bool {
	switch t.Kind {
	case token.Ident, token.Int, token.Float, token.Duration, token.String, token.RawString,
		token.KwTrue, token.KwFalse, token.KwOutcome, token.KwPresent, token.LParen, token.LBracket, token.LBrace, token.Minus:
		return true
	}
	return false
}

// atDocEnd reports whether the current token ends a document: the end of
// the file, a separator or the next header. Header keywords only count at
// statement position; inside braces they're names, and the body parsers
// never call this.
func (p *parser) atDocEnd() bool {
	switch p.tok.Kind {
	case token.EOF, token.Separator, token.KwPolicy, token.KwModule, token.KwKind:
		return true
	}
	return false
}

// parseHeader parses `name: Kind` after a `policy` or `module` keyword.
func (p *parser) parseHeader(kw token.Token) (*ast.PolicyName, *ast.Ident) {
	shape := fmt.Sprintf("a %s starts with `%s name: Kind`", kw.Text, kw.Text)
	name := p.parsePolicyName(fmt.Sprintf("a name after `%s`", kw.Text))
	p.expect(token.Colon, shape)
	kind := p.expectIdent("the kind's name after `:`", shape)
	return name, kind
}

// parsePolicy parses a policy document from its `policy` keyword to the
// end of the document.
func (p *parser) parsePolicy() *ast.PolicyDoc {
	kw := p.tok
	p.next()
	doc := &ast.PolicyDoc{}
	doc.Name, doc.Kind = p.parseHeader(kw)
	end := doc.Kind.End()

	for !p.atDocEnd() {
		p.recover(func() {
			if p.tok.Kind == token.KwUse {
				u := p.parseUse()
				if len(doc.Stmts) > 0 {
					p.report(u.Pos(), u.End(), "`use` must come before every other statement", "move it up, right after the header")
				}
				doc.Uses = append(doc.Uses, u)
				end = u.End()
				return
			}
			s := p.parsePolicyStmt()
			doc.Stmts = append(doc.Stmts, s)
			end = s.End()
		}, p.syncTop)
	}
	doc.Span = ast.Span{From: kw.Pos, To: end}
	return doc
}

// parseModule parses a module document. A module holds only imports and
// lets; anything else is parsed, reported and dropped, so the rest of the
// module still parses.
func (p *parser) parseModule() *ast.ModuleDoc {
	kw := p.tok
	p.next()
	doc := &ast.ModuleDoc{}
	doc.Name, doc.Kind = p.parseHeader(kw)
	end := doc.Kind.End()

	for !p.atDocEnd() {
		p.recover(func() {
			if s := p.parseModuleStmt(doc); s != nil {
				end = s.End()
			}
		}, p.syncTop)
	}
	doc.Span = ast.Span{From: kw.Pos, To: end}
	return doc
}

// parseModuleStmt parses one statement of a module into doc and returns
// it, or nil for a statement a module can't hold, which is reported and
// dropped.
func (p *parser) parseModuleStmt(doc *ast.ModuleDoc) ast.Stmt {
	if doc == nil {
		return nil
	}
	switch p.tok.Kind {
	case token.KwUse:
		u := p.parseUse()
		if len(doc.Lets) > 0 {
			p.report(u.Pos(), u.End(), "`use` must come before every other statement", "move it up, right after the header")
		}
		doc.Uses = append(doc.Uses, u)
		return u
	case token.KwLet, token.KwPub:
		l := p.parseLet()
		doc.Lets = append(doc.Lets, l)
		return l
	}
	s := p.parsePolicyStmt()
	p.report(s.Pos(), s.End(), fmt.Sprintf("a module can't contain %s", describe(s)),
		"a module holds only imports and lets; rules, params and invocations belong in a policy")
	return nil
}

// describe names a statement kind for a message.
func describe(s ast.Stmt) string {
	switch s.(type) {
	case *ast.ParamStmt:
		return "`param`"
	case *ast.WhenStmt:
		return "`when`"
	case *ast.AssertStmt:
		return "`assert`"
	case *ast.CallStmt:
		return "an invocation"
	}
	return fmt.Sprintf("<%T>", s)
}

// try runs fn and reports whether it completed rather than bailing out.
// Bailouts from deeper parse functions unwind to the innermost try, so an
// error inside a `when` body is caught by the body, not by the document.
func (p *parser) try(fn func()) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isBail := r.(bailout); !isBail {
				panic(r) //nolint:nopanic // not ours: re-raise it unchanged
			}
			ok = false
		}
	}()
	fn()
	return true
}

// recover runs fn and, if it bails out, calls sync to skip to a point
// where parsing can resume.
func (p *parser) recover(fn, sync func()) {
	if !p.try(fn) {
		sync()
	}
}

// bail abandons the current parse without adding a diagnostic, for use
// after one has already been reported.
func (p *parser) bail() {
	panic(bailout{}) //nolint:nopanic // internal control flow, recovered by try
}

// sync skips tokens until stop accepts one or the file ends. If the
// parser hasn't moved since the last sync it consumes a token first, so a
// statement that fails at its first token can't be retried forever.
func (p *parser) sync(stop func(token.Token) bool) {
	if p.tok.Pos.Offset == p.lastSync && p.tok.Kind != token.EOF {
		p.next()
	}
	for !stop(p.tok) && p.tok.Kind != token.EOF {
		p.next()
	}
	p.lastSync = p.tok.Pos.Offset
}

// isHeaderKeyword reports whether k starts a document.
func isHeaderKeyword(k token.Kind) bool {
	return k == token.KwPolicy || k == token.KwModule || k == token.KwKind
}

// atHeader reports whether t, the current token, looks like a document
// header while the parser is skipping ahead after an error. At that point
// it has lost track of braces, so it uses the tells that separate a
// header from a field named like a keyword: a field follows a `.` or
// precedes a `:`, and a header starts its line.
func (p *parser) atHeader(t token.Token) bool {
	if !isHeaderKeyword(t.Kind) || p.prev == token.Dot {
		return false
	}
	return p.peek().Kind != token.Colon || t.Pos.Column == 1
}

// syncDoc skips to the next document header or separator.
func (p *parser) syncDoc() {
	p.sync(func(t token.Token) bool {
		return t.Kind == token.Separator || p.atHeader(t)
	})
}

// syncTop skips to the next top-level statement of a policy or module, or
// the end of the document.
func (p *parser) syncTop() {
	p.sync(func(t token.Token) bool {
		switch t.Kind {
		case token.KwUse, token.KwParam, token.KwLet, token.KwPub, token.KwWhen, token.KwAssert, token.Separator:
			return true
		case token.Ident:
			return p.peek().Kind == token.LParen
		}
		return p.atHeader(t)
	})
}

// syncBody skips to the next statement inside a `when` body, the brace
// that closes it, or a token that ends the document. Top-level keywords
// stop it too, so their "move it outside" error still fires.
func (p *parser) syncBody() {
	p.sync(func(t token.Token) bool {
		switch t.Kind {
		case token.KwWhen, token.KwAssert, token.RBrace,
			token.KwUse, token.KwParam, token.KwLet, token.KwPub, token.Separator:
			return true
		case token.Ident:
			return p.peek().Kind == token.LParen
		}
		return p.atHeader(t)
	})
}
