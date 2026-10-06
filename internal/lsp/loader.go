package lsp

import (
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// Loader finds and loads the projects the open documents belong to. The
// sigil CLI's loader reads a project the way `sigil check` does, from the
// nearest configuration file; tests load from memory. The server calls
// Root on its message loop and Load on a goroutine of its own, one load at
// a time per project, so a Loader must be safe for concurrent use.
type Loader interface {
	// Root returns the root of the project the file at path belongs to.
	// folders are the client's workspace folders, as absolute paths.
	Root(path string, folders []string) Root
	// Load reads and checks the project at root. overlay holds the text
	// of every open document by its absolute path, which replaces the
	// file on disk; the server doesn't change it afterwards. Every file
	// name in the result must be the file's absolute path,
	// slash-separated, so the server can map it to a URI.
	Load(root string, overlay map[string][]byte) *Snapshot
}

// Root is where a document's project is, and how the server reports on
// it.
type Root struct {
	Path string // the directory or file the project is read from
	// Note tells the user how the root was chosen when that may
	// surprise them, such as a workspace folder too large to read
	// whole; the server shows each note once.
	Note string
	// Declared says a configuration file declares the project, so the
	// server publishes the diagnostics of every file in it, open or not.
	// Without one, it publishes only those of the open documents.
	Declared bool
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
