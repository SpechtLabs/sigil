package gotool

import (
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.27\n",
		"root_test.go":  "package m\n\nimport \"testing\"\n\nfunc FuzzRoot(f *testing.F) {}\n",
		"a/a.go":        "package a\n",
		"a/a_test.go":   "package a\n\nimport \"testing\"\n\nfunc FuzzA(f *testing.F) {}\n\nfunc BenchmarkA(b *testing.B) {}\n\nfunc TestA(t *testing.T) {}\n",
		"a/x_test.go":   "package a_test\n\nimport \"testing\"\n\nfunc FuzzB(f *testing.F) {}\n",
		"b/c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc FuzzC(f *testing.F) {}\n",
		"d/d.go":        "package d\n",
	})

	tests := []struct {
		name     string
		patterns []string
		prefix   string
		want     []Target
		wantErr  string
	}{
		{
			name:   "every package by default",
			prefix: "Fuzz",
			want: []Target{
				{Package: "example.com/m", Dir: ".", File: "root_test.go", Name: "FuzzRoot"},
				{Package: "example.com/m/a", Dir: "./a", File: "a/a_test.go", Name: "FuzzA"},
				{Package: "example.com/m/a", Dir: "./a", File: "a/x_test.go", Name: "FuzzB"},
				{Package: "example.com/m/b/c", Dir: "./b/c", File: "b/c/c_test.go", Name: "FuzzC"},
			},
		},
		{
			name:     "nested packages",
			patterns: []string{"./b/..."},
			prefix:   "Fuzz",
			want:     []Target{{Package: "example.com/m/b/c", Dir: "./b/c", File: "b/c/c_test.go", Name: "FuzzC"}},
		},
		{
			name:   "another prefix",
			prefix: "Benchmark",
			want:   []Target{{Package: "example.com/m/a", Dir: "./a", File: "a/a_test.go", Name: "BenchmarkA"}},
		},
		{name: "a package without any", patterns: []string{"./d"}, prefix: "Fuzz"},
		{name: "a package that doesn't exist", patterns: []string{"./missing"}, prefix: "Fuzz", wantErr: "go list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Discover(t.Context(), root, tt.patterns, tt.prefix)
			checkErr(t, err, tt.wantErr)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Discover() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	targets := []Target{
		{Dir: "./a", Name: "FuzzA"},
		{Dir: "./a", Name: "FuzzB"},
		{Dir: "./b", Name: "FuzzC"},
	}
	tests := []struct {
		name     string
		targets  []Target
		filter   string
		patterns []string
		want     []string
		wantErr  string
	}{
		{name: "an empty filter matches all", targets: targets, want: []string{"FuzzA", "FuzzB", "FuzzC"}},
		{name: "a filter selects", targets: targets, filter: "B|C", want: []string{"FuzzB", "FuzzC"}},
		{name: "none matching", targets: targets, filter: "Nope", wantErr: "no fuzz targets matching Nope in ./..."},
		{name: "none at all", patterns: []string{"./c"}, wantErr: "no fuzz targets in ./c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Select(tt.targets, regexp.MustCompile(tt.filter), tt.patterns, "fuzz targets")
			checkErr(t, err, tt.wantErr)
			var names []string
			for _, g := range got {
				names = append(names, g.Name)
			}
			if strings.Join(names, ",") != strings.Join(tt.want, ",") {
				t.Errorf("Select() = %q, want %q", names, tt.want)
			}
		})
	}
}

func TestPackages(t *testing.T) {
	tests := []struct {
		name    string
		targets []Target
		want    []string
	}{
		{name: "none"},
		{name: "one per package, in order", targets: []Target{{Dir: "./b"}, {Dir: "./b"}, {Dir: "./a"}}, want: []string{"./b", "./a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Packages(tt.targets); strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("Packages() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompileFilter(t *testing.T) {
	tests := []struct {
		expr    string
		match   string
		wantErr string
	}{
		{expr: "", match: "FuzzAnything"},
		{expr: "Parse|Lexer", match: "FuzzLexer"},
		{expr: "Parse(", wantErr: "--filter Parse( isn't a valid regular expression"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			re, err := CompileFilter(tt.expr)
			checkErr(t, err, tt.wantErr)
			if err == nil && !re.MatchString(tt.match) {
				t.Errorf("%q doesn't match %q", tt.expr, tt.match)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"go.mod": "module example.com/m\n\ngo 1.27\n"})
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=x", "-c", "user.email=x@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}
	}
	head, err := Output(t.Context(), root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		dir       string
		untracked bool
		wantDirty bool
		wantErr   string
	}{
		{name: "clean checkout", dir: root},
		{name: "untracked file", dir: root, untracked: true, wantDirty: true},
		{name: "not a repository", dir: t.TempDir(), wantErr: "git rev-parse HEAD failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.untracked {
				writeFiles(t, root, map[string]string{"untracked.txt": ""})
			}
			c, err := Describe(t.Context(), tt.dir)
			checkErr(t, err, tt.wantErr)
			if err != nil {
				return
			}
			if c.Head != strings.TrimSpace(head) || c.Short() != c.Head[:7] {
				t.Errorf("Head, Short() = %q, %q; want %q", c.Head, c.Short(), strings.TrimSpace(head))
			}
			if c.Dirty != tt.wantDirty {
				t.Errorf("Dirty = %v, want %v", c.Dirty, tt.wantDirty)
			}
			if !strings.HasPrefix(c.Go, "go1.") || !strings.Contains(c.Go, " ") || !strings.Contains(c.Go, "/") {
				t.Errorf("Go = %q, want e.g. go1.27.1 darwin/arm64", c.Go)
			}
		})
	}
}

func TestShortSHA(t *testing.T) {
	tests := []struct {
		sha, want string
	}{
		{sha: "3868261f1630ddbfa10a4f65d2cd3541e2ba2b09", want: "3868261"},
		{sha: "abc", want: "abc"},
		{sha: "", want: ""},
	}
	for _, tt := range tests {
		if got := ShortSHA(tt.sha); got != tt.want {
			t.Errorf("ShortSHA(%q) = %q, want %q", tt.sha, got, tt.want)
		}
	}
}
