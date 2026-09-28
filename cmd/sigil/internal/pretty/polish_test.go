package pretty

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

// at is a position on the first line.
func at(offset, col int) token.Pos {
	return token.Pos{Offset: offset, Line: 1, Column: col}
}

var sample = diag.ErrorList{
	{File: "p.sigil", Msg: "unknown field", Help: "did you mean tier?", Pos: at(5, 6), End: at(9, 10)},
	{File: "q.sigil", Msg: "unused", Code: "unused-let", Severity: diag.SeverityWarning, Pos: at(0, 1), End: at(3, 4)},
}

func sources(file string) []byte {
	return map[string][]byte{"p.sigil": []byte("when teir == 1\n"), "q.sigil": []byte("let x = 1\n")}[file]
}

const samplePlain = "p.sigil:1:6: error: unknown field\n" +
	"  |\n" +
	"1 | when teir == 1\n" +
	"  |      ^^^^\n" +
	"  = help: did you mean tier?\n" +
	"\n" +
	"q.sigil:1:1: warning: unused [unused-let]\n" +
	"  |\n" +
	"1 | let x = 1\n" +
	"  | ^^^\n"

func TestDiagnosticsPlain(t *testing.T) {
	var buf bytes.Buffer
	if err := plain(&buf).Diagnostics(sample, sources); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), samplePlain+"\n"; got != want {
		t.Errorf("Diagnostics() =\n%s\nwant\n%s", got, want)
	}
	buf.Reset()
	if err := plain(&buf).Diagnostics(nil, sources); err != nil || buf.Len() != 0 {
		t.Errorf("Diagnostics(nil) = %v, wrote %q", err, buf.String())
	}
}

func TestDiagnosticsOnTerminal(t *testing.T) {
	var buf bytes.Buffer
	if err := forced(&buf).Diagnostics(sample, sources); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("output %q has no styling", out)
	}
	if got := stripANSI(out); got != samplePlain+"\n" {
		t.Errorf("stripped output =\n%s\nwant the plain form\n%s", got, samplePlain)
	}
	for _, want := range []string{"error", "warning", "^^^^", "= help:", "[unused-let]", "p.sigil:1:6"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q", want)
		}
	}
}

func TestErrWithDiagnostics(t *testing.T) {
	err := Diagnose(sample, sources, "the bundle doesn't check", "fix the errors above")
	var buf bytes.Buffer
	if werr := plain(&buf).Err(err); werr != nil {
		t.Fatal(werr)
	}
	want := samplePlain + "\nError: the bundle doesn't check\n\nWhat you can do\n  • fix the errors above\n"
	if got := buf.String(); got != want {
		t.Errorf("Err() =\n%s\nwant\n%s", got, want)
	}
	if got := err.Error(); got != samplePlain[:len(samplePlain)-1]+"\n\nthe bundle doesn't check" {
		t.Errorf("Error() = %q", got)
	}
	if got := err.Display(); !strings.HasSuffix(got, "the bundle doesn't check\n  - fix the errors above") {
		t.Errorf("Display() = %q", got)
	}
	if err.Message() != "the bundle doesn't check" || err.Cause() != nil || len(err.Advice()) != 1 {
		t.Errorf("Message() = %q, Cause() = %v, Advice() = %v", err.Message(), err.Cause(), err.Advice())
	}
	empty := Diagnose(nil, nil, "nothing", "x")
	if got := empty.Error(); got != "nothing" {
		t.Errorf("Error() without diagnostics = %q", got)
	}
	buf.Reset()
	wrapped := fmt.Errorf("wrapped: %w", err)
	if werr := forced(&buf).Err(wrapped); werr != nil {
		t.Fatal(werr)
	}
	if out := buf.String(); !strings.Contains(out, "ERROR") || !strings.Contains(out, "the bundle doesn't check") || !strings.Contains(out, "^^^^") {
		t.Errorf("Err(wrapped) on a terminal = %q", out)
	}
}

