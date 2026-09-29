package diag

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Theme styles the parts of a rendered diagnostic. Each function receives
// one part and returns it styled; [Plain] returns every part unchanged. The
// CLI builds a Theme from its terminal palette, so the diagnostic keeps
// one layout whether it's colored or piped. The renderer calls every
// field without checking for nil, so a Theme must set them all.
type Theme struct {
	Location func(s string) string               // `file:line:col`
	Doc      func(s string) string               // ` (document)` after the position
	Severity func(sev Severity) string           // the `error` or `warning` label
	Message  func(s string) string               // what's wrong
	Code     func(s string) string               // ` [lint-name]` after the message
	Gutter   func(s string) string               // the line number and the `|` rail
	Source   func(s string) string               // the quoted source line
	Caret    func(s string, sev Severity) string // the carets under the span
	Help     func(s string) string               // the `= help:` label
	HelpText func(s string) string               // the fix
}

// Plain renders every part as it is. [Render] uses it, and [RenderWith]
// falls back to it for a nil theme. Callers must not modify it.
var Plain = &Theme{
	Location: identity,
	Doc:      identity,
	Severity: Severity.String,
	Message:  identity,
	Code:     identity,
	Gutter:   identity,
	Source:   identity,
	Caret:    func(s string, _ Severity) string { return s },
	Help:     identity,
	HelpText: identity,
}

// Sources looks up a file's source for the renderer, or returns nil when
// the file is unknown, in which case the diagnostic quotes no line.
type Sources func(file string) []byte

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
// of that line. When src is nil or has no line at e's position, the source
// block is left out and only the header and help are rendered. The result
// ends with a newline. This is the plain form that tests and non-terminal
// output share; [RenderWith] styles the same layout for a terminal.
func Render(e *Error, src []byte) string {
	return RenderWith(e, src, Plain)
}

// RenderWith renders e the way [Render] does, styled by t. A nil theme
// renders plain, and a nil e renders as the empty string.
func RenderWith(e *Error, src []byte, t *Theme) string {
	if e == nil {
		return ""
	}
	if t == nil {
		t = Plain
	}
	var b strings.Builder
	writeHeader(&b, e, t)
	b.WriteString("\n")

	line, ok := sourceLine(src, e.Pos.Line)
	if !ok {
		if e.Help != "" {
			b.WriteString("  " + t.Help("= help:") + " " + t.HelpText(e.Help) + "\n")
		}
		return b.String()
	}

	num := strconv.Itoa(e.Pos.Line)
	pad := strings.Repeat(" ", len(num))
	b.WriteString(t.Gutter(pad+" |") + "\n")
	b.WriteString(t.Gutter(num+" |") + " " + t.Source(line) + "\n")
	lead, carets := underline(line, e)
	b.WriteString(t.Gutter(pad+" |") + " " + lead + t.Caret(carets, e.Severity) + "\n")
	if e.Help != "" {
		b.WriteString(pad + " " + t.Help("= help:") + " " + t.HelpText(e.Help) + "\n")
	}
	return b.String()
}

// RenderAll renders every diagnostic, one after another with a blank line
// between them, each quoting the line src finds for its file. A nil src
// quotes no lines and a nil t renders plain. Unlike [RenderWith], the
// result has no trailing newline.
func RenderAll(errs ErrorList, src Sources, t *Theme) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		var text []byte
		if src != nil {
			text = src(e.File)
		}
		parts[i] = strings.TrimRight(RenderWith(e, text, t), "\n")
	}
	return strings.Join(parts, "\n\n")
}

// writeHeader writes the first line: where, how serious, and what.
func writeHeader(b *strings.Builder, e *Error, t *Theme) {
	if e == nil {
		return
	}
	if e.File != "" || e.Pos.IsValid() {
		loc := e.File
		if e.Pos.IsValid() {
			if loc != "" {
				loc += ":"
			}
			loc += e.Pos.String()
		}
		b.WriteString(t.Location(loc))
		if e.Pos.IsValid() && e.Doc != "" && !PathMatches(e.File, e.Doc) {
			b.WriteString(t.Doc(" (" + e.Doc + ")"))
		}
		b.WriteString(": ")
	}
	b.WriteString(t.Severity(e.Severity) + ": " + t.Message(e.Msg))
	if e.Code != "" {
		b.WriteString(t.Code(" [" + e.Code + "]"))
	}
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

// underline builds the caret line for e under line: the whitespace up to
// the start column, copying tabs so the carets stay aligned in a terminal,
// and one caret per character of the span on this line.
func underline(line string, e *Error) (lead, carets string) {
	if e == nil {
		return "", ""
	}
	var b strings.Builder
	end := utf8.RuneCountInString(line) + 1
	start := max(1, min(e.Pos.Column, end))
	col := 1
	for _, r := range line {
		if col >= start {
			break
		}
		if r == '\t' {
			b.WriteByte('\t')
		} else {
			b.WriteByte(' ')
		}
		col++
	}

	if e.End.Line == e.Pos.Line && e.End.Column > start {
		end = min(end, e.End.Column)
	}
	return b.String(), strings.Repeat("^", max(1, end-start))
}

func identity(s string) string { return s }
