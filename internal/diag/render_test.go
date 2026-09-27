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
			want: "p.sigil:2:22: `---` can't appear inside an expression\n" +
				"  |\n" +
				"2 | \tlet tight = min_soak---1h\n" +
				"  | \t                    ^^^\n" +
				"  = help: put spaces between the minus signs\n",
		},
		{
			name: "no help",
			err:  &diag.Error{File: "p.sigil", Msg: "expected `:`", Pos: at(9, 1, 10), End: at(10, 1, 11)},
			want: "p.sigil:1:10: expected `:`\n" +
				"  |\n" +
				"1 | policy p: K\n" +
				"  |          ^\n",
		},
		{
			name: "span over several lines underlines to the end of the first",
			err:  &diag.Error{File: "p.sigil", Msg: "unterminated raw string literal", Help: "close it with a backtick", Pos: at(47, 3, 9), End: at(56, 4, 4)},
			want: "p.sigil:3:9: unterminated raw string literal\n" +
				"  |\n" +
				"3 | let s = `abc\n" +
				"  |         ^^^^\n" +
				"  = help: close it with a backtick\n",
		},
		{
			name: "zero-width span at end of file",
			err:  &diag.Error{File: "p.sigil", Msg: "expected `}`, found end of file", Pos: at(57, 5, 1), End: at(57, 5, 1)},
			want: "p.sigil:5:1: expected `}`, found end of file\n" +
				"  |\n" +
				"5 | \n" +
				"  | ^\n",
		},
		{
			name: "line past the end of the source",
			err:  &diag.Error{File: "p.sigil", Msg: "somewhere else", Help: "look again", Pos: at(999, 42, 1), End: at(999, 42, 1)},
			want: "p.sigil:42:1: somewhere else\n  = help: look again\n",
		},
		{
			name: "invalid position",
			err:  &diag.Error{File: "p.sigil", Msg: "nowhere"},
			want: "p.sigil: nowhere\n",
		},
		{
			name: "two-digit line numbers widen the gutter",
			err:  &diag.Error{File: "p.sigil", Msg: "deep", Pos: at(0, 12, 3), End: at(0, 12, 5)},
			want: "p.sigil:12:3: deep\n" +
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
		want := "p.sigil:2:1: x\n  |\n2 | def\n  | ^\n"
		if got != want {
			t.Errorf("Render() =\n%q\nwant\n%q", got, want)
		}
	})

	t.Run("columns are characters", func(t *testing.T) {
		e := &diag.Error{File: "p.sigil", Msg: "x", Pos: at(7, 1, 6), End: at(8, 1, 7)}
		got := diag.Render(e, []byte(`"é" + y`))
		want := "p.sigil:1:6: x\n  |\n1 | \"é\" + y\n  |      ^\n"
		if got != want {
			t.Errorf("Render() =\n%q\nwant\n%q", got, want)
		}
	})
}
