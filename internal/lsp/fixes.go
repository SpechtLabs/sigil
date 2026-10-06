package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
	"github.com/spechtlabs/sigil/internal/types"
)

// The checker's diagnostics a quick fix reads: the name a help line
// suggests, the string a map key that reads as a name could be, the
// reason a constructor should name bare or with its label, a reason the
// decision doesn't declare, and the payload field a constructor leaves
// out.
var (
	didYouMean = regexp.MustCompile("did you mean [`\"]([^`\"]+)[`\"]\\?")
	stringKey  = regexp.MustCompile("for the string key, write `(\"[^`]*\")`")
	bareReason = regexp.MustCompile("write `reason: ([A-Za-z_][A-Za-z0-9_]*)`")
	namedCall  = regexp.MustCompile("^write `[A-Za-z_][A-Za-z0-9_]*\\(reason: ([A-Za-z_][A-Za-z0-9_]*)[,)]")
	noReason   = regexp.MustCompile("^decision (\\S+) has no reason `[^`]+`$")
	needsField = regexp.MustCompile(`^decision (\S+) needs field "([^"]+)"$`)
)

// fixesOf works out the quick fixes of a diagnostic of src, which l
// counts positions in:
//
//   - a name the help suggests another for: the name replaced by it;
//   - a reason that isn't a bare name, such as a string, or that has no
//     `reason:` label: the reason the help writes;
//   - a reason the decision doesn't declare, with no name close to it:
//     each reason the decision declares;
//   - a constructor that leaves a payload field out: the field added
//     with its type's zero value.
//
// k is the kind of the document the diagnostic is in, or nil. It returns
// nil for none.
func fixesOf(e *diag.Error, src []byte, k *kind.Kind, l *lines) *protocol.DiagnosticData {
	if !e.Pos.IsValid() || !e.End.IsValid() || e.End.Offset > len(src) || e.Pos.Offset >= e.End.Offset {
		return nil
	}
	from, to := e.Pos.Offset, e.End.Offset
	old := string(src[from:to])
	var fixes []protocol.Fix
	if isName(src[from:to]) {
		if m := stringKey.FindStringSubmatch(e.Help); m != nil {
			fixes = append(fixes, replace(l, from, to, old, m[1], fmt.Sprintf("Write the string key %s", m[1])))
		}
		if m := didYouMean.FindStringSubmatch(e.Help); m != nil && m[1] != old {
			fixes = append(fixes, replace(l, from, to, old, m[1], fmt.Sprintf("Change to `%s`", m[1])))
		} else if m := noReason.FindStringSubmatch(e.Msg); m != nil && k != nil && k.Decision(m[1]) != nil {
			for _, r := range k.Decision(m[1]).Reasons {
				fixes = append(fixes, replace(l, from, to, old, r, fmt.Sprintf("Change to `%s`", r)))
			}
		}
	}
	if m := bareReason.FindStringSubmatch(e.Help); m != nil && m[1] != old {
		fixes = append(fixes, replace(l, from, to, old, m[1], fmt.Sprintf("Write the reason as `%s`", m[1])))
	}
	if m := namedCall.FindStringSubmatch(e.Help); m != nil {
		fixes = append(fixes, replace(l, from, to, old, "reason: "+m[1], fmt.Sprintf("Write `reason: %s`", m[1])))
	}
	if m := needsField.FindStringSubmatch(e.Msg); m != nil && k != nil && src[to-1] == ')' {
		if fix, ok := missingField(src, from, to, k.Decision(m[1]), m[2], l); ok {
			fixes = append(fixes, fix)
		}
	}
	if fixes == nil {
		return nil
	}
	return &protocol.DiagnosticData{Fixes: fixes}
}

// replace is the fix that replaces old, from from to to, with text.
func replace(l *lines, from, to int, old, text, title string) protocol.Fix {
	return protocol.Fix{Title: title, Replaces: old, Edit: protocol.TextEdit{Range: l.rangeOf(from, to), NewText: text}}
}

// missingField is the fix that adds the payload field name of d, with its
// type's zero value, to the constructor from from to to, whose last byte
// is its `)`. It reports false for a field whose type has no literal to
// write, such as a struct's.
func missingField(src []byte, from, to int, d *kind.Decision, name string, l *lines) (protocol.Fix, bool) {
	if d == nil || d.Field(name) == nil {
		return protocol.Fix{}, false
	}
	zero, ok := zeroOf(d.Field(name).Type)
	if !ok {
		return protocol.Fix{}, false
	}
	arg := name + ": " + zero
	switch before := bytes.TrimRight(src[from:to-1], " \t\r\n"); {
	case bytes.HasSuffix(before, []byte("(")):
	case bytes.HasSuffix(before, []byte(",")):
		arg = " " + arg
	default:
		arg = ", " + arg
	}
	return replace(l, to-1, to, ")", arg+")", fmt.Sprintf("Add the missing field `%s`", name)), true
}

// zeroOf returns the literal of t's zero value, to fill a field with, or
// false for a type that has none to write.
func zeroOf(t types.Type) (string, bool) {
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.String:
			return `""`, true
		case types.Int:
			return "0", true
		case types.Float:
			return "0.0", true
		case types.Bool:
			return kwFalse, true
		case types.Duration:
			return constant.FormatDuration(0), true
		}
	case *types.List:
		return "[]", true
	case *types.Map:
		return "{}", true
	case *types.Optional:
		return "none", true
	case *types.Enum:
		if len(t.Values) > 0 {
			return t.Name + "." + t.Values[0], true
		}
	}
	return "", false
}

// isName reports whether b is a name, dotted or not, as a fix replaces.
func isName(b []byte) bool {
	for _, c := range b {
		if c != '_' && c != '.' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return len(b) > 0
}

// codeActions answers textDocument/codeAction with the quick fixes of
// the diagnostics the client sends: the ones the server attached when it
// published them, each while its document still holds the text it
// replaces, so a fix of an edited document doesn't land in the wrong
// place. A fix is preferred when it's its diagnostic's only one.
func (s *Server) codeActions(raw json.RawMessage) ([]protocol.CodeAction, *jsonrpc.Error) {
	p, bad := decode[protocol.CodeActionParams](raw)
	if bad != nil {
		return nil, bad
	}
	out := []protocol.CodeAction{}
	d := s.open(p.TextDocument.URI)
	if d == nil || len(p.Context.Only) > 0 && !slices.ContainsFunc(p.Context.Only, func(k string) bool {
		return k == "" || k == protocol.CodeActionQuickFix
	}) {
		return out, nil
	}
	l := newLines(d.text, s.encoding)
	for _, diagnostic := range p.Context.Diagnostics {
		if diagnostic.Data == nil {
			continue
		}
		for _, fix := range diagnostic.Data.Fixes {
			from, to := l.offset(fix.Edit.Range.Start), l.offset(fix.Edit.Range.End)
			if from > to || string(d.text[from:to]) != fix.Replaces {
				continue
			}
			out = append(out, protocol.CodeAction{
				Title:       fix.Title,
				Kind:        protocol.CodeActionQuickFix,
				Diagnostics: []protocol.Diagnostic{diagnostic},
				IsPreferred: len(diagnostic.Data.Fixes) == 1,
				Edit:        &protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{p.TextDocument.URI: {fix.Edit}}},
			})
		}
	}
	return out, nil
}
