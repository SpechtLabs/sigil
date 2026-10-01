package engine

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/workspace"
)

// load builds the project of the files and the trusted files. A file at
// or below one of the trusted paths is read as trusted, as the CLI reads
// a file below a trusted: path, and so is every trusted file, whatever
// its path, as policy.From reads its source. Every file is named by its
// cleaned path, which is how the CLI names a file it reads by a relative
// path. A file given twice is read once, as the CLI reads a file it's
// given twice once; a path given twice with two sources is an error,
// since they can't both be the file, and so is a path among both the
// files and the trusted files, so a file can't claim to be trusted by
// taking a trusted file's path. A request without files is an error:
// there's nothing to check, compile or explain.
func load(files []File, trustedPaths []string, trustedFiles []File) (*workspace.Project, humane.Error) {
	if len(files) == 0 {
		return nil, humane.New("the request holds no files", `send the kind and the policies as "files": [{"path": "...", "source": "..."}]`)
	}
	return project(files, trustedPaths, trustedFiles)
}

// project builds the project of the files and the trusted files, as load
// does, of none too: the test op, like sigil test, runs its test files
// against whatever the files define, and a test file whose policy isn't
// among them is a result that says so.
func project(files []File, trustedPaths []string, trustedFiles []File) (*workspace.Project, humane.Error) {
	r := reading{seen: map[string]read{}}
	for i, f := range files {
		if err := r.add(f, "file", i, below(path.Clean(f.Path), trustedPaths)); err != nil {
			return nil, err
		}
	}
	for i, f := range trustedFiles {
		if err := r.add(f, "trusted file", i, true); err != nil {
			return nil, err
		}
	}
	return workspace.NewLoader(nil).Load(r.paths, r.trusted), nil
}

// reading is the files of one load, so far.
type reading struct {
	seen    map[string]read // by path
	paths   []workspace.File
	trusted []workspace.File
}

// read is how a path was given.
type read struct {
	source  string
	trusted bool
}

// add adds the file at index i of its list, what, to the paths or the
// trusted paths.
func (r *reading) add(f File, what string, i int, trusted bool) humane.Error {
	if f.Path == "" {
		return humane.New(fmt.Sprintf("%s %d has no path", what, i+1), "give every file a path, such as access/main.sigil; diagnostics name files by it")
	}
	name := path.Clean(f.Path)
	if prev, ok := r.seen[name]; ok {
		switch {
		case prev.trusted != trusted:
			return humane.New(name+" is among both the files and the trusted files", "a trusted file's path belongs to it; give the other file another path")
		case prev.source != f.Source:
			return humane.New(name+" is given twice, with two sources", "send each file once")
		}
		return nil
	}
	r.seen[name] = read{source: f.Source, trusted: trusted}
	wf := workspace.File{Name: name, Source: []byte(f.Source)}
	if trusted {
		r.trusted = append(r.trusted, wf)
	} else {
		r.paths = append(r.paths, wf)
	}
	return nil
}

// trustedPaths returns the trusted paths of the requirements, each once,
// and checks that each holds a file, as the CLI checks that a trusted
// path of the configuration file exists.
func trustedPaths(files []File, reqs []Requirement) ([]string, humane.Error) {
	var out []string
	for i, r := range reqs {
		for _, t := range r.Trusted {
			t = path.Clean(t)
			if slices.Contains(out, t) {
				continue
			}
			held := false
			for _, f := range files {
				held = held || below(path.Clean(f.Path), []string{t})
			}
			if !held {
				return nil, humane.New(fmt.Sprintf("%s: the trusted path %s of %s holds no file", where(i), t, r.Policy), "name a file of the request, or a directory some of them are below")
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// below reports whether the file named name is one of dirs or below one
// of them.
func below(name string, dirs []string) bool {
	for _, d := range dirs {
		if d == "." || name == d || strings.HasPrefix(name, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}

// where names the requirement at index i of a request, for messages.
func where(i int) string { return fmt.Sprintf("require[%d]", i) }

// cleaned returns the paths, each cleaned as a file's path is.
func cleaned(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = path.Clean(p)
	}
	return out
}
