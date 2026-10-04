// Package corpus keeps the fuzzing corpus, the inputs go test -fuzz found
// interesting, on a git branch of its own, so a run anywhere starts from
// what every earlier run found instead of from the seeds.
//
// go test keeps the corpus in its cache, under
// $(go env GOCACHE)/fuzz/<import path>/<target>/, a file per input named
// by its content's hash. The branch [Branch] holds the same files under the
// package's directory in the module, <dir>/<target>/<hash>, and nothing
// else: it shares no history with the code. [Repo.Restore] copies a
// target's inputs from the branch into the cache, and [Repo.Save] commits
// the inputs the branch lacks and pushes them. Inputs are only ever added,
// and their names are their hashes, so corpora from any number of runs
// merge without conflicts.
//
// Both work through git's plumbing on the repository's object database and
// a temporary index, so they never touch the working tree, the index or
// the checked-out branch.
package corpus

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
)

// Branch is the branch the corpus lives on.
const Branch = "fuzz-corpus"

// pushAttempts is how often Save builds its commit on the remote's latest
// branch and pushes it, when another push keeps getting there first.
const pushAttempts = 5

// Target is a fuzz target whose corpus moves between the cache and the
// branch.
type Target struct {
	Package string // the import path, under which go test caches the corpus
	Dir     string // the package's directory in the module, slash-separated; "" for its root
	Name    string // the function's name, e.g. FuzzParse
}

// cachePath is where go test keeps the target's corpus, relative to its
// fuzz cache directory.
func (t Target) cachePath() string { return filepath.Join(filepath.FromSlash(t.Package), t.Name) }

// branchPath is where the branch keeps the target's corpus.
func (t Target) branchPath() string { return path.Join(t.Dir, t.Name) }

// Repo is a git checkout and the remote whose [Branch] holds the corpus.
type Repo struct {
	Dir    string // any directory in the checkout
	Remote string // e.g. origin
}

// Ref is the remote-tracking ref [Repo.Fetch] updates, e.g.
// refs/remotes/origin/fuzz-corpus.
func (r Repo) Ref() string { return "refs/remotes/" + r.Remote + "/" + Branch }

// Display names the remote's branch the way git does, e.g.
// origin/fuzz-corpus.
func (r Repo) Display() string { return r.Remote + "/" + Branch }

// HasRemote reports whether the checkout has a remote of r's name.
func (r Repo) HasRemote(ctx context.Context) (bool, humane.Error) {
	out, err := r.git(ctx, nil, nil, "remote")
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Fields(out), r.Remote), nil
}

// Fetch updates the remote-tracking ref from the remote's branch, and
// reports whether the remote has one.
func (r Repo) Fetch(ctx context.Context) (bool, humane.Error) {
	heads, err := r.git(ctx, nil, nil, "ls-remote", "--heads", r.Remote, "refs/heads/"+Branch)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(heads) == "" {
		return false, nil
	}
	if _, err = r.git(ctx, nil, nil, "fetch", "--quiet", "--no-tags", r.Remote, "+refs/heads/"+Branch+":"+r.Ref()); err != nil {
		return false, err
	}
	return true, nil
}

