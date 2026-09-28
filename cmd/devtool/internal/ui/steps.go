package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// Steps shows a run as numbered steps. On a terminal, the current step is
// a status line that redraws in place with its progress and the time left
// for the whole run; every finished step leaves one line:
//
//	✓ [ 3/24] FuzzLexer  ./internal/lexer  160k execs (32k/s) · 4 new inputs
//
// Anywhere else, and whenever the command streams its tools' own output,
// there's no status line: a step prints a line naming it before its output
// (when streaming), and the same finished line after.
type Steps struct {
	p      *pretty.Printer
	line   *Line
	stream bool
	total  int
	// subjectWidth and contextWidth align the steps' columns.
	subjectWidth, contextWidth int

	start    time.Time
	finished int
	subject  string
	context  string
}

// NewSteps returns the steps of a run with total of them. stream says the
// command writes its tools' output between the step lines.
func NewSteps(p *pretty.Printer, total int, stream bool) *Steps {
	return &Steps{p: p, line: NewLine(p, 0), stream: stream, total: total, start: time.Now()}
}

// Align widens the columns so a step with this subject and context lines
// up with the others. Call it for every step before the first starts.
func (s *Steps) Align(subject, context string) {
	s.subjectWidth = max(s.subjectWidth, ansi.StringWidth(subject))
	s.contextWidth = max(s.contextWidth, ansi.StringWidth(context))
}

// Next starts the next step.
func (s *Steps) Next(subject, context string) humane.Error {
	s.subject, s.context = subject, context
	if s.live() {
		return s.Progress("", 0)
	}
	if s.stream {
		return s.p.Print(strings.TrimRight("  "+s.label(), " ") + "\n")
	}
	return nil
}

// Progress redraws the status line with the step's detail and progress,
// from 0 to 1, when that's known; the time left is estimated from it.
// context replaces the step's context when it isn't empty.
func (s *Steps) Progress(detail string, progress float64) humane.Error {
	if !s.live() {
		return nil
	}
	elapsed := time.Since(s.start)
	timing := Duration(elapsed)
	if left := Remaining(elapsed, (float64(s.finished)+min(max(progress, 0), 1))/float64(s.total)); left > 0 {
		timing += ", about " + Duration(left) + " left"
	}
	th := s.p.Theme()
	parts := []string{Marker(th) + " " + s.label()}
	if detail != "" {
		parts = append(parts, th.Muted(detail))
	}
	parts = append(parts, th.Muted(timing))
	return s.line.Set(strings.Join(parts, "  "))
}

// Marker is the glyph that starts a status line, the way ✓ starts a finished
// step's line.
func Marker(th pretty.Theme) string {
	return th.Accent("›")
}

// Context changes the current step's context, e.g. to the package it's on.
func (s *Steps) Context(context string) {
	s.context = context
}

// Done finishes the step, leaving a line with detail.
func (s *Steps) Done(detail string) humane.Error {
	err := s.finish(s.p.Theme().Ok("✓"), detail)
	s.finished++
	return err
}

// Failed finishes the step as a failure, leaving a line with detail.
func (s *Steps) Failed(detail string) humane.Error {
	return s.finish(s.p.Theme().Fail("✗"), detail)
}

// Finished returns how many steps are done.
func (s *Steps) Finished() int {
	return s.finished
}

// Elapsed returns the time since the steps were created.
func (s *Steps) Elapsed() time.Duration {
	return time.Since(s.start)
}

// Clear erases the status line, e.g. before an error is printed.
func (s *Steps) Clear() humane.Error {
	return s.line.Clear()
}

func (s *Steps) finish(glyph, detail string) humane.Error {
	if err := s.line.Clear(); err != nil {
		return err
	}
	out := glyph + " " + s.label()
	if detail != "" {
		out += "  " + s.p.Theme().Muted(detail)
	}
	return s.p.Print(strings.TrimRight(out, " ") + "\n")
}

// label renders the step's counter, subject and context in aligned
// columns.
func (s *Steps) label() string {
	th := s.p.Theme()
	count := strconv.Itoa(s.total)
	counter := fmt.Sprintf("[%*d/%s]", len(count), s.finished+1, count)
	out := th.Muted(counter) + " " + th.Bold(pad(s.subject, s.subjectWidth))
	if s.contextWidth > 0 {
		out += "  " + th.Muted(pad(s.context, s.contextWidth))
	}
	return out
}

func (s *Steps) live() bool {
	return s.line.Live() && !s.stream
}

// pad pads s with spaces to width columns.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}
