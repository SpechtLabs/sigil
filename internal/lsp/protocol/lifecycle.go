package protocol

// InitializeParams is what the client says about itself in initialize.
// Only the parts the server reads are here.
type InitializeParams struct {
	ClientInfo       *ClientInfo        `json:"clientInfo,omitempty"`
	RootURI          *string            `json:"rootUri"`
	RootPath         *string            `json:"rootPath,omitempty"`
	Capabilities     ClientCapabilities `json:"capabilities"`
	WorkspaceFolders []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
}

// ClientInfo names the client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// WorkspaceFolder is one root folder the client has open.
type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

// ClientCapabilities is what the client supports. Only the position
// encodings and whether it lets the server register file watchers are
// read.
type ClientCapabilities struct {
	General   *GeneralClientCapabilities   `json:"general,omitempty"`
	Workspace *WorkspaceClientCapabilities `json:"workspace,omitempty"`
}

// WorkspaceClientCapabilities is what the client supports about the
// workspace.
type WorkspaceClientCapabilities struct {
	DidChangeWatchedFiles *DynamicRegistration `json:"didChangeWatchedFiles,omitempty"`
}

// DynamicRegistration says whether the client lets the server register a
// capability at run time.
type DynamicRegistration struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
}

// GeneralClientCapabilities holds the position encodings the client
// supports, in its order of preference.
type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// InitializeResult answers initialize.
type InitializeResult struct {
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerInfo names the server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerCapabilities is what the server offers.
type ServerCapabilities struct {
	TextDocumentSync           *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	CompletionProvider         *CompletionOptions       `json:"completionProvider,omitempty"`
	Workspace                  *WorkspaceCapabilities   `json:"workspace,omitempty"`
	PositionEncoding           string                   `json:"positionEncoding,omitempty"`
	HoverProvider              bool                     `json:"hoverProvider,omitempty"`
	DefinitionProvider         bool                     `json:"definitionProvider,omitempty"`
	DocumentFormattingProvider bool                     `json:"documentFormattingProvider,omitempty"`
}

// TextDocumentSyncKind is how the client sends a changed document.
type TextDocumentSyncKind int

// SyncFull asks the client to send a changed document whole. The server
// reads incremental changes too.
const SyncFull TextDocumentSyncKind = 1

// TextDocumentSyncOptions says which document notifications the server
// wants.
type TextDocumentSyncOptions struct {
	Save      *SaveOptions         `json:"save,omitempty"`
	OpenClose bool                 `json:"openClose"`
	Change    TextDocumentSyncKind `json:"change"`
}

// SaveOptions asks for didSave.
type SaveOptions struct {
	IncludeText bool `json:"includeText"`
}

// CompletionOptions says what triggers completion.
type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// WorkspaceCapabilities is what the server offers about workspace
// folders.
type WorkspaceCapabilities struct {
	WorkspaceFolders *WorkspaceFoldersServerCapabilities `json:"workspaceFolders,omitempty"`
}

// WorkspaceFoldersServerCapabilities says the server follows changes to
// the client's folders.
type WorkspaceFoldersServerCapabilities struct {
	Supported           bool `json:"supported"`
	ChangeNotifications bool `json:"changeNotifications"`
}

// DidChangeWorkspaceFoldersParams lists the folders the client added
// and removed.
type DidChangeWorkspaceFoldersParams struct {
	Event WorkspaceFoldersChangeEvent `json:"event"`
}

// WorkspaceFoldersChangeEvent is one change of the client's folders.
type WorkspaceFoldersChangeEvent struct {
	Added   []WorkspaceFolder `json:"added"`
	Removed []WorkspaceFolder `json:"removed"`
}
