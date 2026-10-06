package lsp

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// TestLines maps offsets to positions and back, in bytes and in UTF-16
// code units, with characters of one, two, three and four bytes.
func TestLines(t *testing.T) {
	src := "ab\né😀x\r\nz"
	tests := []struct {
		name     string
		encoding string
		offset   int
		pos      protocol.Position
		back     int // the offset pos maps back to, when it isn't offset
	}{
		{name: "start", encoding: protocol.EncodingUTF16, offset: 0, pos: protocol.Position{}},
		{name: "end of the first line", encoding: protocol.EncodingUTF16, offset: 2, pos: protocol.Position{Character: 2}},
		{name: "start of the second line", encoding: protocol.EncodingUTF16, offset: 3, pos: protocol.Position{Line: 1}},
		{name: "after a two-byte character", encoding: protocol.EncodingUTF16, offset: 5, pos: protocol.Position{Line: 1, Character: 1}},
		{name: "after an emoji", encoding: protocol.EncodingUTF16, offset: 9, pos: protocol.Position{Line: 1, Character: 3}},
		{name: "before the carriage return", encoding: protocol.EncodingUTF16, offset: 10, pos: protocol.Position{Line: 1, Character: 4}},
		{name: "the last line", encoding: protocol.EncodingUTF16, offset: 13, pos: protocol.Position{Line: 2, Character: 1}},
		{name: "past the end", encoding: protocol.EncodingUTF16, offset: 99, pos: protocol.Position{Line: 2, Character: 1}, back: 13},
		{name: "before the start", encoding: protocol.EncodingUTF16, offset: -1, pos: protocol.Position{}, back: 0},
		{name: "bytes after an emoji", encoding: protocol.EncodingUTF8, offset: 9, pos: protocol.Position{Line: 1, Character: 6}},
		{name: "bytes on the last line", encoding: protocol.EncodingUTF8, offset: 13, pos: protocol.Position{Line: 2, Character: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newLines([]byte(src), tt.encoding)
			if got := l.position(tt.offset); got != tt.pos {
				t.Errorf("position(%d) = %+v, want %+v", tt.offset, got, tt.pos)
			}
			back := tt.offset
			if tt.back != 0 || tt.offset < 0 {
				back = tt.back
			}
			if got := l.offset(tt.pos); got != back {
				t.Errorf("offset(%+v) = %d, want %d", tt.pos, got, back)
			}
		})
	}
}

// TestLinesClamp maps positions outside the text: past a line's end, past
// the last line, and inside an emoji's surrogate pair.
func TestLinesClamp(t *testing.T) {
	src := "ab\n😀x\nz"
	tests := []struct {
		name     string
		encoding string
		pos      protocol.Position
		want     int
	}{
		{name: "past a line's end", encoding: protocol.EncodingUTF16, pos: protocol.Position{Line: 0, Character: 40}, want: 2},
		{name: "past the last line", encoding: protocol.EncodingUTF16, pos: protocol.Position{Line: 9}, want: len(src)},
		{name: "inside a surrogate pair", encoding: protocol.EncodingUTF16, pos: protocol.Position{Line: 1, Character: 1}, want: 3},
		{name: "past the last line's end", encoding: protocol.EncodingUTF16, pos: protocol.Position{Line: 2, Character: 9}, want: len(src)},
		{name: "bytes past a line's end", encoding: protocol.EncodingUTF8, pos: protocol.Position{Line: 1, Character: 99}, want: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newLines([]byte(src), tt.encoding).offset(tt.pos); got != tt.want {
				t.Errorf("offset(%+v) = %d, want %d", tt.pos, got, tt.want)
			}
		})
	}
}

// TestApply applies document changes: a whole text, a range, a range
// given end first, and a range counted in UTF-16 past an emoji.
func TestApply(t *testing.T) {
	rng := func(l1, c1, l2, c2 uint32) *protocol.Range {
		return &protocol.Range{Start: protocol.Position{Line: l1, Character: c1}, End: protocol.Position{Line: l2, Character: c2}}
	}
	tests := []struct {
		name     string
		text     string
		change   protocol.TextDocumentContentChangeEvent
		encoding string
		want     string
	}{
		{name: "the whole text", text: "old", change: protocol.TextDocumentContentChangeEvent{Text: "new"}, want: "new"},
		{name: "an insertion", text: "ab\ncd", change: protocol.TextDocumentContentChangeEvent{Range: rng(1, 1, 1, 1), Text: "X"}, want: "ab\ncXd"},
		{name: "a replacement across lines", text: "ab\ncd", change: protocol.TextDocumentContentChangeEvent{Range: rng(0, 1, 1, 1), Text: "-"}, want: "a-d"},
		{name: "end first", text: "abcd", change: protocol.TextDocumentContentChangeEvent{Range: rng(0, 3, 0, 1), Text: ""}, want: "ad"},
		{name: "after an emoji", text: "😀ab", change: protocol.TextDocumentContentChangeEvent{Range: rng(0, 2, 0, 3), Text: "x"}, encoding: protocol.EncodingUTF16, want: "😀xb"},
		{name: "after an emoji, in bytes", text: "😀ab", change: protocol.TextDocumentContentChangeEvent{Range: rng(0, 4, 0, 5), Text: "x"}, encoding: protocol.EncodingUTF8, want: "😀xb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoding := tt.encoding
			if encoding == "" {
				encoding = protocol.EncodingUTF16
			}
			if got := string(apply([]byte(tt.text), tt.change, encoding)); got != tt.want {
				t.Errorf("apply() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestURIs maps file URIs to paths and back.
func TestURIs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the paths below are POSIX paths")
	}
	tests := []struct {
		uri  string
		path string
		ok   bool
	}{
		{uri: "file:///ws/a.sigil", path: "/ws/a.sigil", ok: true},
		{uri: "file:///ws/my%20policies/a.sigil", path: "/ws/my policies/a.sigil", ok: true},
		{uri: "file:///ws/./b/../a.sigil", path: "/ws/a.sigil", ok: true},
		{uri: "untitled:Untitled-1"},
		{uri: "https://example.com/a.sigil"},
		{uri: "file://%zz"},
	}
	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			path, ok := pathOf(tt.uri)
			if ok != tt.ok || path != filepath.FromSlash(tt.path) {
				t.Errorf("pathOf() = %q, %v, want %q, %v", path, ok, tt.path, tt.ok)
			}
			if !ok {
				return
			}
			if back, _ := pathOf(uriOf(path)); back != path {
				t.Errorf("uriOf(%q) = %q, which maps back to %q", path, uriOf(path), back)
			}
		})
	}
}
