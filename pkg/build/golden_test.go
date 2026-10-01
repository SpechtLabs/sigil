package build_test

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/format"
	"github.com/spechtlabs/sigil/pkg/build"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestGolden renders sets of documents built in Go and compares them with
// testdata/<name>.golden. Every rendered document must check against its
// kind, next to the documents of testdata/base, and be a fixed point of
// `sigil fmt`. Run with -update to accept changes; review the diff, since
// the golden files are the specification of what the builder renders.
func TestGolden(t *testing.T) {
	tests := []struct {
		name  string
		base  fs.FS // the documents written by hand
		check func(fs.FS, ...build.Doc) error
		docs  func() []build.Doc
	}{
		{
			name:  "deploy",
			check: func(base fs.FS, docs ...build.Doc) error { return build.Check(Deploy, base, docs...) },
			docs: func() []build.Doc {
				c := common()
				return []build.Doc{c, guardrails(c), production(c)}
			},
		},
		{
			name:  "access",
			base:  os.DirFS(filepath.Join("testdata", "base")),
			check: func(base fs.FS, docs ...build.Doc) error { return build.Check(Access, base, docs...) },
			docs: func() []build.Doc {
				c := accessCommon()
				g := accessGuard(c)
				return []build.Doc{c, g, accessTeam(g)}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := tt.docs()
			var got strings.Builder
			for _, d := range docs {
				src, err := d.Source()
				if err != nil {
					t.Fatalf("%s: %v", d.Name(), err)
				}
				if again, errs := format.Source(d.Path(), src); errs != nil || !bytes.Equal(again, src) {
					t.Errorf("%s isn't a fixed point of sigil fmt (%v):\n%s", d.Path(), errs, again)
				}
				got.WriteString("== " + d.Path() + " ==\n")
				got.Write(src)
			}
			if err := tt.check(tt.base, docs...); err != nil {
				t.Errorf("the rendered documents don't check:\n%v", err)
			}

			path := filepath.Join("testdata", tt.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got.String() != string(want) {
				t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", path, got.String(), want)
			}
		})
	}
}
