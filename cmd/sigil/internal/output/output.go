// Package output defines the output formats accepted by the --output flag.
package output

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
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
