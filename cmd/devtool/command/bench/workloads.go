package bench

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
)

const (
	// benchSuffix names the files a comparison copies onto the base revision.
	benchSuffix = "_bench_test.go"
	// setupSuffix names benchmark setup that stays with its revision:
	// code that depends on an API a change may touch, such as building a
	// kind with its options. The base keeps its own copy, so a change to
	// that API doesn't break the base build, and takes the checkout's only
	// when it has none, which is when the setup file is new.
	setupSuffix = "_benchsetup_test.go"
)

// base is the exported base revision.
type base struct {
	// known holds the benchmarks the base revision declares, by package
	// directory; a package it doesn't have is missing.
	known map[string]map[string]bool
	sha   string
	// dir holds the base revision with the checkout's workloads and
	// fixtures on it.
	dir string
	// files are the workload files installed on it: those of the packages
	// the base revision has.
	files []string
}

// has reports whether the base revision has the package pkg, a directory
// in the ./dir form.
func (b base) has(pkg string) bool {
	_, ok := b.known[filepath.Clean(pkg)]
	return ok
}

// workloads selects the benchmarks filter matches in the packages patterns
// select, and returns every *_bench_test.go file of their packages,
// relative to root, and the packages' directories. Only those packages are
// built. A benchmark outside a *_bench_test.go file couldn't be copied
// onto the base revision, so it's an error.
func workloads(ctx context.Context, root string, patterns []string, filter string) (files, packages []string, err humane.Error) {
	all, err := gotool.Discover(ctx, root, patterns, "Benchmark")
	if err != nil {
		return nil, nil, err
	}
	// go test matches the part of -test.bench before the first slash
	// against the top-level benchmarks, and the rest against sub-benchmarks.
	top, _, _ := strings.Cut(filter, "/")
	re, err := gotool.CompileFilter(top)
	if err != nil {
		return nil, nil, err
	}
	selected, err := gotool.Select(all, re, patterns, names.Things)
	if err != nil {
		return nil, nil, err
	}

	packages = gotool.Packages(selected)
	for _, t := range all {
		if !slices.Contains(packages, t.Dir) {
			continue
		}
		if !strings.HasSuffix(t.File, benchSuffix) {
			return nil, nil, humane.New(t.Name+" is declared in "+t.File,
				"move benchmarks to a *"+benchSuffix+" file so the comparison can copy them onto the base revision")
		}
		files = append(files, t.File)
	}
	slices.Sort(files)
	return slices.Compact(files), packages, nil
}

// prepareBase exports the baseline revision into tmp and installs the
// checkout's workloads and fixtures on it. It first records which
// benchmarks the base revision declares, so a benchmark the checkout adds,
// which the base revision can't compile, is left out of it; see stripNew.
func prepareBase(ctx context.Context, root, tmp, baseline, fixtures string, files []string) (base, humane.Error) {
	sha, err := gotool.Output(ctx, root, nil, "git", "rev-parse", "--verify", baseline+"^{commit}")
	if err != nil {
		return base{}, err
	}
	b := base{sha: strings.TrimSpace(sha), dir: filepath.Join(tmp, sideBase)}
	if err = snapshot(ctx, root, b.sha, b.dir); err != nil {
		return base{}, err
	}
	dirs := make([]string, 0, len(files))
	for _, rel := range files {
		dirs = append(dirs, filepath.Dir(rel))
	}
	slices.Sort(dirs)
	known, err := declared(ctx, b.dir, slices.Compact(dirs))
	if err != nil {
		return base{}, err
	}
	b.known = known
	for _, rel := range files {
		if b.has(filepath.Dir(rel)) {
			b.files = append(b.files, rel)
		}
	}
	if err := installWorkloads(root, b.dir, b.files, fixtures, known); err != nil {
		return base{}, err
	}
	return b, nil
}

// withOwnFixtures exports the base revision again into dir, with the
// checkout's workloads but its own fixtures. A package whose workloads the
// checkout's fixtures can't build or run on the base revision, such as
// fixtures written in syntax the base revision doesn't parse, runs there.
func (b base) withOwnFixtures(ctx context.Context, root, dir string) humane.Error {
	if err := snapshot(ctx, root, b.sha, dir); err != nil {
		return err
	}
	return installWorkloads(root, dir, b.files, "", b.known)
}

// snapshot extracts the tree of commit sha into dir with git archive.
func snapshot(ctx context.Context, root, sha, dir string) humane.Error {
	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", sha) //nolint:gosec // sha is a resolved commit
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return humane.Wrap(err, "can't run git archive", "check that git is installed")
	}
	if err := cmd.Start(); err != nil {
		return humane.Wrap(err, "can't run git archive", "check that git is installed")
	}
	extractErr := extract(stdout, dir)
	// Drain what's left so git can exit when extraction stopped early.
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return humane.Wrap(err, "git archive "+sha+" failed:\n"+strings.TrimSpace(stderr.String()),
			"check that the base revision is in the local clone")
	}
	return extractErr
}

