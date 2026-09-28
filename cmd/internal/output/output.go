// Package output defines the output formats accepted by the --output flag,
// and the records every command's JSON and YAML output shares.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/diag"
)

// Format is an output format. It implements pflag.Value, so an unknown value
// is rejected while cobra parses the flags.
type Format string

// Supported output formats.
const (
	Text Format = "text"
	JSON Format = "json"
	YAML Format = "yaml"
)

// Formats lists every supported format, for validation and flag completion.
var Formats = []string{string(Text), string(JSON), string(YAML)}

// Diagnostic is one error or lint finding, as JSON and YAML print it.
type Diagnostic struct {
	Severity string `json:"severity" yaml:"severity"` // error or warning
	Lint     string `json:"lint,omitempty" yaml:"lint,omitempty"`
	File     string `json:"file,omitempty" yaml:"file,omitempty"`
	Document string `json:"document,omitempty" yaml:"document,omitempty"`
	Message  string `json:"message" yaml:"message"`
	Help     string `json:"help,omitempty" yaml:"help,omitempty"`
	Line     int    `json:"line,omitempty" yaml:"line,omitempty"`
	Column   int    `json:"column,omitempty" yaml:"column,omitempty"`
}

// String implements pflag.Value.
func (f *Format) String() string { return string(*f) }

// Set implements pflag.Value.
func (f *Format) Set(s string) error {
	if !slices.Contains(Formats, s) {
		return humane.New(
			fmt.Sprintf("unsupported output type %q", s),
			"use one of: "+strings.Join(Formats, ", "),
		)
	}
	*f = Format(s)
	return nil
}

// Type implements pflag.Value.
func (f *Format) Type() string { return "format" }

// Encode writes v to w as JSON or YAML, indented by two spaces. It's for
// the structured formats only; text output is each command's own.
func Encode(w io.Writer, f Format, v any) humane.Error { //nolint:emptyinterface // any record a command prints
	var err error
	if f == YAML {
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		err = enc.Encode(v)
	} else {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		err = enc.Encode(v)
	}
	if err != nil {
		return humane.Wrap(err, "the output couldn't be written", "check where the output is going")
	}
	return nil
}

// NewDiagnostic converts a compiler or lint diagnostic into its record.
func NewDiagnostic(e *diag.Error) Diagnostic {
	d := Diagnostic{Severity: e.Severity.String(), Lint: e.Code, File: e.File, Message: e.Msg, Help: e.Help, Document: e.Doc}
	if e.Pos.IsValid() {
		d.Line, d.Column = e.Pos.Line, e.Pos.Column
	}
	return d
}
