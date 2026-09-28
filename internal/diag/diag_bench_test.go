package diag_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

func BenchmarkDiagnosticRender(b *testing.B) {
	src := []byte(benchtest.Policy(64))
	e := &diag.Error{File: "bench.sigil", Msg: "invalid expression", Help: "check this condition", Pos: token.Pos{Line: 32, Column: 6}, End: token.Pos{Line: 32, Column: 13}}
	b.ReportAllocs()
	for b.Loop() {
		diag.Render(e, src)
	}
}
