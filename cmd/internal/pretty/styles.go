package pretty

import "charm.land/lipgloss/v2"

type styles struct {
	text     lipgloss.Style
	bold     lipgloss.Style
	muted    lipgloss.Style
	ok       lipgloss.Style
	info     lipgloss.Style
	warn     lipgloss.Style
	fail     lipgloss.Style
	accent   lipgloss.Style
	key      lipgloss.Style
	title    lipgloss.Style
	box      lipgloss.Style
	badge    lipgloss.Style
	section  lipgloss.Style
	location lipgloss.Style
	gutter   lipgloss.Style
	help     lipgloss.Style
}

func newStyles(p palette) styles {
	return styles{
		text:     lipgloss.NewStyle().Foreground(p.text),
		bold:     lipgloss.NewStyle().Foreground(p.text).Bold(true),
		muted:    lipgloss.NewStyle().Foreground(p.muted),
		ok:       lipgloss.NewStyle().Foreground(p.ok).Bold(true),
		info:     lipgloss.NewStyle().Foreground(p.info).Bold(true),
		warn:     lipgloss.NewStyle().Foreground(p.warn).Bold(true),
		fail:     lipgloss.NewStyle().Foreground(p.fail).Bold(true),
		accent:   lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		key:      lipgloss.NewStyle().Foreground(p.muted).Bold(true),
		title:    lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		box:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.accent).Padding(0, 1),
		badge:    lipgloss.NewStyle().Foreground(p.onFail).Background(p.fail).Bold(true).Padding(0, 1),
		section:  lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		location: lipgloss.NewStyle().Foreground(p.text).Bold(true),
		gutter:   lipgloss.NewStyle().Foreground(p.accent),
		help:     lipgloss.NewStyle().Foreground(p.info).Bold(true),
	}
}
