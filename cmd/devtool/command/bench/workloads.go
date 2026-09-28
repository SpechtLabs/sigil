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

// benchSuffix names the files a comparison copies onto the base revision.
const benchSuffix = "_bench_test.go"

// base is the exported base revision.
type base struct {
	sha string
	dir string
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
// checkout's workloads and fixtures on it.
func prepareBase(ctx context.Context, root, tmp, baseline, fixtures string, files []string) (base, humane.Error) {
	sha, err := gotool.Output(ctx, root, nil, "git", "rev-parse", "--verify", baseline+"^{commit}")
	if err != nil {
		return base{}, err
	}
	b := base{sha: strings.TrimSpace(sha), dir: filepath.Join(tmp, sideBase)}
	if err := snapshot(ctx, root, b.sha, b.dir); err != nil {
		return base{}, err
	}
	if err := installWorkloads(root, b.dir, files, fixtures); err != nil {
		return base{}, err
	}
	return b, nil
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
func installWorkloads(head, base string, files []string, fixtures string) humane.Error {
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
		if err := copyFile(filepath.Join(head, rel), filepath.Join(base, rel)); err != nil {
			return err
		}
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
