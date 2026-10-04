package corpus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The targets the tests move corpora for: two in one package, one in the
// module's root package.
var (
	fuzzA    = Target{Package: "example.com/m/a", Dir: "a", Name: "FuzzA"}
	fuzzB    = Target{Package: "example.com/m/a", Dir: "a", Name: "FuzzB"}
	fuzzRoot = Target{Package: "example.com/m", Dir: "", Name: "FuzzRoot"}
)

// TestMain gives every git command, Repo's own included, a fixed identity
// and none of the machine's configuration, which may sign commits or lack
// an identity, as CI runners do.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "devtool", "GIT_AUTHOR_EMAIL": "devtool@example.com",
		"GIT_COMMITTER_NAME": "devtool", "GIT_COMMITTER_EMAIL": "devtool@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func TestSaveAndRestore(t *testing.T) {
	origin := bareRepo(t)
	alice, bob := clone(t, origin), clone(t, origin)

	if found, err := alice.Fetch(t.Context()); err != nil || found {
		t.Fatalf("Fetch() on a remote without the branch = %v, %v; want false", found, err)
	}

	// The first save creates the branch.
	src := cacheDir(t, map[string]string{
		"example.com/m/a/FuzzA/1":  "go test fuzz v1\nint(1)\n",
		"example.com/m/a/FuzzA/2":  "go test fuzz v1\nint(2)\n",
		"example.com/m/a/FuzzB/3":  "go test fuzz v1\n[]byte(\"\\x00\\xff\")\n",
		"example.com/m/FuzzRoot/4": "go test fuzz v1\nstring(\"root\")\n",
		"example.com/m/a/FuzzA/.5": "a hidden file go test didn't write",
		"example.com/m/b/FuzzC/6":  "a target Save wasn't asked for",
	})
	if n, err := alice.Save(t.Context(), src, []Target{fuzzA, fuzzB, fuzzRoot}, "alice's machine"); err != nil || n != 4 {
		t.Fatalf("first Save() = %d, %v; want 4", n, err)
	}
	if got, want := branchFiles(t, origin), []string{"FuzzRoot/4", "a/FuzzA/1", "a/FuzzA/2", "a/FuzzB/3"}; !slices.Equal(got, want) {
		t.Errorf("branch holds %q, want %q", got, want)
	}
	if msg := git(t, origin, "log", "-1", "--format=%B", Branch); !strings.Contains(msg, "Add 4 inputs to 3 fuzz targets") || !strings.Contains(msg, "From alice's machine.") {
		t.Errorf("commit message = %q", msg)
	}

	// Saving the same inputs again pushes nothing.
	if n, err := alice.Save(t.Context(), src, []Target{fuzzA, fuzzB, fuzzRoot}, "alice's machine"); err != nil || n != 0 {
		t.Fatalf("repeated Save() = %d, %v; want 0", n, err)
	}
	if commits := git(t, origin, "rev-list", "--count", Branch); commits != "1" {
		t.Errorf("branch has %s commits after a save with nothing new, want 1", commits)
	}

	// Another checkout adds only what the branch lacks, on top of it.
	more := cacheDir(t, map[string]string{
		"example.com/m/a/FuzzA/1": "go test fuzz v1\nint(1)\n",
		"example.com/m/a/FuzzA/7": "go test fuzz v1\nint(7)\n",
	})
	if n, err := bob.Save(t.Context(), more, []Target{fuzzA}, "bob's machine"); err != nil || n != 1 {
		t.Fatalf("Save() from another checkout = %d, %v; want 1", n, err)
	}
	if commits := git(t, origin, "rev-list", "--count", Branch); commits != "2" {
		t.Errorf("branch has %s commits, want 2", commits)
	}

	// Restoring copies the selected targets' inputs, and only once.
	if found, err := alice.Fetch(t.Context()); err != nil || !found {
		t.Fatalf("Fetch() = %v, %v; want true", found, err)
	}
	cache := t.TempDir()
	if n, err := alice.Restore(t.Context(), cache, []Target{fuzzA, fuzzRoot}); err != nil || n != 4 {
		t.Fatalf("Restore() = %d, %v; want 4", n, err)
	}
	for name, want := range map[string]string{
		"example.com/m/a/FuzzA/1":  "go test fuzz v1\nint(1)\n",
		"example.com/m/a/FuzzA/7":  "go test fuzz v1\nint(7)\n",
		"example.com/m/FuzzRoot/4": "go test fuzz v1\nstring(\"root\")\n",
	} {
		if got, err := os.ReadFile(filepath.Join(cache, filepath.FromSlash(name))); err != nil || string(got) != want {
			t.Errorf("restored %s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(cache, "example.com", "m", "a", "FuzzB")); !os.IsNotExist(err) {
		t.Errorf("Restore() copied FuzzB, which it wasn't asked for")
	}
	if n, err := alice.Restore(t.Context(), cache, []Target{fuzzA, fuzzB, fuzzRoot}); err != nil || n != 1 {
		t.Errorf("second Restore() = %d, %v; want only FuzzB's input", n, err)
	}

	// The checkouts' own branches, index and working tree are untouched.
	for _, r := range []Repo{alice, bob} {
		if status := git(t, r.Dir, "status", "--porcelain"); status != "" {
			t.Errorf("%s has changes: %q", r.Dir, status)
		}
	}
}

// A push that loses the race to another one is retried on top of it.
func TestSaveRetriesOnTopOfAConcurrentPush(t *testing.T) {
	origin := bareRepo(t)
	alice, bob := clone(t, origin), clone(t, origin)
	first := cacheDir(t, map[string]string{"example.com/m/a/FuzzA/1": "one"})
	if _, err := alice.Save(t.Context(), first, []Target{fuzzA}, "alice"); err != nil {
		t.Fatal(err)
	}

	// Bob's commit is ready to push; alice's pre-push hook pushes it first,
	// once, so her push is rejected.
	if _, err := bob.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	bobs, _, err := bob.commit(t.Context(), cacheDir(t, map[string]string{"example.com/m/a/FuzzB/2": "two"}), []Target{fuzzB}, "bob")
	if err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(t.TempDir(), "raced")
	hook := "#!/bin/sh\nif [ ! -e '" + mark + "' ]; then\n  touch '" + mark + "'\n" +
		"  unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE\n" +
		"  git -C '" + bob.Dir + "' push -q origin " + bobs + ":refs/heads/" + Branch + "\nfi\n"
	hooks := filepath.Join(alice.Dir, ".git", "hooks")
	if werr := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(hook), 0o700); werr != nil { //nolint:gosec // a hook must be executable
		t.Fatal(werr)
	}

	n, err := alice.Save(t.Context(), cacheDir(t, map[string]string{"example.com/m/a/FuzzA/3": "three"}), []Target{fuzzA}, "alice")
	if err != nil || n != 1 {
		t.Fatalf("Save() = %d, %v; want 1", n, err)
	}
	if _, serr := os.Stat(mark); serr != nil {
		t.Fatal("the hook never raced alice's push")
	}
	if got, want := branchFiles(t, origin), []string{"a/FuzzA/1", "a/FuzzA/3", "a/FuzzB/2"}; !slices.Equal(got, want) {
		t.Errorf("branch holds %q, want %q", got, want)
	}
	if parent := git(t, origin, "rev-parse", Branch+"^"); parent != bobs {
		t.Errorf("alice's commit sits on %s, want bob's %s", parent, bobs)
	}
}

