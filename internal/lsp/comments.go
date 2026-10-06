package lsp

import (
	"bytes"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
)

// The syntax tree keeps no comments, so a declaration's doc comment is
// read from the source: the `//` lines right above the line that declares
// it, as Go writes one.

// kindComment returns the doc comment of the declaration in the kind
// called kindName that find picks, or "" for none, and for a kind linked
// into the binary, which has no source.
func (v *view) kindComment(kindName string, find func(ast.Decl) *ast.Ident) string {
	if v.proj == nil {
		return ""
	}
	doc, file := v.proj.KindSource(kindName)
	if doc == nil {
		return ""
	}
	for _, d := range doc.Decls {
		if id := find(d); id != nil {
			return comment(v.proj.SourceOf(file), id.Pos().Offset)
		}
	}
	return ""
}

// letComment returns the doc comment of the let called name in the
// document called docName, or "".
func (v *view) letComment(docName, name string) string {
	d := v.document(docName)
	if d == nil {
		return ""
	}
	l := letIn(d.Node, name)
	if l == nil {
		return ""
	}
	return comment(v.proj.SourceOf(d.File), l.Pos().Offset)
}

// comment returns the comment on the lines right above the one offset is
// on in src, without its slashes, its lines joined as markdown joins
// them: a blank comment line starts a paragraph. It's "" when the line
// above isn't a comment.
func comment(src []byte, offset int) string {
	if offset > len(src) {
		return ""
	}
	end := bytes.LastIndexByte(src[:offset], '\n')
	var lines []string
	for end >= 0 {
		start := bytes.LastIndexByte(src[:end], '\n') + 1
		line := strings.TrimSpace(string(src[start:end]))
		text, ok := strings.CutPrefix(line, "//")
		if !ok {
			break
		}
		lines = append(lines, strings.TrimPrefix(text, " "))
		end = start - 1
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
