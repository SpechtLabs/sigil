package resultdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReset(t *testing.T) {
	tests := []struct {
		name     string
		abs      bool
		existing map[string]string
		files    []string
		wantGone []string
		wantKept []string
		wantErr  string
	}{
		{name: "new directory"},
		{
			name:     "an earlier run's files",
			existing: map[string]string{"out/summary.md": "old", "out/metadata.json": "old", "out/head.txt": "old", "out/keep.txt": "keep"},
			files:    []string{"head.txt"},
			wantGone: []string{Summary, Metadata, "head.txt"},
			wantKept: []string{"keep.txt"},
		},
		{name: "absolute directory", abs: true, existing: map[string]string{"out/summary.md": "old"}, wantGone: []string{Summary}},
		{name: "a file in the way", existing: map[string]string{"out": "file"}, wantErr: "can't create the results directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, tt.existing)
			dir := "out"
			if tt.abs {
				dir = filepath.Join(root, "out")
			}

			d, err := Reset(root, dir, tt.files...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Reset() = %v, want an error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.Path("x") != filepath.Join(root, "out", "x") {
				t.Errorf("Path() = %q", d.Path("x"))
			}
			for _, name := range tt.wantGone {
				if _, err := os.Stat(d.Path(name)); !os.IsNotExist(err) {
					t.Errorf("%s should be removed", name)
				}
			}
			for _, name := range tt.wantKept {
				if _, err := os.Stat(d.Path(name)); err != nil {
					t.Errorf("%s should be kept: %v", name, err)
				}
			}
		})
	}
}

func TestWrite(t *testing.T) {
	tests := []struct {
		name  string
		write func(d Dir) error
		want  string
	}{
		{name: "write", write: func(d Dir) error { return d.Write("f", "text") }, want: "text"},
		{
			name: "write replaces",
			write: func(d Dir) error {
				if err := d.Write("f", "old"); err != nil {
					return err
				}
				return d.Write("f", "new")
			},
			want: "new",
		},
		{name: "JSON", write: func(d Dir) error { return d.WriteJSON("f", map[string]int{"a": 1}) }, want: "{\n  \"a\": 1\n}\n"},
		{
			name: "append creates, then appends",
			write: func(d Dir) error {
				if err := d.Append("f", "one\n"); err != nil {
					return err
				}
				return d.Append("f", "two\n")
			},
			want: "one\ntwo\n",
		},
		{
			name: "open appends",
			write: func(d Dir) error {
				if err := d.Write("f", "one\n"); err != nil {
					return err
				}
				f, err := d.Open("f")
				if err != nil {
					return err
				}
				if _, err := f.WriteString("two\n"); err != nil {
					return err
				}
				return f.Close()
			},
			want: "one\ntwo\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, herr := Reset(t.TempDir(), "out")
			if herr != nil {
				t.Fatal(herr)
			}
			if err := tt.write(d); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(d.Path("f"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteErrors(t *testing.T) {
	d, herr := Reset(t.TempDir(), "out")
	if herr != nil {
		t.Fatal(herr)
	}
	// A directory where the file should be makes every write fail.
	if err := os.Mkdir(d.Path("f"), 0o750); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		write   func() error
		wantErr string
	}{
		{name: "write", write: func() error { return d.Write("f", "x") }, wantErr: "can't write"},
		{name: "JSON", write: func() error { return d.WriteJSON("f", 1) }, wantErr: "can't write"},
		{name: "unencodable JSON", write: func() error { return d.WriteJSON("g", func() {}) }, wantErr: "can't encode g"},
		{name: "append", write: func() error { return d.Append("f", "x") }, wantErr: "can't open"},
		{name: "open", write: func() error { _, err := d.Open("f"); return err }, wantErr: "can't open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.write(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDisplay(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "below the working directory", path: filepath.Join(wd, "out", "summary.md"), want: filepath.Join("out", "summary.md")},
		{name: "the working directory", path: wd, want: "."},
		{name: "elsewhere", path: outside, want: outside},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Display(tt.path); got != tt.want {
				t.Errorf("Display(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}

	d, herr := Reset(wd, "out")
	if herr != nil {
		t.Fatal(herr)
	}
	if got := d.Display(); got != "out" {
		t.Errorf("Dir.Display() = %q, want out", got)
	}
	if got := d.Display(Summary); got != filepath.Join("out", Summary) {
		t.Errorf("Dir.Display(Summary) = %q", got)
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
