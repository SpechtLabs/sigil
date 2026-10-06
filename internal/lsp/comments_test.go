package lsp

import (
	"strings"
	"testing"
)

// TestComment checks which lines make a declaration's doc comment: the
// `//` lines right above it, and nothing past a line that isn't one.
func TestComment(t *testing.T) {
	tests := []struct {
		name string
		src  string // the declaration starts at the marker
		want string
	}{
		{name: "one line", src: "// Hello.\n<|>input x: int", want: "Hello."},
		{name: "several lines", src: "// One\n// two.\n<|>input x: int", want: "One\ntwo."},
		{name: "a paragraph", src: "// One.\n//\n// Two.\n<|>input x: int", want: "One.\n\nTwo."},
		{name: "indented", src: "type T {\n  // A field.\n  <|>a: int\n}", want: "A field."},
		{name: "on the first line", src: "<|>input x: int"},
		{name: "a blank line between", src: "// Not this.\n\n<|>input x: int"},
		{name: "code above", src: "input y: int\n<|>input x: int"},
		{name: "a comment above the code above", src: "// y\ninput y: int\n<|>input x: int"},
		{name: "the declaration mid-line", src: "// Hello.\ninput <|>x: int", want: "Hello."},
		{name: "a CRLF", src: "// Hello.\r\n<|>input x: int", want: "Hello."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := strings.Index(tt.src, marker)
			src := strings.Replace(tt.src, marker, "", 1)
			if got := comment([]byte(src), offset); got != tt.want {
				t.Errorf("comment() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := comment([]byte("x"), 5); got != "" {
		t.Errorf("comment() past the end = %q", got)
	}
}

// TestCommentsWithoutSource checks that a name without a source to read
// has no doc comment: in a view without a project, and a let of a
// document the project doesn't hold.
func TestCommentsWithoutSource(t *testing.T) {
	v := newView(nil, "p.sigil", []byte("policy p: K@1\n"))
	if got := v.kindComment("K", nil); got != "" {
		t.Errorf("kindComment() = %q without a project", got)
	}
	if got := v.letComment("nope", "x"); got != "" {
		t.Errorf("letComment() = %q without a project", got)
	}
	w, _ := viewOf(t, "production.sigil", head)
	if got := w.letComment("deploy.common", "nope"); got != "" {
		t.Errorf("letComment() = %q for a let the module doesn't declare", got)
	}
}
