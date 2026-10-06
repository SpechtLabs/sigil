package protocol

// CompletionItemKind says what a completion is, for the client's icon.
type CompletionItemKind int

// The completion kinds the server uses.
const (
	CompletionFunction    CompletionItemKind = 3
	CompletionConstructor CompletionItemKind = 4
	CompletionField       CompletionItemKind = 5
	CompletionVariable    CompletionItemKind = 6
	CompletionInterface   CompletionItemKind = 8
	CompletionModule      CompletionItemKind = 9
	CompletionValue       CompletionItemKind = 12
	CompletionEnum        CompletionItemKind = 13
	CompletionKeyword     CompletionItemKind = 14
	CompletionConstant    CompletionItemKind = 21
	CompletionStruct      CompletionItemKind = 22
	CompletionEnumMember  CompletionItemKind = 20
	CompletionOperator    CompletionItemKind = 24
)

// CompletionList answers textDocument/completion.
type CompletionList struct {
	Items        []CompletionItem `json:"items"`
	IsIncomplete bool             `json:"isIncomplete"`
}

// CompletionItem is one completion. TextEdit, when set, says exactly what
// it replaces, such as the whole dotted name typed after `use`, and its
// NewText is a snippet when InsertTextFormat says so.
type CompletionItem struct {
	TextEdit         *TextEdit                   `json:"textEdit,omitempty"`
	Documentation    *MarkupContent              `json:"documentation,omitempty"`
	LabelDetails     *CompletionItemLabelDetails `json:"labelDetails,omitempty"`
	Label            string                      `json:"label"`
	Detail           string                      `json:"detail,omitempty"`
	SortText         string                      `json:"sortText,omitempty"`
	Kind             CompletionItemKind          `json:"kind,omitempty"`
	InsertTextFormat InsertTextFormat            `json:"insertTextFormat,omitempty"`
	Preselect        bool                        `json:"preselect,omitempty"`
}

// CompletionItemLabelDetails is shown next to a completion's label:
// Description says what it is, such as "input".
type CompletionItemLabelDetails struct {
	Description string `json:"description,omitempty"`
}

// InsertTextFormat says whether a completion inserts plain text or a
// snippet, with placeholders such as `$1` and `${1:name}`.
type InsertTextFormat int

// The insert text formats.
const (
	PlainText InsertTextFormat = 1
	Snippet   InsertTextFormat = 2
)

// MarkupContent is text for the user: markdown, or plain text.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Markdown is the markup kind of markdown text.
const Markdown = "markdown"

// Hover answers textDocument/hover.
type Hover struct {
	Range    *Range        `json:"range,omitempty"`
	Contents MarkupContent `json:"contents"`
}

// SignatureHelp answers textDocument/signatureHelp: the signature of the
// call the cursor is in, and which of its parameters the cursor is on.
type SignatureHelp struct {
	ActiveParameter *uint32                `json:"activeParameter,omitempty"`
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature uint32                 `json:"activeSignature"`
}

// SignatureInformation is one signature: its label, and its parameters
// as spans of the label.
type SignatureInformation struct {
	Documentation *MarkupContent         `json:"documentation,omitempty"`
	Label         string                 `json:"label"`
	Parameters    []ParameterInformation `json:"parameters"`
}

// ParameterInformation is one parameter of a signature. Label is the
// span of the signature's label the parameter is, as the start and end
// offsets in UTF-16 code units.
type ParameterInformation struct {
	Documentation *MarkupContent `json:"documentation,omitempty"`
	Label         [2]uint32      `json:"label"`
}

// SignatureHelpOptions says what triggers signature help.
type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

// CodeActionParams asks for the code actions of a range, with the
// diagnostics the client shows there.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Context      CodeActionContext      `json:"context"`
	Range        Range                  `json:"range"`
}

// CodeActionContext holds the diagnostics a code action request is about,
// and the kinds of action the client wants, or none for every kind.
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
}

// CodeActionQuickFix is the kind of a code action that fixes a
// diagnostic.
const CodeActionQuickFix = "quickfix"

// CodeAction is one change the user can make, with the diagnostics it
// fixes.
type CodeAction struct {
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	IsPreferred bool           `json:"isPreferred,omitempty"`
}

// WorkspaceEdit is a set of edits, by document URI.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes"`
}

// CodeActionOptions says which kinds of code action the server offers.
type CodeActionOptions struct {
	CodeActionKinds []string `json:"codeActionKinds,omitempty"`
}

// InlayHintParams asks for the inlay hints of a range of a document.
type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

// InlayHintKind says what an inlay hint shows.
type InlayHintKind int

// InlayHintType is the kind of a hint that shows a type.
const InlayHintType InlayHintKind = 1

// InlayHint is text the client shows inline at a position, such as the
// type of a let after its name.
type InlayHint struct {
	Label       string        `json:"label"`
	Position    Position      `json:"position"`
	Kind        InlayHintKind `json:"kind,omitempty"`
	PaddingLeft bool          `json:"paddingLeft,omitempty"`
}
