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
// Files are added in sorted path order, named by their path in fsys. A
// bundle may load several sources, as a trusted bundle does, but a path
// one of them holds can't be in another: a diagnostic names a file by
// its path alone. The error reports such a path, or an entry that can't
// be read; parse errors stay in the bundle, as [Bundle.Add] leaves them.
func (b *Bundle) Load(fsys fs.FS) humane.Error {
	files, err := walkFS(fsys, ".")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		if _, dup := b.Sources[f]; dup {
			return humane.New("policy file "+f+" is in two of the sources read into one bundle",
				"a diagnostic names a file by its path, so two trusted sources can't hold the same one; rename or move one of the two files")
		}
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
