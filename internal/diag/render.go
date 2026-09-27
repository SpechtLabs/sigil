package diag

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Render formats e in the layout the documentation uses, quoting the
// offending line of src with a caret under the span:
//
//	deploy/production.sigil:9:3: error: expected ..., found `let`
//	  |
//	9 |   let tmp = release.soak
//	  |   ^^^
//	  = help: `let` is only allowed at the top level
//
// A span that runs past the end of its first line is underlined to the end
// of that line. Colors and terminal width are the CLI's concern; this is
// the plain form that tests and non-terminal output share.
func Render(e *Error, src []byte) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(e.Error())
	b.WriteString("\n")

	line, ok := sourceLine(src, e.Pos.Line)
	if !ok {
		if e.Help != "" {
			b.WriteString("  = help: " + e.Help + "\n")
		}
		return b.String()
	}

	num := strconv.Itoa(e.Pos.Line)
	pad := strings.Repeat(" ", len(num))
	b.WriteString(pad + " |\n")
	b.WriteString(num + " | " + line + "\n")
	b.WriteString(pad + " | " + underline(line, e) + "\n")
	if e.Help != "" {
		b.WriteString(pad + " = help: " + e.Help + "\n")
	}
	return b.String()
}

// sourceLine returns line n of src (1-based) without its line ending.
func sourceLine(src []byte, n int) (string, bool) {
	if n < 1 {
		return "", false
	}
	rest := src
	for i := 1; i < n; i++ {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return "", false
		}
		rest = rest[nl+1:]
	}
	if nl := bytes.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	return string(bytes.TrimSuffix(rest, []byte("\r"))), true
}

// underline builds the caret line for e under line: whitespace up to the
// start column, copying tabs so the carets stay aligned in a terminal, then
// one caret per character of the span on this line.
func underline(line string, e *Error) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	col := 1
	for _, r := range line {
		if col >= e.Pos.Column {
			break
		}
		if r == '\t' {
			b.WriteByte('\t')
		} else {
			b.WriteByte(' ')
		}
		col++
	}

	width := utf8.RuneCountInString(line) - e.Pos.Column + 1
	if e.End.Line == e.Pos.Line && e.End.Column > e.Pos.Column {
		width = e.End.Column - e.Pos.Column
	}
	if width < 1 {
		width = 1
	}
	b.WriteString(strings.Repeat("^", width))
	return b.String()
}
