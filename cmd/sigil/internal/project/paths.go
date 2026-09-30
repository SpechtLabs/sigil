package project

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// expand turns the command line's paths into the files to read: a file
// as named, "-" as it is, and a directory the `.sigil` files directly
// inside it, or every one below it with recursive. Entries whose names
// start with `.` are skipped, and what's a file is decided with os.Stat,
// which follows symbolic links, so the keys of a mounted ConfigMap, each
// a link into `..data`, are read once. Each directory's files come in
// sorted order, before its subdirectories; a file named twice is read
// once.
func expand(paths []string, recursive bool) ([]string, humane.Error) {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, p := range paths {
		if p == "-" {
			add(p)
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, humane.Wrap(err, p+" can't be read", "name a `.sigil` file, a directory or `-` for stdin")
		}
		if !info.IsDir() {
			add(clean(p))
			continue
		}
		files, herr := walk(p, recursive)
		if herr != nil {
			return nil, herr
		}
		for _, f := range files {
			add(f)
		}
	}
	return out, nil
}

// walk lists the `.sigil` files directly in dir, and below it when
// recursive is set.
func walk(dir string, recursive bool) ([]string, humane.Error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, humane.Wrap(err, dir+" couldn't be read", "check the directory's permissions")
	}
	var files, dirs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		info, err := os.Stat(p)
		if err != nil {
			return nil, humane.Wrap(err, p+" can't be read", "check the entry's permissions")
		}
		switch {
		case info.IsDir() && recursive:
			dirs = append(dirs, p)
		case !info.IsDir() && strings.HasSuffix(e.Name(), ".sigil"):
			files = append(files, clean(p))
		}
	}
	sort.Strings(files)
	for _, d := range dirs {
		below, err := walk(d, true)
		if err != nil {
			return nil, err
		}
		files = append(files, below...)
	}
	return files, nil
}

// without returns the files not in drop, in order.
func without(files, drop []string) []string {
	skip := map[string]bool{}
	for _, f := range drop {
		skip[f] = true
	}
	out := files[:0:0]
	for _, f := range files {
		if !skip[f] {
			out = append(out, f)
		}
	}
	return out
}

// clean names a file the way diagnostics show it: cleaned and
// slash-separated, so one file named two ways is read once.
func clean(p string) string { return filepath.ToSlash(filepath.Clean(p)) }
