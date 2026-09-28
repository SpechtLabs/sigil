package format

import (
	"strings"
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/token"
)

// indentUnit is one level of indentation.
const indentUnit = "  "

// printer builds the output line by line, so trailing comments can be
// aligned once every line is known.
type printer struct {
	lines    []*line
	comments []token.Token // every comment in the source, in order
	next     int           // index of the first comment not printed yet
	last     int           // source line of the last token or comment printed
}

// line is one output line: its indentation level, its code, and the
// comment that trails the code, if any. A line with neither is blank.
type line struct {
	comment string
	code    strings.Builder
	indent  int
}

// write appends s to the current line.
func (p *printer) write(s string) {
	if len(p.lines) == 0 {
		p.push(0)
	}
	p.lines[len(p.lines)-1].code.WriteString(s)
}

// open ends the current line and starts one at indent for the token at
// next. Comments before next are printed first: one that sat on the line
// the current line's code came from trails it, the others get their own
// lines at commentIndent. A blank line in the source survives before a
// comment when lead allows it for the first item and always between two
// comments, and before next when tail allows it; never more than one.
func (p *printer) open(next token.Pos, indent, commentIndent int, lead, tail bool) {
	first := p.flush(next, commentIndent, lead, true)
	if next.Line > p.last+1 && tail && (lead || !first) {
		p.blank()
	}
	p.push(indent)
}

// section is open for an item that a blank line always sets apart from
// the one before it, above any comments that lead into it.
func (p *printer) section(next token.Pos, indent int) {
	p.trail(next)
	p.blank()
	p.open(next, indent, indent, false, true)
}

// close is open for a closing bracket: comments before it are indented
// like the items they follow, and no blank line is kept before it.
func (p *printer) close(next token.Pos, indent, commentIndent int, blanks bool) {
	p.flush(next, commentIndent, blanks, blanks)
	p.push(indent)
}

// flush prints the comments that come before next, as open describes,
// and reports whether there were none on lines of their own.
func (p *printer) flush(next token.Pos, indent int, lead, between bool) (none bool) {
	p.trail(next)
	none = true
	for ; p.next < len(p.comments) && p.comments[p.next].Pos.Offset < next.Offset; p.next++ {
		c := p.comments[p.next]
		if c.Pos.Line > p.last+1 && ((none && lead) || (!none && between)) {
			p.blank()
		}
		p.push(indent)
		p.write(c.Text)
		p.last = c.Pos.Line
		none = false
	}
	return none
}

// trail attaches the next comment to the current line when it sat on the
// line that line's code came from, and comes before next.
func (p *printer) trail(next token.Pos) {
	if p.next >= len(p.comments) {
		return
	}
	c := p.comments[p.next]
	if cur := p.current(); c.Pos.Offset < next.Offset && c.Pos.Line == p.last && cur != nil && cur.code.Len() > 0 && cur.comment == "" {
		cur.comment = c.Text
		p.next++
	}
}

// current returns the line being written, or nil before the first.
func (p *printer) current() *line {
	if len(p.lines) == 0 {
		return nil
	}
	return p.lines[len(p.lines)-1]
}

// push starts a new line at indent.
func (p *printer) push(indent int) {
	p.lines = append(p.lines, &line{indent: indent})
}

// blank adds a blank line, unless there's nothing yet or the last line
// is blank already.
func (p *printer) blank() {
	cur := p.current()
	if cur == nil || (cur.code.Len() == 0 && cur.comment == "") {
		return
	}
	p.lines = append(p.lines, &line{})
}

// indentOf returns the indentation level of the line being written.
func (p *printer) indentOf() int {
	if cur := p.current(); cur != nil {
		return cur.indent
	}
	return 0
}

// bytes renders the lines. Trailing comments on consecutive lines are
// aligned one space after the longest code among them. The output ends
// with a single newline, or is empty for a file with nothing in it.
func (p *printer) bytes() []byte {
	for len(p.lines) > 0 {
		last := p.lines[len(p.lines)-1]
		if last.code.Len() > 0 || last.comment != "" {
			break
		}
		p.lines = p.lines[:len(p.lines)-1]
	}
	texts := make([]string, len(p.lines))
	for i, l := range p.lines {
		if l.code.Len() > 0 {
			texts[i] = strings.Repeat(indentUnit, l.indent) + l.code.String()
		}
	}
	var b strings.Builder
	for i := 0; i < len(p.lines); {
		j := i
		width := 0
		for j < len(p.lines) && p.lines[j].comment != "" && texts[j] != "" {
			width = max(width, utf8.RuneCountInString(texts[j]))
			j++
		}
		if j == i {
			b.WriteString(strings.TrimRight(texts[i], " "))
			b.WriteByte('\n')
			i++
			continue
		}
		for ; i < j; i++ {
			pad := width - utf8.RuneCountInString(texts[i])
			b.WriteString(texts[i] + strings.Repeat(" ", pad) + " " + p.lines[i].comment)
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}
