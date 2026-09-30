package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"
)

// jsonReader builds the YAML nodes of a JSON document from the JSON
// decoder's tokens, positioning each where its first byte is.
type jsonReader struct {
	src []byte
	dec *json.Decoder
	err error // why the JSON is invalid, once a read found it is
}

// decodeJSON reads a JSON configuration into the YAML nodes the walk
// reads: an object becomes a map, an array a list, and a value a scalar
// with the YAML tag of its type, each positioned where the file writes
// it. A key the object has twice is kept twice, so the walk reports it
// the way it reports one in YAML. A file of only whitespace is empty,
// as it is in YAML and TOML.
func decodeJSON(p *parser, src []byte) (*yaml.Node, humane.Error) {
	if len(bytes.TrimSpace(src)) == 0 {
		return nil, nil
	}
	r := &jsonReader{src: src, dec: json.NewDecoder(bytes.NewReader(src))}
	r.dec.UseNumber()
	const advice = "fix the JSON syntax; a comment, for one, isn't JSON"
	if root := r.value(); root != nil {
		at := r.next()
		if _, err := r.dec.Token(); !errors.Is(err, io.EOF) {
			return nil, p.failAt(at, "invalid JSON: more follows the configuration's object", advice)
		}
		return root, nil
	}
	at, msg := r.next(), r.err.Error()
	switch syn, ok := errors.AsType[*json.SyntaxError](r.err); {
	case ok:
		// Offset counts the bytes read, the offending one included.
		at = position(src, int(syn.Offset)-1)
	case errors.Is(r.err, io.ErrUnexpectedEOF), errors.Is(r.err, io.EOF):
		at, msg = position(src, len(src)), "the file ends before the JSON does"
	}
	return nil, p.failAt(at, "invalid JSON: "+msg, advice)
}

// value reads the next value, with every value it holds. It returns nil
// once the JSON turns out to be invalid, and keeps why in r.err.
func (r *jsonReader) value() *yaml.Node {
	at := r.next()
	tok, err := r.dec.Token()
	if err != nil {
		r.err = err
		return nil
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Line: at.Line, Column: at.Column}
	switch v := tok.(type) {
	case json.Delim:
		return r.container(n, v)
	case string:
		n.Tag, n.Value = tagStr, v
	case json.Number:
		n.Tag, n.Value = tagFloat, v.String()
		if _, err := v.Int64(); err == nil {
			n.Tag = tagInt
		}
	case bool:
		n.Tag, n.Value = tagBool, "false"
		if v {
			n.Value = "true"
		}
	default: // null
		n.Tag = tagNull
	}
	return n
}

// container reads what an object or array that open starts holds, and
// its closing delimiter, into n.
func (r *jsonReader) container(n *yaml.Node, open json.Delim) *yaml.Node {
	n.Kind, n.Tag = yaml.SequenceNode, tagSeq
	if open == '{' {
		n.Kind, n.Tag = yaml.MappingNode, tagMap
	}
	for r.dec.More() {
		// An object's key comes before each of its values.
		if n.Kind == yaml.MappingNode {
			k := r.value()
			if k == nil {
				return nil
			}
			n.Content = append(n.Content, k)
		}
		item := r.value()
		if item == nil {
			return nil
		}
		n.Content = append(n.Content, item)
	}
	if _, err := r.dec.Token(); err != nil {
		r.err = err
		return nil
	}
	return n
}

// next returns the position of the next token: past what the decoder
// has read, and past the whitespace, commas and colons between tokens.
func (r *jsonReader) next() Pos {
	i := int(r.dec.InputOffset())
	for i < len(r.src) && bytes.IndexByte([]byte(" \t\r\n,:"), r.src[i]) >= 0 {
		i++
	}
	return position(r.src, i)
}
