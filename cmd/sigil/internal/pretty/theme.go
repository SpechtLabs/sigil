// Package pretty renders everything sigil prints for a human: status lines,
// key/value blocks, errors, and (through fang) help and usage. All output goes
// through a colorprofile writer, so colors degrade to what the terminal
// supports, honor NO_COLOR, and disappear entirely when output is piped.
package pretty

import (
	"image/color"
	"os"
	"sync"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// palette is sigil's color set. Every color has a light- and a dark-background
// variant, picked by the lipgloss.LightDarkFunc passed to newPalette.
//
// Each variant keeps a WCAG AA contrast ratio of at least 4.5:1 against common
// terminal backgrounds of its mode (light: #FFFFFF, #F5F5F5, Solarized Light;
// dark: #000000, #1E1E1E, Dracula, Solarized Dark) and against the code
// block background. Re-check contrast when changing a color.
type palette struct {
	text   color.Color
	muted  color.Color
	accent color.Color
	ok     color.Color
	info   color.Color
	warn   color.Color
	fail   color.Color
	onFail color.Color
	code   color.Color
}

func newPalette(c lipgloss.LightDarkFunc) palette {
	return palette{
		text:   c(lipgloss.Color("#23222B"), lipgloss.Color("#E4E4EC")),
		muted:  c(lipgloss.Color("#62616D"), lipgloss.Color("#A3A2B0")),
		accent: c(lipgloss.Color("#5B3FC8"), lipgloss.Color("#B69CFF")),
		ok:     c(lipgloss.Color("#177A40"), lipgloss.Color("#7EE0A1")),
		info:   c(lipgloss.Color("#1A62BD"), lipgloss.Color("#7AB8FF")),
		warn:   c(lipgloss.Color("#8A5700"), lipgloss.Color("#FFCB6B")),
		fail:   c(lipgloss.Color("#B8233A"), lipgloss.Color("#FF7A85")),
		// Text on a fail background: white on the dark red, near-black on
		// the light red.
		onFail: c(lipgloss.Color("#FFFFFF"), lipgloss.Color("#1B1B1F")),
		code:   c(lipgloss.Color("#F1EFF8"), lipgloss.Color("#2A2933")),
	}
}

// ColorScheme is a fang.ColorSchemeFunc that styles help and usage output
// with sigil's palette, so it matches everything else the CLI prints.
func ColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	p := newPalette(c)
	return fang.ColorScheme{
		Base:           p.text,
		Title:          p.accent,
		Description:    p.text,
		Codeblock:      p.code,
		Program:        p.accent,
		Command:        p.info,
		DimmedArgument: p.muted,
		Comment:        p.muted,
		Flag:           p.ok,
		FlagDefault:    p.muted,
		QuotedString:   p.warn,
		Argument:       p.text,
		Help:           p.text,
		Dash:           p.muted,
		ErrorHeader:    [2]color.Color{p.onFail, p.fail},
		ErrorDetails:   p.fail,
	}
}

// hasDarkBackground asks the terminal for its background color, once per
// process, since each query waits for the terminal to answer.
//
// The answer arrives on stdin, so it only asks when both stdin and stdout are
// terminals; otherwise it would swallow piped input such as
// `sigil eval --input -`. Without a terminal it assumes a dark background.
var hasDarkBackground = sync.OnceValue(func() bool {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return true
	}
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
})
