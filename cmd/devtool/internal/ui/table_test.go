package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestTable(t *testing.T) {
	columns := []Column{{Heading: "PACKAGE"}, {Heading: "NAME"}, {Heading: "TIME", Right: true}}
	tests := []struct {
		name string
		rows [][]Cell
		want string
	}{
		{name: "headings only", want: "  PACKAGE  NAME  TIME\n"},
		{
			name: "aligned columns",
			rows: [][]Cell{
				{{Text: "internal/lexer"}, {Text: "Lexer/rules=64"}, {Text: "58.2µs"}},
				{{Text: "."}, {Text: "Root"}, {Text: "90ns"}},
			},
			want: "" +
				"  PACKAGE         NAME              TIME\n" +
				"  internal/lexer  Lexer/rules=64  58.2µs\n" +
				"  .               Root              90ns\n",
		},
		{
			name: "styles don't change the padding",
			rows: [][]Cell{{{Text: "a", Style: strings.ToUpper}, {Text: "b"}, {Text: "1s", Style: func(s string) string { return "<" + s + ">" }}}},
			want: "  PACKAGE  NAME  TIME\n  A        b       <1s>\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := Table(printer(&out, false).Theme(), columns, tt.rows); got != tt.want {
				t.Errorf("Table() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestMarkdown(t *testing.T) {
	columns := []Column{{Heading: "Target"}, {Heading: "Execs", Right: true}}
	tests := []struct {
		name string
		rows [][]Cell
		want string
	}{
		{name: "headings only", want: "| Target | Execs |\n| --- | ---: |\n"},
		{
			name: "rows lose their style, and pipes are escaped",
			rows: [][]Cell{{{Text: "Fuzz|A", Style: strings.ToUpper}, {Text: "1.2M"}}},
			want: "| Target | Execs |\n| --- | ---: |\n| Fuzz\\|A | 1.2M |\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Markdown(columns, tt.rows); got != tt.want {
				t.Errorf("Markdown() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
