package fuzz

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain gives the git commands devtool runs a fixed identity and none of
// the machine's configuration, which may sign commits or lack an identity,
// as CI runners do.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME":     "devtool",
		"GIT_AUTHOR_EMAIL":    "devtool@example.com",
		"GIT_COMMITTER_NAME":  "devtool",
		"GIT_COMMITTER_EMAIL": "devtool@example.com",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func TestCorpus(t *testing.T) {
	root := fuzzModule(t, "")
	origin := withOrigin(t, root)
	cache := t.TempDir()
	writeInputs(t, cache, map[string]string{
		"example.com/m/a/FuzzA/1": "go test fuzz v1\nint(1)\n",
		"example.com/m/a/FuzzB/2": "go test fuzz v1\nint(2)\n",
		"example.com/m/b/FuzzC/3": "go test fuzz v1\nint(3)\n",
	})

	steps := []struct {
		name    string
		args    []string
		env     map[string]string
		cache   string
		want    string
		wantErr string
	}{
		{name: "pull before the branch exists", args: []string{"pull"}, cache: t.TempDir(), want: "Nothing to restore\n  origin has no fuzz-corpus branch yet"},
		{name: "push one package", args: []string{"push", "./b"}, cache: cache, want: "✓ Pushed 1 input\n  to origin/fuzz-corpus"},
		{name: "push the rest", args: []string{"push"}, cache: cache, want: "✓ Pushed 2 inputs\n  to origin/fuzz-corpus"},
		{name: "push with nothing new", args: []string{"push"}, cache: cache, want: "✓ Nothing to push\n  origin/fuzz-corpus has every input already"},
		{name: "pull by name", args: []string{"pull", "--filter", "FuzzB"}, cache: t.TempDir(), want: "✓ Restored 1 input\n  from origin/fuzz-corpus for 1 fuzz target, into "},
		{name: "pull from the environment's remote", args: []string{"pull"}, env: map[string]string{"FUZZ_REMOTE": "upstream"}, cache: t.TempDir(), wantErr: "git ls-remote --heads upstream"},
		{name: "push from a directory that isn't one", args: []string{"push", "--from", filepath.Join(root, "go.mod")}, cache: cache, wantErr: "isn't a directory"},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := NewCommand(WithRoot(root), withFuzzCache(s.cache), WithGetenv(func(k string) string { return s.env[k] }))
			cmd.SetArgs(append([]string{"corpus"}, s.args...))
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			checkErr(t, cmd.Execute(), s.wantErr)
			if !strings.Contains(out.String(), s.want) {
				t.Errorf("output = %q, want it to contain %q", out.String(), s.want)
			}
		})
	}

	if got := gitOut(t, origin, "ls-tree", "-r", "--name-only", "fuzz-corpus"); got != "a/FuzzA/1\na/FuzzB/2\nb/FuzzC/3" {
		t.Errorf("branch holds %q", got)
	}
	if got := gitOut(t, origin, "log", "--format=%s", "fuzz-corpus"); got != "Add 2 inputs to 2 fuzz targets\nAdd 1 input to 1 fuzz target" {
		t.Errorf("branch history = %q", got)
	}

	// CI pushes what its jobs uploaded, and names its run.
	from := t.TempDir()
	writeInputs(t, from, map[string]string{"example.com/m/a/FuzzA/4": "go test fuzz v1\nint(4)\n"})
	env := map[string]string{"GITHUB_SERVER_URL": "https://github.com", "GITHUB_REPOSITORY": "example/m", "GITHUB_RUN_ID": "42"}
	var out bytes.Buffer
	cmd := NewCommand(WithRoot(root), withFuzzCache(t.TempDir()), WithGetenv(func(k string) string { return env[k] }))
	cmd.SetArgs([]string{"corpus", "push", "--from", from})
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if msg := gitOut(t, origin, "log", "-1", "--format=%B", "fuzz-corpus"); !strings.Contains(msg, "From https://github.com/example/m/actions/runs/42.") {
		t.Errorf("commit message = %q", msg)
	}
}

func TestRunRestoresTheCorpus(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		branch bool
		remote bool
		want   string
		inputs int
	}{
		{name: "from the branch", branch: true, remote: true, want: "Corpus:   1 new input from origin/fuzz-corpus", inputs: 1},
		{name: "without the branch", remote: true, want: "Corpus:   origin has no fuzz-corpus branch yet"},
		{name: "without the remote", want: "Corpus:   not restored, no remote origin"},
		{name: "with --no-restore", args: []string{"--no-restore"}, branch: true, remote: true, want: "Corpus:   not restored, --no-restore"},
		{name: "from a remote that's gone", args: []string{"--remote", "gone"}, want: "! Can't restore the fuzzing corpus from gone/fuzz-corpus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := fuzzModule(t, "")
			if tt.remote {
				withOrigin(t, root)
			}
			if tt.branch {
				src := t.TempDir()
				writeInputs(t, src, map[string]string{"example.com/m/b/FuzzC/1": "go test fuzz v1\nint(7)\n"})
				push := NewCommand(WithRoot(root), withFuzzCache(t.TempDir()))
				push.SetArgs([]string{"corpus", "push", "--from", src})
				push.SetOut(io.Discard)
				if err := push.Execute(); err != nil {
					t.Fatal(err)
				}
			}
			if tt.name == "from a remote that's gone" {
				gitOut(t, root, "remote", "add", "gone", filepath.Join(t.TempDir(), "missing"))
			}

			cache := t.TempDir()
			var out bytes.Buffer
			cmd := NewCommand(WithRoot(root), withFuzzCache(cache))
			cmd.SetArgs(append([]string{"run", "--time", "1x", "--results", t.TempDir(), "./b"}, tt.args...))
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("fuzz run failed: %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("output doesn't contain %q:\n%s", tt.want, out.String())
			}
			restored, _ := os.ReadDir(filepath.Join(cache, "example.com", "m", "b", "FuzzC"))
			if len(restored) != tt.inputs {
				t.Errorf("restored %d inputs, want %d", len(restored), tt.inputs)
			}
		})
	}
}

// withOrigin gives the checkout at root a bare repository as its origin,
// and returns the bare repository.
func withOrigin(t *testing.T, root string) string {
	t.Helper()
	origin := t.TempDir()
	gitOut(t, origin, "init", "-q", "--bare")
	gitOut(t, root, "remote", "add", "origin", origin)
	return origin
}

// writeInputs writes files into a directory laid out like go test's fuzz
// cache.
func writeInputs(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// gitOut runs git in dir and returns its trimmed output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
