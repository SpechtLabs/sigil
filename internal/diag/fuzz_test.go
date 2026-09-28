package diag_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

func FuzzRender(f *testing.F) {
	f.Add([]byte("let x = 1\n"), uint16(1), uint16(5), uint16(6))
	f.Add([]byte("\t世界\xff\n"), uint16(1), uint16(0), uint16(65535))
	f.Fuzz(func(t *testing.T, src []byte, line, start, end uint16) {
		e := &diag.Error{File: "fuzz.sigil", Msg: "invalid expression", Help: "check the source",
			Pos: token.Pos{Line: int(line), Column: int(start)}, End: token.Pos{Line: int(line), Column: int(end)}}
		first := diag.Render(e, src)
		if first != diag.Render(e, src) {
			t.Fatal("rendering is not deterministic")
		}
		// Invalid or stale positions must never produce a caret allocation
		// larger than the source they are meant to underline.
		if len(first) > 4*len(src)+512 {
			t.Fatalf("diagnostic grew beyond source: %d bytes", len(first))
		}
	})
}
