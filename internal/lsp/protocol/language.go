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
)

// CompletionList answers textDocument/completion.
type CompletionList struct {
	Items        []CompletionItem `json:"items"`
	IsIncomplete bool             `json:"isIncomplete"`
}

// CompletionItem is one completion. TextEdit, when set, says exactly what
// it replaces, such as the whole dotted name typed after `use`.
type CompletionItem struct {
	TextEdit      *TextEdit          `json:"textEdit,omitempty"`
	Documentation *MarkupContent     `json:"documentation,omitempty"`
	Label         string             `json:"label"`
	Detail        string             `json:"detail,omitempty"`
	SortText      string             `json:"sortText,omitempty"`
	Kind          CompletionItemKind `json:"kind,omitempty"`
}

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
