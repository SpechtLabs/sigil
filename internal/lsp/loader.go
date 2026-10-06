package lsp

import (
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// Loader finds and loads the projects the open documents belong to. The
// sigil CLI's loader reads a project the way `sigil check` does, from the
// nearest configuration file; tests load from memory. The server calls it
// from one goroutine at a time.
type Loader interface {
	// Root returns the root of the project the file at path belongs to:
	// the directory or file the server loads it from. folders are the
	// client's workspace folders, as absolute paths.
	Root(path string, folders []string) string
	// Load reads and checks the project at root. overlay holds the text
	// of every open document by its absolute path, which replaces the
	// file on disk. Every file name in the result must be the file's
	// absolute path, slash-separated, so the server can map it to a URI.
	Load(root string, overlay map[string][]byte) *Snapshot
}

// Snapshot is one load of a project: what the server answers from until
// the next one.
type Snapshot struct {
	Project *workspace.Project // the documents, loaded and checked; nil when the project couldn't be read at all
	// Err is what stopped the check, such as a configuration file that
	// doesn't parse or a requirement that can't be enforced. The server
	// shows it to the user; the project, when there is one, still answers
	// completion, hover and definition.
	Err         error
	Diagnostics diag.ErrorList // what `sigil check` reports, unresolved
}
