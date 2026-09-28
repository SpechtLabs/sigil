package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// Cell is one table cell. Style colors it; nil leaves it plain.
type Cell struct {
	Text  string
	Style func(string) string
}

// Column is a table column: its heading, and whether its cells are
// aligned to the right, as numbers are.
type Column struct {
	Heading string
	Right   bool
}

// Table lays out rows under muted headings, indented by two spaces with
// two spaces between the columns.
func Table(th pretty.Theme, columns []Column, rows [][]Cell) string {
	widths := make([]int, len(columns))
	for i, c := range columns {
		widths[i] = ansi.StringWidth(c.Heading)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], ansi.StringWidth(c.Text))
		}
	}

	line := func(cells []Cell) string {
		parts := make([]string, len(cells))
		for i, c := range cells {
			padding := strings.Repeat(" ", widths[i]-ansi.StringWidth(c.Text))
			text := c.Text
			if c.Style != nil {
				text = c.Style(text)
			}
			if columns[i].Right {
				parts[i] = padding + text
			} else {
				parts[i] = text + padding
			}
		}
		return strings.TrimRight("  "+strings.Join(parts, "  "), " ") + "\n"
	}

	headings := make([]Cell, len(columns))
	for i, c := range columns {
		headings[i] = Cell{Text: c.Heading, Style: th.Muted}
	}
	var b strings.Builder
	b.WriteString(line(headings))
	for _, r := range rows {
		b.WriteString(line(r))
	}
	return b.String()
}

// Markdown lays out rows as a Markdown table, for the summaries CI shows
// on the job page. Cells keep their text and lose their style.
func Markdown(columns []Column, rows [][]Cell) string {
	var b strings.Builder
	line := func(cells []string) {
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	headings := make([]string, len(columns))
	rules := make([]string, len(columns))
	for i, c := range columns {
		headings[i] = c.Heading
		rules[i] = "---"
		if c.Right {
			rules[i] = "---:"
		}
	}
	line(headings)
	line(rules)
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = strings.ReplaceAll(c.Text, "|", `\|`)
		}
		line(cells)
	}
	return b.String()
}
