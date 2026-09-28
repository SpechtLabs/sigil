package pretty

import (
	"charm.land/lipgloss/v2"

	"github.com/spechtlabs/sigil/internal/diag"
)

// Theme styles the pieces of a report, such as an evaluation's trace or
// a test run, for the Printer's terminal. Every method returns its
// argument styled, or unchanged when the output isn't a terminal, so a
// report has one layout whether it's read on screen or piped to a file.
type Theme struct {
	s   styles
	tty bool
}

// Theme returns the printer's theme, for reports it prints with Print.
func (p *Printer) Theme() Theme {
	return Theme{s: p.styles, tty: p.tty}
}

// TTY reports whether the output is a terminal.
func (t Theme) TTY() bool { return t.tty }

// Bold emphasizes s.
func (t Theme) Bold(s string) string { return t.style(t.s.bold, s) }

// Muted de-emphasizes s: positions, separators and other detail.
func (t Theme) Muted(s string) string { return t.style(t.s.muted, s) }

// Ok marks s as a success.
func (t Theme) Ok(s string) string { return t.style(t.s.ok, s) }

// Info marks s as informational.
func (t Theme) Info(s string) string { return t.style(t.s.info, s) }

// Warn marks s as a warning.
func (t Theme) Warn(s string) string { return t.style(t.s.warn, s) }

// Fail marks s as a failure.
func (t Theme) Fail(s string) string { return t.style(t.s.fail, s) }

// Accent highlights s: a heading, or the name of what a report is about.
func (t Theme) Accent(s string) string { return t.style(t.s.accent, s) }

// Key styles s as the name of a field or key.
func (t Theme) Key(s string) string { return t.style(t.s.key, s) }

// Location styles a file position in a report: quietly, so the decision
// next to it stands out. A diagnostic's header is styled by Diagnostics.
func (t Theme) Location(s string) string { return t.style(t.s.muted, s) }

// Help styles the label of a hint.
func (t Theme) Help(s string) string { return t.style(t.s.help, s) }

// Diagnostics returns the theme a diagnostic renders with.
func (t Theme) Diagnostics() *diag.Theme {
	if !t.tty {
		return diag.Plain
	}
	severity := func(sev diag.Severity) lipgloss.Style {
		if sev == diag.SeverityWarning {
			return t.s.warn
		}
		return t.s.fail
	}
	return &diag.Theme{
		Location: one(t.s.location),
		Doc:      one(t.s.muted),
		Severity: func(sev diag.Severity) string { return severity(sev).Render(sev.String()) },
		Message:  one(t.s.bold),
		Code:     one(t.s.muted),
		Gutter:   one(t.s.gutter),
		Source:   one(t.s.text),
		Caret:    func(s string, sev diag.Severity) string { return severity(sev).Render(s) },
		Help:     one(t.s.help),
		HelpText: one(t.s.text),
	}
}

// one adapts a style's variadic Render to the theme's one-string form.
func one(st lipgloss.Style) func(string) string {
	return func(s string) string { return st.Render(s) }
}

func (t Theme) style(st lipgloss.Style, s string) string {
	if !t.tty {
		return s
	}
	return st.Render(s)
}
