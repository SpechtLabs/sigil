package testsuite_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/testsuite"
)

func BenchmarkTestSuiteParse(b *testing.B) {
	src := []byte("policy: main\ncases:\n" + strings.Repeat("  - name: member\n    input: {actor: ada, enabled: true}\n    expect: {decision: allow, reason: member}\n", 32))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := testsuite.Parse("bench_test.yaml", src); err != nil {
			b.Fatal(err)
		}
	}
}
