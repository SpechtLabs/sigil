// Package format prints Sigil source in its one canonical style, the way
// gofmt does for Go. It's what `sigil fmt` runs, through [Source].
//
// The formatter reprints the syntax tree, so spacing and indentation are
// always normalized, and takes its few layout decisions from where the
// author broke lines: an `and`, `or` or `xor` chain, and the `|` list of
// an enum's values or a decision's reasons, breaks where the source broke
// it, a list, map or argument list is one item per line when its first
// item started a new line, and a `let` keeps its value on the next line
// when it was written there. Comments aren't part of the tree;
// they come from a second pass over the tokens and are put back by
// position, each on its own line or trailing the code it followed.
//
// A decision prints with its reason first and then its payload fields,
// one per line. The formatter also migrates the syntax from before enums:
// a decision written `decision name(fields) { reasons }` prints in the
// current syntax, and a constructor's positional reason prints as a
// leading `reason:` argument. A comment moves with the field it belongs
// to.
//
// Formatting is idempotent and keeps every comment. The formatted source
// parses to the same tree as the original, except for the migration
// above, and that a quantifier or filter body with a top-level `and`,
// `or` or `xor` gains parentheses, which show how far the body extends.
// The package's corpus test and fuzz target check these properties.
package format

import (
	"slices"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/token"
)

// Source formats src, a file of any number of policy, module and kind
// documents. A file that doesn't parse isn't formatted: the result is
// nil and the list holds the parse errors, since reprinting a partial
// tree would drop whatever didn't parse. The file name only appears in
// those errors.
//
// The output uses two spaces per indentation level and ends with a single
// newline, or is empty for a file with nothing in it. Source doesn't modify
// src and is safe for concurrent use.
func Source(file string, src []byte) ([]byte, diag.ErrorList) {
	f, errs := parser.ParseFile(file, src)
	if errs != nil {
		return nil, errs
	}
	comments, seps := scan(src)
	p := &printer{comments: comments}
	p.file(f, seps, len(src))
	return p.bytes(), nil
}

// scan collects the tokens the parser skips or doesn't keep: comments and
// document separators, in source order.
func scan(src []byte) (comments, seps []token.Token) {
	l := lexer.New(src)
	for {
		t := l.Next()
		switch t.Kind {
		case token.EOF:
			return comments, seps
		case token.Comment:
			comments = append(comments, t)
		case token.Separator:
			seps = append(seps, t)
		}
	}
}

// file prints every document, one `---` line between each pair, then the
// comments after the last one, set apart by a blank line.
func (p *printer) file(f *ast.File, seps []token.Token, size int) {
	for i, doc := range f.Docs {
		if i > 0 {
			p.separator(f.Docs[i-1], doc, seps)
		}
		p.open(doc.Pos(), 0, 0, false, true)
		p.doc(doc)
	}
	end := token.Pos{Offset: size + 1}
	p.trail(end)
	p.blank()
	p.flush(end, 0, false, true)
}

// separator ends the document before and prints a `---` line with a blank
// line on each side. Comments above the source's separator stay with the
// document before it, and those below it go with the next document.
func (p *printer) separator(before, next ast.Doc, seps []token.Token) {
	for _, s := range slices.Backward(seps) {
		if s.Pos.Offset > before.End().Offset && s.Pos.Offset < next.Pos().Offset {
			p.flush(s.Pos, 0, true, true)
			p.last = s.Pos.Line
			break
		}
	}
	p.blank()
	p.push(0)
	p.write("---")
	p.blank()
}
