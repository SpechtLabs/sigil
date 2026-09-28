package bundle

import (
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// Load reads every `.sigil` file in fsys into the bundle, in every
// directory, skipping entries whose names start with `.`. What's a file
// is decided with fs.Stat, which follows symbolic links, so the keys of
// a mounted ConfigMap, each a link into `..data`, load once.
func (b *Bundle) Load(fsys fs.FS) humane.Error {
	files, err := walkFS(fsys, ".")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		src, err := fs.ReadFile(fsys, f)
		if err != nil {
			return humane.Wrap(err, "policy file "+f+" couldn't be read", "check the file's permissions")
		}
		b.Add(f, src)
	}
	return nil
}

// walkFS collects the `.sigil` files under dir.
func walkFS(fsys fs.FS, dir string) ([]string, humane.Error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, humane.Wrap(err, "directory "+dir+" of the policy bundle couldn't be read", "check that the fs.FS is readable; every `.sigil` file in it is loaded")
	}
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := path.Join(dir, e.Name())
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return nil, humane.Wrap(err, p+" in the policy bundle can't be read", "a symbolic link there may be dangling")
		}
		switch {
		case info.IsDir():
			below, err := walkFS(fsys, p)
			if err != nil {
				return nil, err
			}
			files = append(files, below...)
		case strings.HasSuffix(e.Name(), ".sigil"):
			files = append(files, p)
		}
	}
	return files, nil
}

// LoadPaths reads the command line's inputs: each path is a file, a
// directory, whose `.sigil` files are read (every one below it with
// recursive), or "-" for stdin. Entries whose names start with `.` are
// skipped, as Load does.
func (b *Bundle) LoadPaths(paths []string, recursive bool, stdin io.Reader) humane.Error {
	for _, p := range paths {
		if p == "-" {
			src, err := io.ReadAll(stdin)
			if err != nil {
				return humane.Wrap(err, "stdin couldn't be read", "pipe a policy bundle in, or name files instead of `-`")
			}
			b.Add("<stdin>", src)
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return humane.Wrap(err, p+" can't be read", "name a `.sigil` file, a directory or `-` for stdin")
		}
		if info.IsDir() {
			if err := b.addDir(p, recursive); err != nil {
				return err
			}
			continue
		}
		if err := b.addFile(p); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bundle) addFile(p string) humane.Error {
	src, err := os.ReadFile(p) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return humane.Wrap(err, p+" couldn't be read", "check the file's permissions")
	}
	b.Add(filepath.ToSlash(p), src)
	return nil
}

// addDir reads the `.sigil` files directly in dir, and below it when
// recursive is set.
func (b *Bundle) addDir(dir string, recursive bool) humane.Error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return humane.Wrap(err, dir+" couldn't be read", "check the directory's permissions")
	}
	var files, dirs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := os.Stat(p)
		if err != nil {
			return humane.Wrap(err, p+" can't be read", "check the entry's permissions")
		}
		switch {
		case info.IsDir() && recursive:
			dirs = append(dirs, p)
		case !info.IsDir() && strings.HasSuffix(e.Name(), ".sigil"):
			files = append(files, p)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		if err := b.addFile(f); err != nil {
			return err
		}
	}
	for _, d := range dirs {
		if err := b.addDir(d, true); err != nil {
			return err
		}
	}
	return nil
}
