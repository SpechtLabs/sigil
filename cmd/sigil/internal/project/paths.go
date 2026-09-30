package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// walker is one walk of a directory tree.
type walker struct {
	match func(name string) bool
	seen  map[string]bool // the directories read, by their real paths
}

// Expand turns the command line's paths into the files a command reads,
// by the rules every command shares: a file counts as named, whatever its
// name; "-" stays as it is, for stdin; and a directory contributes every
// file below it that match accepts, however deep. Entries whose names
// start with `.` are skipped, and what's a file is decided with os.Stat,
// which follows symbolic links, so the keys of a mounted ConfigMap, each
// a link into `..data`, are read once. Each directory's files come in
// sorted order, before its subdirectories', and a file named twice, or
// two ways, comes once, named by its cleaned, slash-separated path. Only
// a path that can't be read is an error.
func Expand(paths []string, match func(name string) bool) ([]string, humane.Error) {
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
		files, herr := walk(p, match)
		if herr != nil {
			return nil, herr
		}
		for _, f := range files {
			add(f)
		}
	}
	return out, nil
}

// IsSigil reports whether name is a `.sigil` file, the files a directory
// contributes to a bundle.
func IsSigil(name string) bool { return strings.HasSuffix(name, ".sigil") }

// walk lists the files below dir that match accepts.
func walk(dir string, match func(name string) bool) ([]string, humane.Error) {
	w := &walker{match: match, seen: map[string]bool{}}
	return w.walk(dir)
}

// walk lists the files below dir that match accepts. A directory reached
// again, through a symbolic link back up the tree, is read once. An entry
// that can't be stat'ed, such as a dangling link, matters only when match
// would take it: then it's an error that names the link.
func (w *walker) walk(dir string) ([]string, humane.Error) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if w.seen[real] {
			return nil, nil
		}
		w.seen[real] = true
	}
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
		switch {
		case err != nil && !e.IsDir() && !w.match(p):
			continue
		case err != nil && e.Type()&fs.ModeSymlink != 0:
			return nil, humane.Wrap(err, p+" is a symbolic link to nothing that can be read", "point the link at a file, or remove it")
		case err != nil:
			return nil, humane.Wrap(err, p+" can't be read", "check the entry's permissions")
		case info.IsDir():
			dirs = append(dirs, p)
		case w.match(p):
			files = append(files, clean(p))
		}
	}
	sort.Strings(files)
	for _, d := range dirs {
		below, err := w.walk(d)
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
