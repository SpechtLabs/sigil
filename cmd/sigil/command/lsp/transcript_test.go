package lsp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// completionKinds names the completion kinds the server sends.
var completionKinds = map[protocol.CompletionItemKind]string{
	protocol.CompletionFunction:    "function",
	protocol.CompletionConstructor: "constructor",
	protocol.CompletionField:       "field",
	protocol.CompletionVariable:    "variable",
	protocol.CompletionInterface:   "kind",
	protocol.CompletionModule:      "module",
	protocol.CompletionValue:       "value",
	protocol.CompletionEnum:        "enum",
	protocol.CompletionKeyword:     "keyword",
	protocol.CompletionEnumMember:  "enum value",
	protocol.CompletionConstant:    "param",
	protocol.CompletionStruct:      "type",
}

// render writes what the server sent, for method, to the transcript in a
// form a reader can review: a line per diagnostic, completion and
// location, a hover's and an edit's text, and anything else as indented
// JSON. Ranges are the protocol's, lines and characters from 0.
func (c *client) render(method string, raw json.RawMessage) {
	c.t.Helper()
	if string(raw) == "null" {
		fmt.Fprintf(&c.log, "<-- null\n")
		return
	}
	switch method {
	case protocol.MethodPublishDiagnostic:
		var p protocol.PublishDiagnosticsParams
		c.decode(raw, &p)
		version := ""
		if p.Version != nil {
			version = fmt.Sprintf(" (version %d)", *p.Version)
		}
		fmt.Fprintf(&c.log, "<-- diagnostics for %s%s: %d\n", c.relative(p.URI), version, len(p.Diagnostics))
		for _, d := range p.Diagnostics {
			severity := "error"
			if d.Severity == protocol.SeverityWarning {
				severity = "warning"
			}
			if d.Code != "" {
				severity += " " + d.Code
			}
			fmt.Fprintf(&c.log, "    %s %s: %s\n", span(d.Range), severity, indent(d.Message, "      "))
		}
	case protocol.MethodCompletion:
		var l protocol.CompletionList
		c.decode(raw, &l)
		fmt.Fprintf(&c.log, "<-- %d completions\n", len(l.Items))
		for _, it := range l.Items {
			line := fmt.Sprintf("    %s (%s)", it.Label, completionKinds[it.Kind])
			if it.Detail != "" {
				line += " " + it.Detail
			}
			if it.TextEdit != nil {
				line += fmt.Sprintf(" -> %q at %s", it.TextEdit.NewText, span(it.TextEdit.Range))
			}
			fmt.Fprintln(&c.log, line)
		}
	case protocol.MethodHover:
		var h protocol.Hover
		c.decode(raw, &h)
		fmt.Fprintf(&c.log, "<-- hover at %s\n    %s\n", span(*h.Range), indent(h.Contents.Value, "    "))
	case protocol.MethodDefinition:
		var locs []protocol.Location
		c.decode(raw, &locs)
		for _, l := range locs {
			fmt.Fprintf(&c.log, "<-- %s at %s\n", c.relative(l.URI), span(l.Range))
		}
	case protocol.MethodFormatting:
		var edits []protocol.TextEdit
		c.decode(raw, &edits)
		fmt.Fprintf(&c.log, "<-- %d edits\n", len(edits))
		for _, e := range edits {
			fmt.Fprintf(&c.log, "    replace %s with\n    %s\n", span(e.Range), indent(strings.TrimSuffix(e.NewText, "\n"), "    "))
		}
	default:
		c.json(raw)
	}
}

// decode reads raw into v.
func (c *client) decode(raw json.RawMessage, v any) {
	c.t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		c.t.Fatalf("%s: %v", raw, err)
	}
}

// span renders a range as line:character-line:character.
func span(r protocol.Range) string {
	return fmt.Sprintf("%d:%d-%d:%d", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

// indent indents every line of s after the first by prefix.
func indent(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}
