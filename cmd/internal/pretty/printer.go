package pretty

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
)

// Printer writes styled output for humans. Create one per command run from
// cmd.OutOrStdout() or cmd.ErrOrStderr().
type Printer struct {
	w      *colorprofile.Writer
	tty    bool
	styles styles
}

// New returns a Printer that writes to w. Colors are downsampled to what w
// supports; when w isn't a terminal, output is plain text with no borders.
// A w that is already a colorprofile writer, as fang's are, is used as it
// is. On a terminal, the light or dark palette follows the terminal's
// background color, asked for once per process, unless
// [WithDarkBackground] picks one.
func New(w io.Writer, opts ...Option) *Printer {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	// fang already wraps its writers; wrapping again would detect a non-TTY.
	cw, ok := w.(*colorprofile.Writer)
	if !ok {
		cw = colorprofile.NewWriter(w, o.environ)
	}
	tty := cw.Profile > colorprofile.NoTTY
	dark := true
	if tty {
		dark = o.darkBackground()
	}
	return &Printer{
		w:      cw,
		tty:    tty,
		styles: newStyles(newPalette(lipgloss.LightDark(dark))),
	}
}

// KV is one row of a [Printer.KeyValues] block.
type KV struct {
	Key   string
	Value string
}

// Print writes s as it is. Reports build their text with the printer's
// [Printer.Theme] and hand it here.
func (p *Printer) Print(s string) humane.Error {
	return p.write(s)
}

// Ok prints a success line, followed by indented details.
func (p *Printer) Ok(msg string, details ...string) humane.Error {
	return p.status(p.styles.ok, "✓", msg, details)
}

// Note prints an informational line, followed by indented details.
func (p *Printer) Note(msg string, details ...string) humane.Error {
	return p.status(p.styles.info, "ℹ", msg, details)
}

// Warning prints a warning line, followed by indented details.
func (p *Printer) Warning(msg string, details ...string) humane.Error {
	return p.status(p.styles.warn, "!", msg, details)
}

// Fail prints a failure line, followed by indented details. Use
// [Printer.Err] to render an error value.
func (p *Printer) Fail(msg string, details ...string) humane.Error {
	return p.status(p.styles.fail, "✗", msg, details)
}

func (p *Printer) status(icon lipgloss.Style, glyph, msg string, details []string) humane.Error {
	var b strings.Builder
	b.WriteString(icon.Render(glyph) + " " + p.styles.bold.Render(msg) + "\n")
	for _, d := range details {
		b.WriteString("  " + p.styles.muted.Render(d) + "\n")
	}
	return p.write(b.String())
}

// Diagnostics prints diagnostics in the documented layout, each quoting
// the line src finds for its file, with a blank line between them and
// after the last.
func (p *Printer) Diagnostics(errs diag.ErrorList, src diag.Sources) humane.Error {
	if len(errs) == 0 {
		return nil
	}
	return p.write(diag.RenderAll(errs, src, p.Theme().Diagnostics()) + "\n\n")
}

// KeyValues prints aligned key/value rows. On a terminal they sit in a
// rounded box under title; otherwise they are plain `key: value` lines, so
// the output stays easy to grep.
func (p *Printer) KeyValues(title string, rows ...KV) humane.Error {
	width := 0
	for _, r := range rows {
		width = max(width, lipgloss.Width(r.Key)+1)
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		key := fmt.Sprintf("%-*s", width, r.Key+":")
		lines = append(lines, p.styles.key.Render(key)+" "+p.styles.text.Render(r.Value))
	}
	body := strings.Join(lines, "\n")
	if !p.tty {
		return p.write(body + "\n")
	}
	if title != "" {
		body = p.styles.title.Render(title) + "\n\n" + body
	}
	return p.write(p.styles.box.Render(body) + "\n")
}

func (p *Printer) write(s string) humane.Error {
	if _, err := io.WriteString(p.w, s); err != nil {
		return humane.Wrap(err, "the output couldn't be written", "check where the output is going, e.g. that the pipe isn't closed")
	}
	return nil
}
