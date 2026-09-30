package workspace

import "github.com/spechtlabs/sigil/internal/diag"

// Diagnostic is one error or lint finding, as `sigil check -o json` and
// the WebAssembly module print it.
type Diagnostic struct {
	Severity string `json:"severity" yaml:"severity"`             // error or warning
	Lint     string `json:"lint,omitempty" yaml:"lint,omitempty"` // the lint's name, for a lint finding
	File     string `json:"file,omitempty" yaml:"file,omitempty"`
	Document string `json:"document,omitempty" yaml:"document,omitempty"` // the document it's in, when that's known
	Message  string `json:"message" yaml:"message"`
	Help     string `json:"help,omitempty" yaml:"help,omitempty"`     // how to fix it
	Line     int    `json:"line,omitempty" yaml:"line,omitempty"`     // from 1; zero, and left out, when the diagnostic has no position
	Column   int    `json:"column,omitempty" yaml:"column,omitempty"` // in characters, from 1
}

// NewDiagnostic converts a compiler or lint diagnostic into its record.
func NewDiagnostic(e *diag.Error) Diagnostic {
	d := Diagnostic{Severity: e.Severity.String(), Lint: e.Code, File: e.File, Message: e.Msg, Help: e.Help, Document: e.Doc}
	if e.Pos.IsValid() {
		d.Line, d.Column = e.Pos.Line, e.Pos.Column
	}
	return d
}

// Diagnostics converts a list of diagnostics into their records, in
// order. It never returns nil, so an empty list encodes as [].
func Diagnostics(errs diag.ErrorList) []Diagnostic {
	out := make([]Diagnostic, len(errs))
	for i, e := range errs {
		out[i] = NewDiagnostic(e)
	}
	return out
}
