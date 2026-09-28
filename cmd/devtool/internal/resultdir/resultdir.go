// Package resultdir manages the directory a devtool run writes its results
// to: its raw output, a Markdown summary CI adds to the job page, and the
// run's metadata. Every command writes the same kinds of files there.
package resultdir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// The files every command writes.
const (
	Summary  = "summary.md"
	Metadata = "metadata.json"
)

// Dir is a results directory.
type Dir struct {
	path string
}

// Reset creates dir, relative to root unless it's absolute, and removes
// the files an earlier run left there, so a new run never appends to them.
// files are the command's own, besides Summary and Metadata.
func Reset(root, dir string, files ...string) (Dir, humane.Error) {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Dir{}, humane.Wrap(err, "can't create the results directory "+dir, "pass a writable --results")
	}
	for _, name := range append([]string{Summary, Metadata}, files...) {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return Dir{}, humane.Wrap(err, "can't remove the earlier result "+name, "delete "+dir+" by hand")
		}
	}
	return Dir{path: dir}, nil
}

// Path returns the path of a file in the directory.
func (d Dir) Path(name string) string {
	return filepath.Join(d.path, name)
}

// Display returns the directory, or a file in it, the way to print it:
// relative to the working directory when it's below it.
func (d Dir) Display(name ...string) string {
	return Display(filepath.Join(append([]string{d.path}, name...)...))
}

// Write replaces a file's content.
func (d Dir) Write(name, content string) humane.Error {
	if err := os.WriteFile(d.Path(name), []byte(content), 0o600); err != nil {
		return humane.Wrap(err, "can't write "+d.Path(name), "check that --results is writable")
	}
	return nil
}

// WriteJSON replaces a file's content with v as indented JSON.
func (d Dir) WriteJSON(name string, v any) humane.Error { //nolint:emptyinterface // encodes whatever it's given
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return humane.Wrap(err, "can't encode "+name, "this is a bug in devtool")
	}
	return d.Write(name, string(b)+"\n")
}

// Append adds content to the end of a file, creating it.
func (d Dir) Append(name, content string) humane.Error {
	f, err := d.Open(name)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(content)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return humane.Wrap(werr, "can't write "+d.Path(name), "check that --results is writable")
	}
	return nil
}

// Open opens a file for appending, creating it. The caller closes it.
func (d Dir) Open(name string) (*os.File, humane.Error) {
	f, err := os.OpenFile(d.Path(name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:resourceclose // Open returns the file for the caller to close
	if err != nil {
		return nil, humane.Wrap(err, "can't open "+d.Path(name), "check that --results is writable")
	}
	return f, nil
}

// Display returns path relative to the working directory when it's below
// it, so the paths devtool prints are short and clickable.
func Display(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
