package bench

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkloads(t *testing.T) {
	tests := []struct {
		name         string
		files        map[string]string
		patterns     []string
		filter       string
		want         []string
		wantPackages []string
		wantErr      string
	}{
		{
			name: "benchmarks in bench files",
			files: map[string]string{
				"a/a.go":            "package a\n",
				"a/a_bench_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n\nfunc BenchmarkB(b *testing.B) {}\n",
				"a/a_test.go":       "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
				"b/b_bench_test.go": "package b_test\n\nimport \"testing\"\n\nfunc BenchmarkB(b *testing.B) {}\n",
			},
			want:         []string{"a/a_bench_test.go", "b/b_bench_test.go"},
			wantPackages: []string{"./a", "./b"},
		},
		{
			name: "packages with a matching benchmark, and all their bench files",
			files: map[string]string{
				"a/a_bench_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n",
				"a/x_bench_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n",
				"b/b_bench_test.go": "package b\n\nimport \"testing\"\n\nfunc BenchmarkB(b *testing.B) {}\n",
			},
			filter:       "X/sub",
			want:         []string{"a/a_bench_test.go", "a/x_bench_test.go"},
			wantPackages: []string{"./a"},
		},
		{
			name:         "selected packages",
			files:        map[string]string{"a/a_bench_test.go": benchFile("BenchmarkA"), "b/b_bench_test.go": "package b\n\nimport \"testing\"\n\nfunc BenchmarkB(b *testing.B) {}\n"},
			patterns:     []string{"./b"},
			want:         []string{"b/b_bench_test.go"},
			wantPackages: []string{"./b"},
		},
		{
			name:     "selected package without benchmarks",
			files:    map[string]string{"a/a_bench_test.go": benchFile("BenchmarkA"), "c/c.go": "package c\n"},
			patterns: []string{"./c"},
			wantErr:  "no benchmarks in ./c",
		},
		{
			name: "benchmark in an ordinary test file",
			files: map[string]string{
				"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n",
			},
			wantErr: "BenchmarkA is declared in a/a_test.go",
		},
		{
			name:    "no benchmarks",
			files:   map[string]string{"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"},
			wantErr: "no benchmarks in ./...",
		},
		{
			name:    "module that doesn't load",
			files:   map[string]string{"go.mod": "module\n"},
			wantErr: "go list",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{"go.mod": "module example.com/m\n\ngo 1.27\n"})
			writeFiles(t, root, tt.files)

			got, packages, err := workloads(t.Context(), root, tt.patterns, tt.filter)
			checkErr(t, err, tt.wantErr)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") || strings.Join(packages, ",") != strings.Join(tt.wantPackages, ",") {
				t.Errorf("workloads() = %q, %q; want %q, %q", got, packages, tt.want, tt.wantPackages)
			}
		})
	}
}

func TestInstallWorkloads(t *testing.T) {
	tmp := t.TempDir()
	head, base := filepath.Join(tmp, "head"), filepath.Join(tmp, "base")
	writeFiles(t, base, map[string]string{
		"stale_bench_test.go":        "stale",
		"library.go":                 "original library",
		"internal/benchtest/old.go":  "old fixture",
		"deep/renamed_bench_test.go": "renamed",
	})
	writeFiles(t, head, map[string]string{
		"current_bench_test.go":         "new workload",
		"deep/current_bench_test.go":    "new deep workload",
		"internal/benchtest/fixture.go": "new fixture",
	})

	if err := installWorkloads(head, base, []string{"current_bench_test.go", "deep/current_bench_test.go"}, "internal/benchtest"); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"stale_bench_test.go":           "",
		"deep/renamed_bench_test.go":    "",
		"internal/benchtest/old.go":     "",
		"library.go":                    "original library",
		"current_bench_test.go":         "new workload",
		"deep/current_bench_test.go":    "new deep workload",
		"internal/benchtest/fixture.go": "new fixture",
	}
	for name, content := range want {
		got, err := os.ReadFile(filepath.Join(base, name))
		switch {
		case content == "" && !os.IsNotExist(err):
			t.Errorf("%s should have been removed", name)
		case content != "" && string(got) != content:
			t.Errorf("%s = %q, want %q", name, got, content)
		}
	}
}

func TestInstallWorkloadsErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		wantErr string
	}{
		{name: "missing workload", files: []string{"missing_bench_test.go"}, wantErr: "can't copy missing_bench_test.go"},
		{name: "missing fixtures", wantErr: "can't copy internal/benchtest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			writeFiles(t, tmp, map[string]string{"base/library.go": ""})
			err := installWorkloads(filepath.Join(tmp, "head"), filepath.Join(tmp, "base"), tt.files, "internal/benchtest")
			checkErr(t, err, tt.wantErr)
		})
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		name    string
		entries []*tar.Header
		want    map[string]string
		wantErr string
	}{
		{
			name: "directories and files",
			entries: []*tar.Header{
				{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": "abc"}},
				{Typeflag: tar.TypeDir, Name: "a/", Mode: 0o755},
				{Typeflag: tar.TypeReg, Name: "a/a.go", Mode: 0o644},
				{Typeflag: tar.TypeReg, Name: "b/b.go", Mode: 0o644},
			},
			want: map[string]string{"a/a.go": "a/a.go", "b/b.go": "b/b.go"},
		},
		{
			name:    "symbolic link",
			entries: []*tar.Header{{Typeflag: tar.TypeSymlink, Name: "link", Linkname: "a"}},
			wantErr: "unsupported git archive entry link",
		},
		{
			name:    "path outside the directory",
			entries: []*tar.Header{{Typeflag: tar.TypeReg, Name: "../escape.go", Mode: 0o644}},
			wantErr: "can't extract ../escape.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			for _, h := range tt.entries {
				// Every file holds its own name.
				if h.Typeflag == tar.TypeReg {
					h.Size = int64(len(h.Name))
				}
				if err := tw.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Typeflag == tar.TypeReg {
					if _, err := tw.Write([]byte(h.Name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}

			dir := filepath.Join(t.TempDir(), "out")
			checkErr(t, extract(&buf, dir), tt.wantErr)
			for name, content := range tt.want {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(got) != content {
					t.Errorf("%s = %q, %v; want %q", name, got, err, content)
				}
			}
		})
	}
}

func TestExtractCorruptArchive(t *testing.T) {
	err := extract(strings.NewReader(strings.Repeat("x", 1024)), t.TempDir())
	checkErr(t, err, "can't read the git archive")
}
