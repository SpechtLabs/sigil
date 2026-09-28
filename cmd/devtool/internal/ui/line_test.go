package ui

import (
	"bytes"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

func TestLine(t *testing.T) {
	tests := []struct {
		name  string
		tty   bool
		width int
		set   []string
		clear bool
		want  string
	}{
		{name: "silent off a terminal", set: []string{"building", "sampling"}, clear: true, want: ""},
		{name: "redrawn in place", tty: true, width: 80, set: []string{"one", "two"}, want: "\r\x1b[Kone\r\x1b[Ktwo"},
		{name: "cut to the width", tty: true, width: 6, set: []string{"abcdefgh"}, want: "\r\x1b[Kabcd…"},
		{name: "cleared once shown", tty: true, width: 80, set: []string{"one"}, clear: true, want: "\r\x1b[Kone\r\x1b[K"},
		{name: "nothing to clear", tty: true, width: 80, clear: true, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			env := []string{"TERM=xterm-256color", "NO_COLOR=1"}
			if tt.tty {
				env = append(env, "CLICOLOR_FORCE=1")
			}
			l := NewLine(pretty.New(&out, pretty.WithEnviron(env), pretty.WithDarkBackground(true)), tt.width)
			for _, s := range tt.set {
				if err := l.Set(s); err != nil {
					t.Fatal(err)
				}
			}
			if tt.clear {
				if err := l.Clear(); err != nil {
					t.Fatal(err)
				}
			}
			if l.Live() != tt.tty {
				t.Errorf("Live() = %v, want %v", l.Live(), tt.tty)
			}
			if out.String() != tt.want {
				t.Errorf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestDefaultWidth(t *testing.T) {
	if l := NewLine(pretty.New(&bytes.Buffer{}), 0); l.width <= 0 {
		t.Errorf("width = %d, want a positive default", l.width)
	}
}
