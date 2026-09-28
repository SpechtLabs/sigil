package diag_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
)

func TestRender(t *testing.T) {
	src := []byte("policy p: K\n\tlet tight = min_soak---1h\nlet s = `abc\ndef\n")

	tests := []struct {
		name string
		err  *diag.Error
		want string
	}{
		{
			name: "span on one line",
			err:  &diag.Error{File: "p.sigil", Msg: "`---` can't appear inside an expression", Help: "put spaces between the minus signs", Pos: at(33, 2, 22), End: at(36, 2, 25)},
			want: "p.sigil:2:22: error: `---` can't appear inside an expression\n" +
				"  |\n" +
				"2 | \tlet tight = min_soak---1h\n" +
				"  | \t                    ^^^\n" +
				"  = help: put spaces between the minus signs\n",
		},
		{
			name: "no help",
			err:  &diag.Error{File: "p.sigil", Msg: "expected `:`", Pos: at(9, 1, 10), End: at(10, 1, 11)},
			want: "p.sigil:1:10: error: expected `:`\n" +
				"  |\n" +
				"1 | policy p: K\n" +
				"  |          ^\n",
		},
		{
			name: "span over several lines underlines to the end of the first",
			err:  &diag.Error{File: "p.sigil", Msg: "unterminated raw string literal", Help: "close it with a backtick", Pos: at(47, 3, 9), End: at(56, 4, 4)},
			want: "p.sigil:3:9: error: unterminated raw string literal\n" +
				"  |\n" +
				"3 | let s = `abc\n" +
				"  |         ^^^^\n" +
				"  = help: close it with a backtick\n",
		},
		{
			name: "zero-width span at end of file",
			err:  &diag.Error{File: "p.sigil", Msg: "expected `}`, found end of file", Pos: at(57, 5, 1), End: at(57, 5, 1)},
			want: "p.sigil:5:1: error: expected `}`, found end of file\n" +
				"  |\n" +
				"5 | \n" +
				"  | ^\n",
		},
		{
			name: "line past the end of the source",
			err:  &diag.Error{File: "p.sigil", Msg: "somewhere else", Help: "look again", Pos: at(999, 42, 1), End: at(999, 42, 1)},
			want: "p.sigil:42:1: error: somewhere else\n  = help: look again\n",
		},
		{
			name: "invalid position",
			err:  &diag.Error{File: "p.sigil", Msg: "nowhere"},
			want: "p.sigil: error: nowhere\n",
		},
		{
			name: "two-digit line numbers widen the gutter",
			err:  &diag.Error{File: "p.sigil", Msg: "deep", Pos: at(0, 12, 3), End: at(0, 12, 5)},
			want: "p.sigil:12:3: error: deep\n" +
				"   |\n" +
				"12 | line twelve\n" +
				"   |   ^^\n",
		},
	}

	long := []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\nline twelve\n")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := src
			if tt.err.Pos.Line == 12 {
				s = long
			}
			if got := diag.Render(tt.err, s); got != tt.want {
				t.Errorf("Render() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}

	t.Run("crlf line endings", func(t *testing.T) {
		e := &diag.Error{File: "p.sigil", Msg: "x", Pos: at(5, 2, 1), End: at(6, 2, 2)}
		got := diag.Render(e, []byte("abc\r\ndef\r\n"))
		want := "p.sigil:2:1: error: x\n  |\n2 | def\n  | ^\n"
		if got != want {
			t.Errorf("Render() =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("columns are characters", func(t *testing.T) {
		e := &diag.Error{File: "p.sigil", Msg: "x", Pos: at(7, 1, 6), End: at(8, 1, 7)}
		got := diag.Render(e, []byte(`"é" + y`))
		want := "p.sigil:1:6: error: x\n  |\n1 | \"é\" + y\n  |      ^\n"
		if got != want {
			t.Errorf("Render() =\n%q\nwant\n%q", got, want)
		}
	})
}

func TestRenderWarning(t *testing.T) {
	e := &diag.Error{File: "p.sigil", Msg: "let x is never read", Help: "remove it", Code: "unused-let", Severity: diag.SeverityWarning, Pos: at(4, 1, 5), End: at(5, 1, 6)}
	got := diag.Render(e, []byte("let x = 1\n"))
	want := "p.sigil:1:5: warning: let x is never read [unused-let]\n" +
		"  |\n" +
		"1 | let x = 1\n" +
		"  |     ^\n" +
		"  = help: remove it\n"
	if got != want {
		t.Errorf("Render() =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderDocument(t *testing.T) {
	e := &diag.Error{File: "policies.sigil", Doc: "payments.production", Msg: "x", Pos: at(9, 2, 1), End: at(10, 2, 2)}
	if got, want := diag.Render(e, nil), "policies.sigil:2:1 (payments.production): error: x\n"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
	e.File = "payments/production.sigil"
	if got, want := diag.Render(e, nil), "payments/production.sigil:2:1: error: x\n"; got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestRenderWithTheme(t *testing.T) {
	wrap := func(tag string) func(string) string {
		return func(s string) string { return "<" + tag + ">" + s + "</" + tag + ">" }
	}
	theme := &diag.Theme{
		Location: wrap("loc"),
		Doc:      wrap("doc"),
		Severity: func(s diag.Severity) string { return "<sev>" + s.String() + "</sev>" },
		Message:  wrap("msg"),
		Code:     wrap("code"),
		Gutter:   wrap("gut"),
		Source:   wrap("src"),
		Caret:    func(s string, _ diag.Severity) string { return "<caret>" + s + "</caret>" },
		Help:     wrap("help"),
		HelpText: wrap("text"),
	}
	e := &diag.Error{File: "p.sigil", Doc: "q", Msg: "m", Help: "h", Code: "c", Pos: at(2, 1, 3), End: at(3, 1, 4)}
	got := diag.RenderWith(e, []byte("abcd\n"), theme)
	want := "<loc>p.sigil:1:3</loc><doc> (q)</doc>: <sev>error</sev>: <msg>m</msg><code> [c]</code>\n" +
		"<gut>  |</gut>\n" +
		"<gut>1 |</gut> <src>abcd</src>\n" +
		"<gut>  |</gut>   <caret>^</caret>\n" +
		"  <help>= help:</help> <text>h</text>\n"
	if got != want {
		t.Errorf("RenderWith() =\n%s\nwant\n%s", got, want)
	}
	if got := diag.RenderWith(e, nil, nil); got != diag.Render(e, nil) {
		t.Errorf("RenderWith(nil theme) = %q, want the plain form %q", got, diag.Render(e, nil))
	}
	if got := diag.RenderWith(nil, nil, theme); got != "" {
		t.Errorf("RenderWith(nil) = %q, want empty", got)
	}
}

func TestRenderAll(t *testing.T) {
	files := map[string][]byte{"a.sigil": []byte("one\n"), "b.sigil": []byte("two\n")}
	src := func(file string) []byte { return files[file] }
	errs := diag.ErrorList{
		{File: "a.sigil", Msg: "first", Pos: at(0, 1, 1), End: at(3, 1, 4)},
		{File: "b.sigil", Msg: "second", Help: "fix it", Pos: at(0, 1, 1), End: at(1, 1, 2)},
		{File: "c.sigil", Msg: "no source"},
	}
	got := diag.RenderAll(errs, src, nil)
	want := "a.sigil:1:1: error: first\n  |\n1 | one\n  | ^^^\n\n" +
		"b.sigil:1:1: error: second\n  |\n1 | two\n  | ^\n  = help: fix it\n\n" +
		"c.sigil: error: no source"
	if got != want {
		t.Errorf("RenderAll() =\n%s\nwant\n%s", got, want)
	}
	if got := diag.RenderAll(nil, nil, nil); got != "" {
		t.Errorf("RenderAll(nil) = %q, want empty", got)
	}
	if got := diag.RenderAll(errs[2:], nil, nil); got != "c.sigil: error: no source" {
		t.Errorf("RenderAll(no sources) = %q", got)
	}
}
