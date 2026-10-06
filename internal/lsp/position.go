package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// lines maps between byte offsets into a source and LSP positions, whose
// character counts bytes or UTF-16 code units, as the client and the
// server agreed. Lines end at `\n`; a `\r` before it is part of the line.
type lines struct {
	src    []byte
	starts []int // the offset each line starts at
	utf8   bool  // characters are bytes; otherwise UTF-16 code units
}

// newLines indexes src's lines for the encoding the session agreed on.
func newLines(src []byte, encoding string) *lines {
	l := &lines{src: src, starts: []int{0}, utf8: encoding == protocol.EncodingUTF8}
	for i, c := range src {
		if c == '\n' {
			l.starts = append(l.starts, i+1)
		}
	}
	return l
}

// position returns the position of offset, which is clamped to the
// source.
func (l *lines) position(offset int) protocol.Position {
	offset = max(0, min(offset, len(l.src)))
	line := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > offset }) - 1
	start := l.starts[line]
	if l.utf8 {
		return protocol.Position{Line: uint32(line), Character: uint32(offset - start)} //nolint:gosec // a source fits in 64 MiB, the transport's limit
	}
	units := 0
	for _, r := range string(l.src[start:offset]) {
		units += utf16Len(r)
	}
	return protocol.Position{Line: uint32(line), Character: uint32(units)} //nolint:gosec // as above
}

// rangeOf returns the range from one offset to another.
func (l *lines) rangeOf(from, to int) protocol.Range {
	return protocol.Range{Start: l.position(from), End: l.position(max(from, to))}
}

// offset returns the byte offset of p. A line past the end is the end of
// the source, and a character past the end of its line is the line's end,
// as the specification asks; one inside a character is its start.
func (l *lines) offset(p protocol.Position) int {
	if int(p.Line) >= len(l.starts) {
		return len(l.src)
	}
	start := l.starts[p.Line]
	end := len(l.src)
	if int(p.Line)+1 < len(l.starts) {
		end = l.starts[p.Line+1] - 1 // the `\n`
	}
	if l.utf8 {
		return start + min(int(p.Character), end-start)
	}
	i, units := start, 0
	for i < end {
		r, size := utf8.DecodeRune(l.src[i:end])
		if units+utf16Len(r) > int(p.Character) {
			break
		}
		units += utf16Len(r)
		i += size
	}
	return i
}

// utf16Len is how many UTF-16 code units r takes. A byte that isn't
// UTF-8 decodes as the replacement character, one unit.
func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// pathOf returns the file path a file URI names, or false for a URI of
// another scheme, such as an editor's unsaved untitled: buffer.
func pathOf(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		// file:///C:/x has the path /C:/x; a UNC share keeps its host.
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		} else if u.Host != "" {
			p = "//" + u.Host + p
		}
	}
	return filepath.Clean(filepath.FromSlash(p)), true
}

// uriOf returns the file URI of an absolute path.
func uriOf(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // a Windows drive letter
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
