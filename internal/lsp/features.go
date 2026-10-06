package lsp

import (
	"bytes"
	"encoding/json"

	"github.com/spechtlabs/sigil/internal/format"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// at decodes the params of a request about a position in an open
// document, and returns the view to answer from and the position's
// offset. The view has no project when the document's project couldn't be
// loaded, or didn't read the file; its source is then the buffer's.
func (s *Server) at(raw json.RawMessage) (*view, *lines, int, *jsonrpc.Error) {
	p, bad := decode[protocol.TextDocumentPositionParams](raw)
	if bad != nil {
		return nil, nil, 0, bad
	}
	d, snap, name := s.current(p.TextDocument.URI)
	if d == nil {
		return nil, nil, 0, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: p.TextDocument.URI + " isn't open"}
	}
	v := &view{src: d.text}
	if name != "" {
		v = &view{proj: snap.Project, file: name, src: snap.Project.SourceOf(name)}
	}
	l := newLines(v.src, s.encoding)
	return v, l, l.offset(p.Position), nil
}

// completion answers textDocument/completion.
func (s *Server) completion(raw json.RawMessage) (*protocol.CompletionList, *jsonrpc.Error) {
	v, l, offset, bad := s.at(raw)
	if bad != nil {
		return nil, bad
	}
	items, from := v.complete(offset)
	edit := l.rangeOf(from, offset)
	out := &protocol.CompletionList{Items: make([]protocol.CompletionItem, len(items))}
	for i, it := range items {
		insert := it.insert
		if insert == "" {
			insert = it.label
		}
		ci := protocol.CompletionItem{
			Label:    it.label,
			Kind:     it.kind,
			Detail:   it.detail,
			SortText: it.sort + it.label,
			TextEdit: &protocol.TextEdit{Range: edit, NewText: insert},
		}
		if it.doc != "" {
			ci.Documentation = &protocol.MarkupContent{Kind: protocol.Markdown, Value: it.doc}
		}
		out.Items[i] = ci
	}
	return out, nil
}

// hover answers textDocument/hover, with null where there's nothing to
// show.
func (s *Server) hover(raw json.RawMessage) (*protocol.Hover, *jsonrpc.Error) {
	v, l, offset, bad := s.at(raw)
	if bad != nil {
		return nil, bad
	}
	t := v.targetAt(offset)
	if t == nil || t.hover == "" {
		return nil, nil
	}
	r := l.rangeOf(t.from, t.to)
	return &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.Markdown, Value: t.hover}, Range: &r}, nil
}

// definition answers textDocument/definition with the locations of the
// declaration, or null where there's none, such as a name a kind linked
// into the binary declares.
func (s *Server) definition(raw json.RawMessage) ([]protocol.Location, *jsonrpc.Error) {
	v, _, offset, bad := s.at(raw)
	if bad != nil {
		return nil, bad
	}
	t := v.targetAt(offset)
	if t == nil || len(t.defs) == 0 {
		return nil, nil
	}
	out := make([]protocol.Location, 0, len(t.defs))
	for _, loc := range t.defs {
		uri, _ := s.uriOfName(loc.file)
		l := newLines(v.proj.SourceOf(loc.file), s.encoding)
		out = append(out, protocol.Location{URI: uri, Range: l.rangeOf(loc.from, loc.to)})
	}
	return out, nil
}

// formatting answers textDocument/formatting with the edit that formats
// the document as `sigil fmt` does: none when it's formatted already, and
// an error when it doesn't parse, which formatting needs.
func (s *Server) formatting(raw json.RawMessage) ([]protocol.TextEdit, *jsonrpc.Error) {
	p, bad := decode[protocol.DocumentFormattingParams](raw)
	if bad != nil {
		return nil, bad
	}
	d := s.open(p.TextDocument.URI)
	if d == nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: p.TextDocument.URI + " isn't open"}
	}
	out, errs := format.Source(d.path, d.text)
	if errs != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.RequestFailed, Message: "the document doesn't parse, so it can't be formatted: " + errs[0].Error()}
	}
	if bytes.Equal(out, d.text) {
		return []protocol.TextEdit{}, nil
	}
	l := newLines(d.text, s.encoding)
	return []protocol.TextEdit{{Range: l.rangeOf(0, len(d.text)), NewText: string(out)}}, nil
}
