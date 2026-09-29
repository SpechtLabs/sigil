// Package ui holds the terminal components every devtool command is built
// from, so they all look and read alike: a header box with the run's plan,
// numbered steps with a live status line, aligned tables, lists, and the
// durations and counts they show.
package ui

import (
	"os"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// clearLine returns the cursor to the start of the line and erases it.
const clearLine = "\r\x1b[K"

// Line is a status line. On a terminal, Set redraws it in place; anywhere
// else it's silent, so logs only get the lines a command prints for good.
type Line struct {
	p     *pretty.Printer
	width int
	shown bool
}

// NewLine returns a Line that writes through p. Its width is width when
// that's positive, and otherwise the terminal's, or 80 when stdout isn't
// one.
func NewLine(p *pretty.Printer, width int) *Line {
	if width <= 0 {
		width = 80
		if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
			width = w
		}
	}
	return &Line{p: p, width: width}
}

// Live reports whether the line updates in place.
func (l *Line) Live() bool {
	return l.p.Theme().TTY()
}

// Set replaces the status line with s, cut to the terminal's width so it
// never wraps: a wrapped line can't be redrawn in place.
func (l *Line) Set(s string) humane.Error {
	if !l.Live() {
		return nil
	}
	l.shown = true
	return l.p.Print(clearLine + ansi.Truncate(s, l.width-1, "…"))
}

// Clear erases the status line, so the next output starts on a clean one.
func (l *Line) Clear() humane.Error {
	if !l.shown {
		return nil
	}
	l.shown = false
	return l.p.Print(clearLine)
}
