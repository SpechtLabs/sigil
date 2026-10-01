package engine

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/testrun"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

// tested is the test op's response: the records `sigil test -o json`
// prints.
type tested struct {
	envelope
	Results []testrun.SuiteResult `json:"results"`
}

// dataFS serves a request's data files, by cleaned path, as input files
// are read from disk: a path that climbs out of the test file's directory,
// such as ../shared/owner.json, is one more name. It serves the names
// testsuite.Suite.ReadInput joins, a test file's directory and a case's
// input_file, and nothing else: it isn't a general fs.FS, with no
// directories to list or walk.
type dataFS map[string][]byte

// dataFile is an open data file.
type dataFile struct {
	*bytes.Reader
	name string
	size int64
}

// test answers the test op: the test files run against the files and
// the trusted files, which load as explain's do, as `sigil test -o json`
// prints them for the same relative paths, in the same order. A test
// file that can't run is a result whose error says why, and a case that
// fails is a result that didn't pass: only a request the CLI's command
// line couldn't have given fails the op.
func (e *Engine) test(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	var filter *regexp.Regexp
	if r.Run != "" {
		re, err := regexp.Compile(r.Run)
		if err != nil {
			return nil, humane.New("run isn't a valid regular expression: "+err.Error(), "run takes a Go regular expression matched against case names")
		}
		filter = re
	}
	// The .sigil files' paths are taken too: on disk, a path is one file.
	seen := map[string]string{}
	for _, f := range slices.Concat(r.Files, r.Trusted) {
		if _, ok := seen[path.Clean(f.Path)]; !ok && f.Path != "" {
			seen[path.Clean(f.Path)] = f.Source
		}
	}
	tests, err := virtual(seen, r.TestFiles, "test file")
	if err != nil {
		return nil, err
	}
	if len(tests) == 0 {
		return nil, humane.New("the request holds no test files", `send the test files as "test_files": [{"path": "checkout/alerts_test.yaml", "source": "..."}]`)
	}
	for _, f := range tests {
		if !testsuite.IsTestFile(f.Path) {
			return nil, humane.New("the test file "+f.Path+" isn't named like one", "a test file's name ends in "+strings.Join(testsuite.Suffixes, " or ")+"; send other files a case reads as data_files")
		}
	}
	data, err := virtual(seen, r.DataFiles, "data file")
	if err != nil {
		return nil, err
	}
	inputs := make(dataFS, len(data))
	for _, f := range data {
		inputs[f.Path] = []byte(f.Source)
	}
	p, err := project(r.Files, nil, r.Trusted)
	if err != nil {
		return nil, err
	}
	p.Check()
	// The CLI runs its test files in sorted order.
	slices.SortFunc(tests, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	results := make([]testrun.SuiteResult, len(tests))
	for i, f := range tests {
		results[i] = testrun.Run(context.Background(), p, f.Path, []byte(f.Source), inputs, filter)
	}
	return tested{envelope: env, Results: results}, nil
}

// Open implements [fs.FS]: it opens the data file of that path.
func (d dataFS) Open(name string) (fs.File, error) { //nolint:humaneerror // fs.FS fixes the signature
	src, ok := d[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &dataFile{Reader: bytes.NewReader(src), name: path.Base(name), size: int64(len(src))}, nil
}

// Stat implements [fs.File]. The file is its own [fs.FileInfo].
func (f *dataFile) Stat() (fs.FileInfo, error) { return f, nil } //nolint:humaneerror // fs.File fixes the signature

// Close implements [fs.File]. It does nothing.
func (f *dataFile) Close() error { return nil }

// Name implements [fs.FileInfo]. It returns the file's base name.
func (f *dataFile) Name() string { return f.name }

// Size implements [fs.FileInfo]. It returns the source's length.
func (f *dataFile) Size() int64 { return f.size }

// Mode implements [fs.FileInfo]. A data file is a read-only regular file.
func (f *dataFile) Mode() fs.FileMode { return 0o444 }

// ModTime implements [fs.FileInfo]. A data file has none.
func (f *dataFile) ModTime() time.Time { return time.Time{} }

// IsDir implements [fs.FileInfo]. It returns false.
func (f *dataFile) IsDir() bool { return false }

// Sys implements [fs.FileInfo]. It returns nil.
func (f *dataFile) Sys() any { return nil } //nolint:emptyinterface // fs.FileInfo fixes the signature

// virtual returns the files of one list of a request, what, each once
// and named by its cleaned path, as a file the CLI reads is. seen holds
// the source of every path given so far, across the lists: a path given
// twice with the same source is read once, and with two sources is an
// error, since they can't both be the file.
func virtual(seen map[string]string, files []File, what string) ([]File, humane.Error) {
	var out []File
	listed := map[string]bool{}
	for i, f := range files {
		if f.Path == "" {
			return nil, humane.New(fmt.Sprintf("%s %d has no path", what, i+1), "give every file a path, such as checkout/alerts_test.yaml; a case's input_file is relative to its test file's")
		}
		name := path.Clean(f.Path)
		if prev, ok := seen[name]; ok && prev != f.Source {
			return nil, humane.New(name+" is given twice, with two sources", "send each file once")
		}
		if listed[name] {
			continue
		}
		seen[name], listed[name] = f.Source, true
		out = append(out, File{Path: name, Source: f.Source})
	}
	return out, nil
}
