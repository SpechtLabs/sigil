// Package protocol holds the Language Server Protocol types the Sigil
// language server sends and receives, and only those: the lifecycle,
// document sync, diagnostics, completion, hover, go-to-definition and
// formatting. Field names and JSON tags follow the specification at
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/,
// so a value marshals to exactly what a client expects.
package protocol

import "encoding/json"

// The methods the server handles, and the ones it sends.
const (
	MethodInitialize        = "initialize"
	MethodInitialized       = "initialized"
	MethodShutdown          = "shutdown"
	MethodExit              = "exit"
	MethodCancelRequest     = "$/cancelRequest"
	MethodDidOpen           = "textDocument/didOpen"
	MethodDidChange         = "textDocument/didChange"
	MethodDidClose          = "textDocument/didClose"
	MethodDidSave           = "textDocument/didSave"
	MethodCompletion        = "textDocument/completion"
	MethodHover             = "textDocument/hover"
	MethodDefinition        = "textDocument/definition"
	MethodFormatting        = "textDocument/formatting"
	MethodPublishDiagnostic = "textDocument/publishDiagnostics"
	MethodDidChangeWatched  = "workspace/didChangeWatchedFiles"
	MethodDidChangeFolders  = "workspace/didChangeWorkspaceFolders"
	MethodShowMessage       = "window/showMessage"
)

// The position encodings a client and server can agree on: what a
// Position's Character counts.
const (
	EncodingUTF8  = "utf-8"  // bytes
	EncodingUTF16 = "utf-16" // UTF-16 code units, the default
)

// Position is a place in a document: a line and a character offset in
// it, both from 0. Character counts in the encoding the server and the
// client agreed on.
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range is a span of a document, End exclusive.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range in a document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// TextDocumentIdentifier names a document by its URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier names a version of a document.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}

// TextDocumentItem is a document the client opened, with its text.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Text       string `json:"text"`
	Version    int32  `json:"version"`
}

// TextDocumentPositionParams names a position in a document, the params
// of completion, hover and definition.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// TextEdit replaces a range of a document with NewText.
type TextEdit struct {
	NewText string `json:"newText"`
	Range   Range  `json:"range"`
}

// CancelParams names the request $/cancelRequest cancels.
type CancelParams struct {
	ID json.RawMessage `json:"id"`
}

// MessageType is how serious a window/showMessage is.
type MessageType int

// MessageError is the type of a message about an error, the only kind the
// server shows.
const MessageError MessageType = 1

// ShowMessageParams is a window/showMessage, which the client shows the
// user.
type ShowMessageParams struct {
	Message string      `json:"message"`
	Type    MessageType `json:"type"`
}