// extract writes the directories and regular files of a tar stream below
// dir. Git archives of this repository contain nothing else.
func extract(r io.Reader, dir string) humane.Error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return humane.Wrap(err, "can't create "+dir, "check that TMPDIR is writable")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return humane.Wrap(err, "can't open "+dir, "check that TMPDIR is writable")
	}
	defer func() { _ = root.Close() }()

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return humane.Wrap(err, "can't read the git archive", "check that the base revision exists")
		}
		switch h.Typeflag {
		case tar.TypeXGlobalHeader:
			// git archive stores the commit ID here; there's nothing to extract.
		case tar.TypeDir:
			err = root.MkdirAll(h.Name, 0o750)
		case tar.TypeReg:
			err = extractFile(root, h, tr)
		default:
			return humane.New("unsupported git archive entry "+h.Name,
				"devtool only extracts directories and regular files; compare with a revision without links")
		}
		if err != nil {
			return humane.Wrap(err, "can't extract "+h.Name, "check that TMPDIR is writable")
		}
	}
}

// extractFile writes one regular file of the archive. root confines it to
// the extraction directory, whatever its name says.
func extractFile(root *os.Root, h *tar.Header, r io.Reader) humane.Error {
	if err := root.MkdirAll(filepath.Dir(h.Name), 0o750); err != nil {
		return humane.Wrap(err, "can't create the directory for "+h.Name, "check that TMPDIR is writable")
	}
	f, err := root.OpenFile(h.Name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fs.FileMode(h.Mode)&0o777) //nolint:gosec // tar modes fit in FileMode
	if err != nil {
		return humane.Wrap(err, "can't create "+h.Name, "check that TMPDIR is writable")
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return humane.Wrap(err, "can't write "+h.Name, "check that TMPDIR is writable")
	}
	return nil
}

// installWorkloads replaces the base revision's benchmarks and fixtures with
// the checkout's, so both revisions run identical workloads. Removed or
// renamed benchmarks must not leave stale base-only workloads behind.
// Benchmark setup stays with its revision; see installSetup.
//
// known holds the benchmarks the base revision declares, by package
// directory. A benchmark it doesn't list is new and left out; see
// stripNew. With a nil known, every file is copied as it is. With no
// fixtures, the base revision keeps its own.
func installWorkloads(head, base string, files []string, fixtures string, known map[string]map[string]bool) humane.Error {
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), benchSuffix) {
			return err
		}
		return os.Remove(path) //nolint:gosec // base is the temporary export this run created
	})
	if err != nil {
		return humane.Wrap(err, "can't remove the base revision's benchmarks", "check that TMPDIR is writable")
	}

	for _, rel := range files {
		if err := installWorkload(head, base, rel, known); err != nil {
			return err
		}
	}
	if err := installSetup(head, base, files); err != nil {
		return err
	}

	if fixtures == "" {
		return nil
	}
	dst := filepath.Join(base, fixtures)
	if err := os.RemoveAll(dst); err != nil {
		return humane.Wrap(err, "can't remove the base revision's "+fixtures, "check that TMPDIR is writable")
	}
	if err := os.CopyFS(dst, os.DirFS(filepath.Join(head, fixtures))); err != nil {
		return humane.Wrap(err, "can't copy "+fixtures+" onto the base revision", "check that "+fixtures+" exists")
	}
	return nil
}

// installWorkload copies one of the checkout's workload files onto the base
// revision, without the benchmarks known says it doesn't declare.
func installWorkload(head, base, rel string, known map[string]map[string]bool) humane.Error {
	if known == nil {
		return copyFile(filepath.Join(head, rel), filepath.Join(base, rel))
	}
	src, err := os.ReadFile(filepath.Join(head, rel)) //nolint:gosec // rel is a workload go list reported
	if err != nil {
		return humane.Wrap(err, "can't copy "+rel+" onto the base revision", "check that the file exists")
	}
	out, herr := stripNew(rel, src, known[filepath.Dir(rel)])
	if herr != nil {
		return herr
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(base, rel)), 0o750); err != nil {
		return humane.Wrap(err, "can't copy "+rel+" onto the base revision", "check that TMPDIR is writable")
	}
	if err := os.WriteFile(filepath.Join(base, rel), out, 0o600); err != nil { //nolint:gosec // the destination is inside the temporary export
		return humane.Wrap(err, "can't copy "+rel+" onto the base revision", "check that TMPDIR is writable")
	}
	return nil
}

// installSetup gives the base revision the checkout's *_benchsetup_test.go
// files it lacks, in the packages whose workloads were copied. A setup
// file the base already has stays as it is, so each revision builds its
// workloads' setup with its own API.
func installSetup(head, base string, files []string) humane.Error {
	dirs := make([]string, 0, len(files))
	for _, rel := range files {
		dirs = append(dirs, filepath.Dir(rel))
	}
	slices.Sort(dirs)
	for _, dir := range slices.Compact(dirs) {
		matches, err := filepath.Glob(filepath.Join(head, dir, "*"+setupSuffix))
		if err != nil {
			return humane.Wrap(err, "can't list the benchmark setup in "+dir, "rename the directory so its path has no glob metacharacters")
		}
		for _, src := range matches {
			rel, _ := filepath.Rel(head, src) // src is inside head, since Glob built it from there
			if _, err := os.Stat(filepath.Join(base, rel)); err == nil {
				continue
			}
			if err := copyFile(src, filepath.Join(base, rel)); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) humane.Error {
	data, err := os.ReadFile(src) //nolint:gosec // src is a workload go list reported
	if err == nil {
		err = os.MkdirAll(filepath.Dir(dst), 0o750)
	}
	if err == nil {
		err = os.WriteFile(dst, data, 0o600) //nolint:gosec // dst is inside the temporary export
	}
	if err != nil {
		return humane.Wrap(err, "can't copy "+filepath.Base(src)+" onto the base revision", "check that TMPDIR is writable")
	}
	return nil
}
