package lsp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kindSource is a kind small enough to write in a test.
const kindSource = "kind K version 1\n\ninput n: int\n\ndecision deny {\n  reason: no\n}\n\ncollect all\n"

// TestRoot finds a document's project root: the nearest configuration
// file's directory, a declared project; or the deepest workspace folder
// holding it, when it's small enough to read whole; or for a large folder
// the document's directory, or the document alone, with a note; or for a
// document outside every folder, the document alone.
func TestRoot(t *testing.T) {
	tmp := realTemp(t)
	files := map[string]string{
		"repo/sigil.yaml":             "",
		"repo/teams/a.sigil":          "",
		"ws/inner/b.sigil":            "",
		"loose/c.sigil":               "",
		"two/sigil.yaml":              "",
		"two/sigil.json":              "{}",
		"two/d.sigil":                 "",
		"ws/inner/deeper/sub/e.sigil": "",
		"big/team/f.sigil":            "",
		"huge/g.sigil":                "",
	}
	for i := range maxUndeclared {
		files[fmt.Sprintf("big/vendor/%d.sigil", i)] = ""
		files[fmt.Sprintf("huge/%d.sigil", i)] = ""
		files[fmt.Sprintf("big/.hidden/%d.sigil", i)] = ""
	}
	writeFiles(t, tmp, files)
	folders := []string{filepath.Join(tmp, "ws"), filepath.Join(tmp, "ws", "inner"), filepath.Join(tmp, "two"), filepath.Join(tmp, "big"), filepath.Join(tmp, "huge")}
	tests := []struct {
		file     string
		want     string
		declared bool
		note     bool
	}{
		{file: "repo/teams/a.sigil", want: "repo", declared: true},
		{file: "ws/inner/b.sigil", want: "ws/inner"},
		{file: "ws/inner/deeper/sub/e.sigil", want: "ws/inner"},
		{file: "loose/c.sigil", want: "loose/c.sigil"},
		{file: "two/d.sigil", want: "two"},
		{file: "big/team/f.sigil", want: "big/team", note: true},
		{file: "huge/g.sigil", want: "huge/g.sigil", note: true},
		{file: "loose/unsaved/new.sigil", want: "loose/unsaved/new.sigil"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := loader{}.Root(filepath.Join(tmp, filepath.FromSlash(tt.file)), folders)
			if want := filepath.Join(tmp, filepath.FromSlash(tt.want)); got.Path != want || got.Declared != tt.declared || (got.Note != "") != tt.note {
				t.Errorf("Root() = %+v, want %q, declared %v, a note %v", got, want, tt.declared, tt.note)
			}
		})
	}
}

// TestLoad loads projects as `sigil check` reads them: with a
// configuration file and without one, a single file, one whose
// configuration doesn't parse, and one whose requirements can't be
// enforced, which still loads.
func TestLoad(t *testing.T) {
	policy := "policy a.p: K@1\n\nwhen n > 1 {\n  deny(reason: no)\n}\n"
	tests := []struct {
		name    string
		files   map[string]string
		root    string // below the temporary directory
		overlay map[string]string
		project bool   // the snapshot has a project
		err     string // what the snapshot's error holds
		diags   []string
	}{
		{name: "no configuration", files: map[string]string{"k.sigil": kindSource, "p/a.sigil": policy}, project: true},
		{name: "a lint the configuration sets", files: map[string]string{"sigil.yaml": "lints:\n  unused-let: error\n", "k.sigil": kindSource, "a.sigil": "policy a.p: K@1\n\nlet x = 1\n"}, project: true, diags: []string{"error unused-let: let x is never read"}},
		{name: "a buffer over a file", files: map[string]string{"k.sigil": kindSource, "a.sigil": policy}, overlay: map[string]string{"a.sigil": "policy a.p: K@1\n\nlet x = nope\n"}, project: true, diags: []string{"error : unknown name `nope`"}},
		{name: "a file alone", files: map[string]string{"k.sigil": kindSource, "a.sigil": policy}, root: "a.sigil", project: true, diags: []string{"error : policy a.p is written against kind K, but no kind K was found"}},
		{name: "a buffer that isn't on disk", root: "gone/x.sigil", overlay: map[string]string{"gone/x.sigil": kindSource + "---\n" + policy}, project: true},
		{name: "a configuration that doesn't parse", files: map[string]string{"sigil.yaml": "nope: 1\n", "a.sigil": policy}, err: "unknown key"},
		{name: "two configurations", files: map[string]string{"sigil.yaml": "", "sigil.json": "{}"}, err: "are both configuration files"},
		{name: "a requirement that can't be enforced", files: map[string]string{"sigil.yaml": "require:\n  - policy: a.missing\n", "k.sigil": kindSource, "a.sigil": policy}, project: true, err: "a.missing is required, but no policy a.missing was found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := realTemp(t)
			writeFiles(t, tmp, tt.files)
			overlay := map[string][]byte{}
			for name, src := range tt.overlay {
				overlay[filepath.Join(tmp, name)] = []byte(src)
			}
			snap := loader{}.Load(filepath.Join(tmp, tt.root), overlay)
			if (snap.Project != nil) != tt.project {
				t.Errorf("project = %v, want one: %v", snap.Project, tt.project)
			}
			switch {
			case tt.err == "" && snap.Err != nil:
				t.Errorf("error = %v", snap.Err)
			case tt.err != "" && (snap.Err == nil || !strings.Contains(snap.Err.Error(), tt.err)):
				t.Errorf("error = %v, want one holding %q", snap.Err, tt.err)
			}
			var diags []string
			for _, d := range snap.Diagnostics {
				if !filepath.IsAbs(filepath.FromSlash(d.File)) {
					t.Errorf("%s isn't an absolute path", d.File)
				}
				diags = append(diags, fmt.Sprintf("%s %s: %s", d.Severity, d.Code, d.Msg))
			}
			if fmt.Sprint(diags) != fmt.Sprint(tt.diags) {
				t.Errorf("diagnostics = %q, want %q", diags, tt.diags)
			}
		})
	}
}

// TestCommand runs the command as the CLI does: a whole session from
// stdin ends cleanly, and a stdin that ends without one fails.
func TestCommand(t *testing.T) {
	session := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)
	tests := []struct {
		name  string
		stdin string
		err   string
		out   string // a substring of stdout
	}{
		{name: "a session", stdin: session, out: `"id":2,"result":null`},
		{name: "no session", stdin: "", err: "closed the connection without shutting the language server down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(WithVersion("test"), WithKinds(nil))
			cmd.SilenceUsage = true // as the root command does
			var out, errs bytes.Buffer
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetOut(&out)
			cmd.SetErr(&errs)
			cmd.SetArgs([]string{"--stdio"})
			err := cmd.Execute()
			switch {
			case tt.err == "" && err != nil:
				t.Errorf("Execute() = %v", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("Execute() = %v, want an error holding %q", err, tt.err)
			}
			if !strings.Contains(out.String(), tt.out) {
				t.Errorf("stdout = %q, want it to hold %q", out.String(), tt.out)
			}
			if !strings.HasPrefix(out.String(), "Content-Length: ") && out.Len() > 0 {
				t.Errorf("stdout holds more than protocol: %q", out.String())
			}
		})
	}
}

// realTemp returns a temporary directory by its real path, so paths the
// loader resolves compare equal to it.
func realTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return dir
}

// writeFiles writes files, by their slash-separated paths below dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// frame frames a body as the base protocol does.
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