// Restore copies the corpus of targets from the fetched branch into cache,
// go test's fuzz cache directory, and returns how many inputs it copied.
// Inputs the cache has already are left alone. Call [Repo.Fetch] first.
func (r Repo) Restore(ctx context.Context, cache string, targets []Target) (int, humane.Error) {
	tree, err := r.tree(ctx)
	if err != nil {
		return 0, err
	}
	var ids, dests []string
	for _, t := range targets {
		for _, e := range tree[t.branchPath()] {
			dest := filepath.Join(cache, t.cachePath(), e.name)
			if _, serr := os.Stat(dest); serr == nil {
				continue
			}
			ids, dests = append(ids, e.id), append(dests, dest)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}

	out, err := r.git(ctx, nil, strings.NewReader(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return 0, err
	}
	blobs := bufio.NewReader(strings.NewReader(out))
	for i, dest := range dests {
		content, berr := readBlob(blobs, ids[i])
		if berr != nil {
			return i, humane.Wrap(berr, "can't read the corpus from "+r.Ref(), "fetch it again with devtool fuzz corpus pull")
		}
		if werr := writeInput(dest, content); werr != nil {
			return i, werr
		}
	}
	return len(dests), nil
}

// Save commits the inputs of targets in src, a directory laid out like go
// test's fuzz cache, that the remote's branch doesn't have yet, and pushes
// them as one commit on top of it. It creates the branch when the remote
// has none, and pushes nothing when there's nothing new. from says in the
// commit message where the inputs come from. It returns how many inputs
// it pushed.
//
// When another push gets there first, Save fetches the branch again and
// retries on top of it; inputs only ever add up, so that can't conflict.
func (r Repo) Save(ctx context.Context, src string, targets []Target, from string) (int, humane.Error) {
	var last humane.Error
	for range pushAttempts {
		commit, n, err := r.commit(ctx, src, targets, from)
		if err != nil || n == 0 {
			return 0, err
		}
		if _, last = r.git(ctx, nil, nil, "push", "--quiet", r.Remote, commit+":refs/heads/"+Branch); last == nil {
			// Keep the tracking ref on what was pushed, as git push would.
			_, _ = r.git(ctx, nil, nil, "update-ref", r.Ref(), commit)
			return n, nil
		}
	}
	return 0, humane.Wrap(last, fmt.Sprintf("can't push the corpus to %s after %d attempts", r.Display(), pushAttempts),
		"check that you may push to "+r.Remote+", then run devtool fuzz corpus push again")
}

// commit fetches the branch and commits the inputs it lacks on top of it,
// without pushing. It returns the commit and how many inputs it adds;
// there's no commit when that's none.
func (r Repo) commit(ctx context.Context, src string, targets []Target, from string) (string, int, humane.Error) {
	found, err := r.Fetch(ctx)
	if err != nil {
		return "", 0, err
	}
	tree := map[string][]entry{}
	if found {
		if tree, err = r.tree(ctx); err != nil {
			return "", 0, err
		}
	}

	var files, paths []string
	withNew := 0
	for _, t := range targets {
		have := map[string]bool{}
		for _, e := range tree[t.branchPath()] {
			have[e.name] = true
		}
		names, lerr := inputs(filepath.Join(src, t.cachePath()))
		if lerr != nil {
			return "", 0, lerr
		}
		before := len(files)
		for _, name := range names {
			if !have[name] {
				files = append(files, filepath.Join(src, t.cachePath(), name))
				paths = append(paths, path.Join(t.branchPath(), name))
			}
		}
		if len(files) > before {
			withNew++
		}
	}
	if len(files) == 0 {
		return "", 0, nil
	}

	ids, err := r.git(ctx, nil, strings.NewReader(strings.Join(files, "\n")+"\n"), "hash-object", "-w", "--stdin-paths")
	if err != nil {
		return "", 0, err
	}
	var index strings.Builder
	for i, id := range strings.Fields(ids) {
		index.WriteString("100644 " + id + "\t" + paths[i] + "\n")
	}

	tmp, terr := os.MkdirTemp("", "devtool-corpus-")
	if terr != nil {
		return "", 0, humane.Wrap(terr, "can't create a temporary index", "check that the temporary directory is writable")
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
	if found {
		if _, err = r.git(ctx, env, nil, "read-tree", r.Ref()); err != nil {
			return "", 0, err
		}
	}
	if _, err = r.git(ctx, env, strings.NewReader(index.String()), "update-index", "--add", "--index-info"); err != nil {
		return "", 0, err
	}
	treeID, err := r.git(ctx, env, nil, "write-tree")
	if err != nil {
		return "", 0, err
	}

	msg := fmt.Sprintf("Add %d %s to %d fuzz %s\n\nFrom %s.\n",
		len(files), plural(len(files), "input", "inputs"), withNew, plural(withNew, "target", "targets"), from)
	args := []string{"commit-tree", strings.TrimSpace(treeID), "-m", msg}
	if found {
		args = append(args, "-p", r.Ref())
	}
	commit, err := r.git(ctx, nil, nil, args...)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(commit), len(files), nil
}

// entry is an input on the branch: its file name and its blob.
type entry struct {
	name string
	id   string
}

// tree lists the fetched branch's inputs by the directory they're in,
// which is a target's [Target.branchPath].
func (r Repo) tree(ctx context.Context) (map[string][]entry, humane.Error) {
	out, err := r.git(ctx, nil, nil, "ls-tree", "-r", "-z", "--full-tree", r.Ref())
	if err != nil {
		return nil, err
	}
	tree := map[string][]entry{}
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\x00"), "\x00") {
		// <mode> SP <type> SP <object> TAB <path>
		meta, file, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		dir, name := path.Split(file)
		tree[strings.TrimSuffix(dir, "/")] = append(tree[strings.TrimSuffix(dir, "/")], entry{name: name, id: fields[2]})
	}
	return tree, nil
}

// git runs git in the checkout. env replaces the environment when it isn't
// nil.
func (r Repo) git(ctx context.Context, env []string, stdin io.Reader, args ...string) (string, humane.Error) {
	return gotool.Pipe(ctx, r.Dir, env, stdin, "git", args...)
}

// inputs lists the input files in a target's cache directory, which is
// missing when the target has none.
func inputs(dir string) ([]string, humane.Error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, humane.Wrap(err, "can't list the corpus in "+dir, "check the directory's permissions")
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// readBlob reads the next object `git cat-file --batch` printed, which
// must be the blob id.
func readBlob(r *bufio.Reader, id string) ([]byte, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != id || fields[1] != "blob" {
		return nil, fmt.Errorf("git cat-file printed %q for object %s", strings.TrimSpace(header), id)
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	content := make([]byte, size+1) // and the newline after it
	if _, err = io.ReadFull(r, content); err != nil {
		return nil, err
	}
	return bytes.Clone(content[:size]), nil
}

// writeInput writes an input to the cache, through a temporary file, so
// go test never reads half of one.
func writeInput(dest string, content []byte) humane.Error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return humane.Wrap(err, "can't create "+filepath.Dir(dest), "check that the Go cache is writable")
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return humane.Wrap(err, "can't write "+tmp, "check that the Go cache is writable")
	}
	if err := os.Rename(tmp, dest); err != nil {
		return humane.Wrap(err, "can't write "+dest, "check that the Go cache is writable")
	}
	return nil
}

// plural returns one when n is 1, and many otherwise.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
