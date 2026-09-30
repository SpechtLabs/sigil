package bench

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripNew(t *testing.T) {
	const header = "package a\n\nimport (\n\t\"strings\"\n\t\"testing\"\n\n\tv2 \"example.com/lib/v2\"\n\t\"example.com/newlib\"\n)\n\n"
	const old = "// BenchmarkOld is on both revisions.\nfunc BenchmarkOld(b *testing.B) {\n\tfor b.Loop() {\n\t\t_ = strings.ToUpper(\"x\")\n\t}\n}\n"
	const added = "// BenchmarkNew only exists in the checkout.\nfunc BenchmarkNew(b *testing.B) {\n\t// It needs the new library.\n\t_ = newlib.Value\n\t_ = v2.Thing\n\tfor b.Loop() {\n\t}\n}\n"

	tests := []struct {
		name    string
		src     string
		keep    map[string]bool
		want    []string
		wantNot []string
		same    bool
		wantErr string
	}{
		{
			name:    "a new benchmark and the imports only it used",
			src:     header + old + "\n" + added,
			keep:    map[string]bool{"BenchmarkOld": true},
			want:    []string{"func BenchmarkOld", `"strings"`, `"testing"`},
			wantNot: []string{"BenchmarkNew", "only exists in the checkout", "It needs the new library", "newlib", "example.com/lib/v2"},
		},
		{name: "nothing new", src: header + old + "\n" + added, keep: map[string]bool{"BenchmarkOld": true, "BenchmarkNew": true}, same: true},
		{
			name:    "every benchmark new keeps the imports the rest uses",
			src:     "package a\n\nimport \"testing\"\n\nvar _ testing.B\n\nfunc BenchmarkNew(b *testing.B) {}\n",
			want:    []string{`import "testing"`, "var _ testing.B"},
			wantNot: []string{"BenchmarkNew"},
		},
		{
			name:    "an import group that ends up empty goes",
			src:     "package a\n\nimport \"example.com/newlib\"\n\nfunc BenchmarkNew(b *testing.B) { _ = newlib.Value }\n",
			wantNot: []string{"import", "BenchmarkNew"},
		},
		{
			name:    "a method named like a benchmark stays",
			src:     "package a\n\ntype T struct{}\n\nfunc (T) BenchmarkNew() {}\n",
			same:    true,
			wantNot: []string{},
		},
		{name: "a file that doesn't parse", src: "package a\n\nfunc {", wantErr: "can't parse a/a_bench_test.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stripNew("a/a_bench_test.go", []byte(tt.src), tt.keep)
			checkErr(t, err, tt.wantErr)
			if tt.wantErr != "" {
				return
			}
			if tt.same && string(got) != tt.src {
				t.Errorf("stripNew changed a file with nothing new:\n%s", got)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), "", got, 0); err != nil {
				t.Errorf("result doesn't parse: %v\n%s", err, got)
			}
			for _, w := range tt.want {
				if !strings.Contains(string(got), w) {
					t.Errorf("result doesn't contain %q:\n%s", w, got)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(string(got), w) {
					t.Errorf("result contains %q:\n%s", w, got)
				}
			}
		})
	}
}

func TestImportName(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{spec: `"testing"`, want: "testing"},
		{spec: `"example.com/m/internal/eval"`, want: "eval"},
		{spec: `"example.com/lib/v2"`, want: "lib"},
		{spec: `alias "example.com/lib"`, want: "alias"},
		{spec: `_ "embed"`, want: ""},
		{spec: `. "strings"`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "", "package p\nimport "+tt.spec+"\n", parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			if got := importName(f.Imports[0]); got != tt.want {
				t.Errorf("importName(%s) = %q, want %q", tt.spec, got, tt.want)
			}
		})
	}
	// A path that isn't a valid string literal binds no name we can read.
	if got := importName(&ast.ImportSpec{Path: &ast.BasicLit{Value: "bad"}}); got != "" {
		t.Errorf("importName(bad) = %q", got)
	}
}

func TestDeclared(t *testing.T) {
	base := testModule(t)
	writeFiles(t, base, map[string]string{
		"a/extra_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkNotAWorkload(b *testing.B) {}\n",
		"empty/README":    "no Go here",
	})
	got, err := declared(t.Context(), base, []string{"a", "empty", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("declared = %v, want only a", got)
	}
	if !got["a"]["BenchmarkSum"] || !got["a"]["BenchmarkSumLong"] || got["a"]["BenchmarkNotAWorkload"] {
		t.Errorf("declared[a] = %v, want the benchmarks of its *_bench_test.go files", got["a"])
	}

	none, err := declared(t.Context(), base, []string{"missing"})
	if err != nil || len(none) != 0 {
		t.Errorf("declared(missing) = %v, %v; want nothing", none, err)
	}
}

func TestInstallWorkloadsWithoutNewBenchmarks(t *testing.T) {
	tmp := t.TempDir()
	head, base := filepath.Join(tmp, "head"), filepath.Join(tmp, "base")
	writeFiles(t, base, map[string]string{"internal/benchtest/fixture.go": "base fixture"})
	writeFiles(t, head, map[string]string{
		"a/a_bench_test.go":             "package a\n\nimport \"testing\"\n\nfunc BenchmarkOld(b *testing.B) {}\n\nfunc BenchmarkNew(b *testing.B) {}\n",
		"internal/benchtest/fixture.go": "head fixture",
	})
	known := map[string]map[string]bool{"a": {"BenchmarkOld": true}}

	// Without fixtures, the base revision keeps its own.
	if err := installWorkloads(head, base, []string{"a/a_bench_test.go"}, "", known); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(base, "a/a_bench_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "BenchmarkOld") || strings.Contains(string(got), "BenchmarkNew") {
		t.Errorf("installed workload =\n%s\nwant BenchmarkOld without BenchmarkNew", got)
	}
	if fixture, _ := os.ReadFile(filepath.Join(base, "internal/benchtest/fixture.go")); string(fixture) != "base fixture" {
		t.Errorf("fixture = %q, want the base revision's own", fixture)
	}

	checkErr(t, installWorkloads(head, base, []string{"a/missing_bench_test.go"}, "", known), "can't copy a/missing_bench_test.go")
	writeFiles(t, head, map[string]string{"a/broken_bench_test.go": "package a\n\nfunc {"})
	checkErr(t, installWorkloads(head, base, []string{"a/broken_bench_test.go"}, "", known), "can't parse a/broken_bench_test.go")
}
