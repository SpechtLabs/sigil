package check_test

import (
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/parser"
)

func BenchmarkCheckPolicy(b *testing.B) {
	k, _ := benchtest.Kind(b, false)
	for _, n := range []int{1, 8, 16, 32, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			file, errs := parser.ParseFile("bench.sigil", []byte(benchtest.Policy(n)))
			if errs != nil {
				b.Fatal(errs)
			}
			doc := file.Docs[0].(*ast.PolicyDoc)
			b.ReportAllocs()
			for b.Loop() {
				c := check.New("bench.sigil")
				c.Policy(doc, k)
				if c.Errors() != nil {
					b.Fatal(c.Errors())
				}
			}
		})
	}
}

func BenchmarkLoadKind(b *testing.B) {
	k, _ := benchtest.Kind(b, false)
	src := []byte(k.Source())
	b.ReportAllocs()
	for b.Loop() {
		if _, errs := check.LoadKind("bench.sigil", src); errs != nil {
			b.Fatal(errs)
		}
	}
}