func TestErrors(t *testing.T) {
	origin := bareRepo(t)
	r := clone(t, origin)
	missing := Repo{Dir: r.Dir, Remote: "upstream"}

	if ok, err := r.HasRemote(t.Context()); err != nil || !ok {
		t.Errorf("HasRemote(origin) = %v, %v; want true", ok, err)
	}
	if ok, err := missing.HasRemote(t.Context()); err != nil || ok {
		t.Errorf("HasRemote(upstream) = %v, %v; want false", ok, err)
	}
	if _, err := missing.Fetch(t.Context()); err == nil {
		t.Error("Fetch() from a missing remote succeeded")
	}
	if _, err := r.Restore(t.Context(), t.TempDir(), []Target{fuzzA}); err == nil || !strings.Contains(err.Error(), "git ls-tree") {
		t.Errorf("Restore() before Fetch() = %v, want git ls-tree's error", err)
	}

	// A push the remote refuses every time gives up with advice.
	if err := os.WriteFile(filepath.Join(r.Dir, ".git", "hooks", "pre-push"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { //nolint:gosec // a hook must be executable
		t.Fatal(err)
	}
	_, err := r.Save(t.Context(), cacheDir(t, map[string]string{"example.com/m/a/FuzzA/1": "one"}), []Target{fuzzA}, "test")
	if err == nil || !strings.Contains(err.Error(), "can't push the corpus to origin/fuzz-corpus after 5 attempts") {
		t.Errorf("Save() with a refused push = %v", err)
	}
}

// bareRepo creates the repository the clones push to.
func bareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--bare")
	return dir
}

// clone creates a checkout whose origin is the bare repository at origin,
// with a commit of its own on its main branch.
func clone(t *testing.T, origin string) Repo {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	git(t, dir, "remote", "add", "origin", origin)
	return Repo{Dir: dir, Remote: "origin"}
}

// cacheDir creates a directory laid out like go test's fuzz cache.
func cacheDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// branchFiles lists the files on the corpus branch of the repository at
// dir, sorted.
func branchFiles(t *testing.T, dir string) []string {
	t.Helper()
	files := strings.Fields(git(t, dir, "ls-tree", "-r", "--name-only", Branch))
	slices.Sort(files)
	return files
}

// git runs git in dir and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
