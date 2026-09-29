package output

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/pflag"
)

// Color is the value of the --color flag: when output is styled. *Color
// implements [pflag.Value], so an unknown mode is rejected while cobra
// parses the flags.
type Color string

// The color modes.
const (
	ColorAuto   Color = "auto"   // on a terminal, unless NO_COLOR is set
	ColorAlways Color = "always" // even when piped
	ColorNever  Color = "never"  // plain text everywhere
)

// Colors lists every color mode, for validation and flag completion.
var Colors = []string{string(ColorAuto), string(ColorAlways), string(ColorNever)}

// String implements [pflag.Value]. It returns the mode's name.
func (c *Color) String() string { return string(*c) }

// Set implements [pflag.Value]. It returns an error for anything but one
// of [Colors].
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

// Type implements [pflag.Value]. It returns "mode", the name help shows
// for the flag's value.
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

// ColorFromArgs reads --color from args, ignoring everything else, so the
// mode can take effect before anything is written: including the help
// cobra prints while it's still parsing flags. An invalid value reads as
// auto; the real parse then reports it.
func ColorFromArgs(args []string) Color {
	mode := ColorAuto
	fs := pflag.NewFlagSet("color", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Var(&mode, "color", "")
	_ = fs.Parse(args)
	return mode
}
