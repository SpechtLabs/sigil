// Package output defines the values of the --output and --color flags the
// sigil and devtool commands take, and the records the commands' JSON and
// YAML output shares.
//
// [Format] and [Color] are flag values that reject anything but their
// listed modes while cobra parses the flags. [Encode] writes any record as
// JSON or YAML, and [Diagnostic] is the record of one compiler error or
// lint finding. [ColorFromArgs] and [Color.Apply] let --color take effect
// before the real parse, so even the help cobra prints on a parse error
// honors it.
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
	"github.com/spechtlabs/sigil/internal/workspace"
)

// Format is an output format, the value of --output. *Format implements
// [github.com/spf13/pflag.Value], so an unknown value is rejected while
// cobra parses the flags.
type Format string

// Supported output formats.
const (
	Text Format = "text" // for a person; each command lays out its own
	JSON Format = "json" // the command's records, indented by two spaces
	YAML Format = "yaml" // the same records as YAML
)

// Formats lists every supported format, for validation and flag completion.
var Formats = []string{string(Text), string(JSON), string(YAML)}

// Diagnostic is one error or lint finding, as JSON and YAML print it. The
// WebAssembly module prints the same record.
type Diagnostic = workspace.Diagnostic

// String implements [github.com/spf13/pflag.Value]. It returns the
// format's name.
func (f *Format) String() string { return string(*f) }

// Set implements [github.com/spf13/pflag.Value]. It returns an error for
// anything but one of [Formats].
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

// Type implements [github.com/spf13/pflag.Value]. It returns "format",
// the name help shows for the flag's value.
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
func NewDiagnostic(e *diag.Error) Diagnostic { return workspace.NewDiagnostic(e) }
