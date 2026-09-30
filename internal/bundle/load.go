package bundle

import (
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// Load reads every `.sigil` file in fsys into the bundle, in every
// directory, skipping entries whose names start with `.`. What's a file
// is decided with fs.Stat, which follows symbolic links, so the keys of
// a mounted ConfigMap, each a link into `..data`, load once.
//
// Files are added in sorted path order, named by their path in fsys. The
// error reports only an entry that can't be read; parse errors stay in
// the bundle, as [Bundle.Add] leaves them.
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