func TestErrWithFailed(t *testing.T) {
	f := Fail("2 of 5 cases failed", "look above")
	if f.Error() != "2 of 5 cases failed" || f.Display() != "2 of 5 cases failed\n  - look above" || f.Cause() != nil || len(f.Advice()) != 1 {
		t.Errorf("Fail() = %q / %q / %v / %v", f.Error(), f.Display(), f.Cause(), f.Advice())
	}
	if !Reported(f) || !Reported(fmt.Errorf("wrapped: %w", f)) || Reported(errors.New("plain")) || Reported(nil) {
		t.Error("Reported() doesn't recognize Failed errors")
	}
	var buf bytes.Buffer
	if err := plain(&buf).Err(f); err != nil || buf.Len() != 0 {
		t.Errorf("Err(Failed) = %v, wrote %q, want nothing", err, buf.String())
	}
}

func TestErrDedupesCauses(t *testing.T) {
	inner := errors.New("no such file or directory")
	path := fmt.Errorf("open k.sigil: %w", inner)
	err := humane.Wrap(path, "the kind file couldn't be read", "pass it with --kind")
	var buf bytes.Buffer
	_ = plain(&buf).Err(err)
	want := "Error: the kind file couldn't be read\n\nWhat you can do\n  • pass it with --kind\n\nCaused by\n  • open k.sigil: no such file or directory\n"
	if got := buf.String(); got != want {
		t.Errorf("Err() =\n%s\nwant\n%s", got, want)
	}
}

func TestPrint(t *testing.T) {
	var buf bytes.Buffer
	if err := plain(&buf).Print("raw text\n"); err != nil || buf.String() != "raw text\n" {
		t.Errorf("Print() = %v, wrote %q", err, buf.String())
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

func TestWriteError(t *testing.T) {
	p := New(failingWriter{}, WithEnviron(nil))
	err := p.Ok("x")
	if err == nil || err.Error() != "the output couldn't be written" {
		t.Fatalf("Ok() on a failing writer = %v", err)
	}
	if !strings.Contains(err.Cause().Error(), "closed pipe") {
		t.Errorf("cause = %v", err.Cause())
	}
	if err := p.Err(Diagnose(sample, sources, "m")); err == nil {
		t.Error("Err(Diagnostics) on a failing writer = nil")
	}
}

func TestThemePlain(t *testing.T) {
	th := plain(&bytes.Buffer{}).Theme()
	if th.TTY() {
		t.Fatal("TTY() = true for a buffer")
	}
	for name, f := range map[string]func(string) string{
		"Bold": th.Bold, "Muted": th.Muted, "Ok": th.Ok, "Info": th.Info, "Warn": th.Warn,
		"Fail": th.Fail, "Accent": th.Accent, "Key": th.Key, "Location": th.Location, "Help": th.Help,
	} {
		if got := f("text"); got != "text" {
			t.Errorf("%s(text) = %q, want unchanged", name, got)
		}
	}
	if th.Diagnostics() != diag.Plain {
		t.Error("Diagnostics() isn't the plain theme")
	}
}

func TestThemeOnTerminal(t *testing.T) {
	th := forced(&bytes.Buffer{}).Theme()
	if !th.TTY() {
		t.Fatal("TTY() = false for a forced terminal")
	}
	for name, f := range map[string]func(string) string{
		"Bold": th.Bold, "Muted": th.Muted, "Ok": th.Ok, "Info": th.Info, "Warn": th.Warn,
		"Fail": th.Fail, "Accent": th.Accent, "Key": th.Key, "Location": th.Location, "Help": th.Help,
	} {
		got := f("text")
		if !strings.Contains(got, "\x1b[") || stripANSI(got) != "text" {
			t.Errorf("%s(text) = %q, want it styled around the same text", name, got)
		}
	}
	d := th.Diagnostics()
	if d == diag.Plain {
		t.Fatal("Diagnostics() is the plain theme")
	}
	if got := d.Severity(diag.SeverityWarning); stripANSI(got) != "warning" || got == "warning" {
		t.Errorf("Severity(warning) = %q", got)
	}
	if got := d.Caret("^^", diag.SeverityError); stripANSI(got) != "^^" || got == "^^" {
		t.Errorf("Caret() = %q", got)
	}
}

func TestColorScheme(t *testing.T) {
	for _, dark := range []bool{true, false} {
		cs := ColorScheme(lipgloss.LightDark(dark))
		if cs.Base == nil || cs.Title == nil || cs.ErrorHeader[0] == nil || cs.ErrorHeader[1] == nil {
			t.Errorf("ColorScheme(dark=%v) has nil colors", dark)
		}
	}
}

// stripANSI removes SGR escape sequences.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
