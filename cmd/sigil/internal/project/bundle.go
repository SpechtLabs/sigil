package project

import (
	"github.com/spechtlabs/sigil/internal/payload"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// FromBundle returns the files of a bundle compiled into the binary, for
// [LoadFiles]. Each file's ID is its name: [Read] gives a file named
// several ways one name, so two files of the bundle with the same name
// are the same file, as they were when it was compiled.
func FromBundle(b *payload.Bundle) *Files {
	return &Files{Kinds: fromPayload(b.Kinds), Paths: fromPayload(b.Paths), Trusted: fromPayload(b.Trusted)}
}

// Bundle returns the files as a bundle to compile, the inverse of
// [FromBundle]: its Kinds, Paths and Trusted, each file by its name and
// contents. The caller sets the root and the requirements, and renames
// the files if it must, the same name the same way, so that files that
// were one stay one.
func (f *Files) Bundle() *payload.Bundle {
	return &payload.Bundle{Kinds: toPayload(f.Kinds), Paths: toPayload(f.Paths), Trusted: toPayload(f.Trusted)}
}

// fromPayload converts a bundle's files, each with its name for an ID.
func fromPayload(files []payload.File) []workspace.File {
	out := make([]workspace.File, len(files))
	for i, f := range files {
		out[i] = workspace.File{Name: f.Name, ID: f.Name, Source: []byte(f.Source)}
	}
	return out
}

// toPayload converts files read from disk for a bundle, which keeps
// their names and contents only.
func toPayload(files []workspace.File) []payload.File {
	out := make([]payload.File, len(files))
	for i, f := range files {
		out[i] = payload.File{Name: f.Name, Source: string(f.Source)}
	}
	return out
}
