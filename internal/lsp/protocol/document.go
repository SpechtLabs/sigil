package protocol

// DidOpenTextDocumentParams is a textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams is a textDocument/didChange: the document's
// new version, and the changes that made it, in order.
type DidChangeTextDocumentParams struct {
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
}

// TextDocumentContentChangeEvent is one change: the whole new text when
// Range is nil, and otherwise the text that replaces Range.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DidCloseTextDocumentParams is a textDocument/didClose.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DidSaveTextDocumentParams is a textDocument/didSave.
type DidSaveTextDocumentParams struct {
	Text         *string                `json:"text,omitempty"`
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DidChangeWatchedFilesParams is a workspace/didChangeWatchedFiles. The
// server reloads on any change, so the events aren't read.
type DidChangeWatchedFilesParams struct{}

// DiagnosticSeverity is how serious a diagnostic is.
type DiagnosticSeverity int

// The severities `sigil check` reports.
const (
	SeverityError   DiagnosticSeverity = 1
	SeverityWarning DiagnosticSeverity = 2
)

// Diagnostic is one problem in a document.
type Diagnostic struct {
	Code     string             `json:"code,omitempty"` // the lint's name, for a lint finding
	Source   string             `json:"source"`
	Message  string             `json:"message"`
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity"`
}

// PublishDiagnosticsParams replaces every diagnostic of a document.
type PublishDiagnosticsParams struct {
	Version     *int32       `json:"version,omitempty"` // the version the diagnostics are for, when the document is open
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// DocumentFormattingParams is a textDocument/formatting. The formatting
// options are ignored: `sigil fmt` has one style.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// RegistrationParams is a client/registerCapability.
type RegistrationParams struct {
	Registrations []Registration `json:"registrations"`
}

// Registration registers one capability at run time.
type Registration struct {
	RegisterOptions any    `json:"registerOptions,omitempty"`
	ID              string `json:"id"`
	Method          string `json:"method"`
}

// DidChangeWatchedFilesRegistrationOptions says which files the client
// watches for the server.
type DidChangeWatchedFilesRegistrationOptions struct {
	Watchers []FileSystemWatcher `json:"watchers"`
}

// FileSystemWatcher is one glob pattern of files to watch, for every kind
// of change.
type FileSystemWatcher struct {
	GlobPattern string `json:"globPattern"`
}
