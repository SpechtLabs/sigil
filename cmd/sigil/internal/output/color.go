package output

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// Color is the value of the --color flag: when output is styled.
type Color string

// The color modes.
const (
	ColorAuto   Color = "auto"   // on a terminal, unless NO_COLOR is set
	ColorAlways Color = "always" // even when piped
	ColorNever  Color = "never"  // plain text everywhere
)

// Colors lists every color mode, for validation and flag completion.
var Colors = []string{string(ColorAuto), string(ColorAlways), string(ColorNever)}

// String implements pflag.Value.
func (c *Color) String() string { return string(*c) }

// Set implements pflag.Value.
func (c *Color) Set(s string) error {
	if !slices.Contains(Colors, s) {
		return humane.New(
			fmt.Sprintf("unsupported color mode %q", s),
			"use one of: "+strings.Join(Colors, ", "),
		)
	}
	*c = Color(s)
	return nil
}

// Type implements pflag.Value.
func (c *Color) Type() string { return "mode" }

// Apply makes the process's environment say what the mode says, in the
// variables every terminal library reads: NO_COLOR for never and
// CLICOLOR_FORCE for always. Auto leaves the environment alone, so the
// user's own NO_COLOR still counts.
func (c Color) Apply() {
	// Setting a variable in the process's own environment only fails on
	// an invalid name, and these are fixed.
	switch c {
	case ColorAlways:
		_ = os.Unsetenv("NO_COLOR")
		_ = os.Setenv("CLICOLOR_FORCE", "1")
	case ColorNever:
		_ = os.Unsetenv("CLICOLOR_FORCE")
		_ = os.Setenv("NO_COLOR", "1")
	default:
	}
}
