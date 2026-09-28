package gotool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutput(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		args    []string
		want    string
		wantErr string
	}{
		{name: "standard output", args: []string{"env", "GOOS"}, want: "\n"},
		{name: "replaced environment", env: append(os.Environ(), "GOOS=plan9"), args: []string{"env", "GOOS"}, want: "plan9\n"},
		{name: "failure carries standard error", args: []string{"no-such-command"}, wantErr: "go no-such-command failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Output(t.Context(), t.TempDir(), tt.env, "go", tt.args...)
			checkErr(t, err, tt.wantErr)
			if tt.want != "\n" && got != tt.want {
				t.Errorf("Output() = %q, want %q", got, tt.want)
			}
			if tt.wantErr != "" && !strings.Contains(err.Display(), "unknown command") {
				t.Errorf("error %q doesn't carry go's standard error", err.Display())
			}
		})
	}
}

func TestModuleRoot(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"go.mod": "module example.com/m\n", "a/b/b.go": "package b\n"})

	tests := []struct {
		name    string
		dir     string
		env     map[string]string
		want    string
		wantErr string
	}{
		{name: "module root", dir: root, want: root},
		{name: "nested directory", dir: filepath.Join(root, "a", "b"), want: root},
		{name: "outside a module", dir: t.TempDir(), env: map[string]string{"GO111MODULE": "on"}, wantErr: "not inside a Go module"},
		{name: "missing directory", dir: filepath.Join(root, "missing"), wantErr: "go env GOMOD failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := ModuleRoot(t.Context(), tt.dir)
			checkErr(t, err, tt.wantErr)
			if tt.want != "" {
				want, _ := filepath.EvalSymlinks(tt.want)
				if got, _ = filepath.EvalSymlinks(got); got != want {
					t.Errorf("ModuleRoot() = %q, want %q", got, want)
				}
			}
		})
	}
}

func TestListAndFuncs(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.27\n",
		"a/a.go":      "package a\n\nfunc FuzzNotATest() {}\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc FuzzA(f *testing.F) {}\n\nfunc (s suite) FuzzMethod(f *testing.F) {}\n\ntype suite struct{}\n\nfunc TestA(t *testing.T) {}\n",
		"a/x_test.go": "package a_test\n\nimport \"testing\"\n\nfunc FuzzX(f *testing.F) {}\n\n// func FuzzCommented(f *testing.F) {}\n",
		"b/b.go":      "package b\n",
	})

	pkgs, err := List(t.Context(), root, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || pkgs[0].ImportPath != "example.com/m/a" || pkgs[1].ImportPath != "example.com/m/b" {
		t.Fatalf("List() = %+v", pkgs)
	}

	tests := []struct {
		prefix string
		pkg    Package
		want   string
	}{
		{prefix: "Fuzz", pkg: pkgs[0], want: "a_test.go:FuzzA x_test.go:FuzzX"},
		{prefix: "Test", pkg: pkgs[0], want: "a_test.go:TestA"},
		{prefix: "Benchmark", pkg: pkgs[0], want: ""},
		{prefix: "Fuzz", pkg: pkgs[1], want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.pkg.ImportPath+"/"+tt.prefix, func(t *testing.T) {
			funcs, ferr := tt.pkg.Funcs(tt.prefix)
			if ferr != nil {
				t.Fatal(ferr)
			}
			var got []string
			for _, f := range funcs {
				got = append(got, f.File+":"+f.Name)
			}
			if strings.Join(got, " ") != tt.want {
				t.Errorf("Funcs(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}

	missing := Package{Dir: root, TestGoFiles: []string{"missing_test.go"}}
	_, err = missing.Funcs("Fuzz")
	checkErr(t, err, "can't read test file missing_test.go")
}

func TestListErrors(t *testing.T) {
	tests := []struct {
		name    string
		gomod   string
		wantErr string
	}{
		{name: "invalid go.mod", gomod: "module\n", wantErr: "go list -json ./... failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{"go.mod": tt.gomod})
			_, err := List(t.Context(), root, "./...")
			checkErr(t, err, tt.wantErr)
		})
	}
}

func TestRelDir(t *testing.T) {
	tests := []struct {
		root, dir string
		want      string
	}{
		{root: "/src/m", dir: "/src/m", want: "."},
		{root: "/src/m", dir: "/src/m/internal/eval", want: "./internal/eval"},
		{root: "/src/m", dir: "/src/other", want: "/src/other"},
	}
	for _, tt := range tests {
		if got := RelDir(tt.root, tt.dir); got != tt.want {
			t.Errorf("RelDir(%q, %q) = %q, want %q", tt.root, tt.dir, got, tt.want)
		}
	}
}

func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Fatalf("expected an error containing %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Fatalf("error %q doesn't contain %q", err, want)
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
