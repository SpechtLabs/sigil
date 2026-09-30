package config

import (
	"errors"

	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"
)

// tomlTags are the YAML tags of TOML's scalar kinds, so a TOML value
// reads as the YAML value that means the same.
var tomlTags = map[unstable.Kind]string{
	unstable.String:        tagStr,
	unstable.Bool:          tagBool,
	unstable.Integer:       tagInt,
	unstable.Float:         tagFloat,
	unstable.LocalDate:     tagTimestamp,
	unstable.LocalTime:     tagTimestamp,
	unstable.LocalDateTime: tagTimestamp,
	unstable.DateTime:      tagTimestamp,
}

// tomlTree builds the YAML nodes of a TOML document, one expression at a
// time.
type tomlTree struct {
	src    []byte
	parser *unstable.Parser
	root   *yaml.Node
	tables map[*yaml.Node]bool // the lists [[...]] headers made, which later headers append to
}

// decodeTOML reads a TOML configuration into the YAML nodes the walk
// reads: a table becomes a map, an array or an array of tables a list,
// and a value a scalar with the YAML tag of its kind, each positioned
// where the file writes it. An array, which the TOML parser doesn't
// position, takes its key's position. A key the document sets twice,
// which TOML forbids, is kept twice, so the walk reports it the way it
// reports one in YAML.
func decodeTOML(p *parser, src []byte) (*yaml.Node, humane.Error) {
	t := &tomlTree{
		src:    src,
		parser: &unstable.Parser{},
		root:   &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Line: 1, Column: 1},
		tables: map[*yaml.Node]bool{},
	}
	t.parser.Reset(src)
	current := t.root
	for t.parser.NextExpression() {
		e := t.parser.Expression()
		switch e.Kind {
		case unstable.KeyValue:
			t.keyValue(current, e)
		case unstable.Table:
			current = t.table(e)
		case unstable.ArrayTable:
			current = t.arrayTable(e)
		}
	}
	if err := t.parser.Error(); err != nil {
		at := Pos{Line: 1, Column: 1}
		msg := err.Error()
		if perr, ok := errors.AsType[*unstable.ParserError](err); ok {
			at = t.offset(perr.Highlight)
			msg = perr.Message
		}
		return nil, p.failAt(at, "invalid TOML: "+msg, "fix the TOML syntax; the file holds "+p.keyList(topKeys))
	}
	if len(t.root.Content) == 0 {
		return nil, nil
	}
	return t.root, nil
}

// keyValue adds a key = value line, whose key may be dotted, to table.
func (t *tomlTree) keyValue(table *yaml.Node, e *unstable.Node) {
	keys := t.keys(e.Key())
	last := keys[len(keys)-1]
	parent := t.descend(table, keys[:len(keys)-1])
	parent.Content = append(parent.Content, last, t.value(e.Value(), last))
}

// table opens a [table] header's table, and returns it.
func (t *tomlTree) table(e *unstable.Node) *yaml.Node {
	keys := t.keys(e.Key())
	last := keys[len(keys)-1]
	parent := t.descend(t.root, keys[:len(keys)-1])
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Line: last.Line, Column: last.Column}
	parent.Content = append(parent.Content, last, m)
	return m
}

// arrayTable appends a table to an [[array]] header's list, and returns
// the table.
func (t *tomlTree) arrayTable(e *unstable.Node) *yaml.Node {
	keys := t.keys(e.Key())
	last := keys[len(keys)-1]
	parent := t.descend(t.root, keys[:len(keys)-1])
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Line: last.Line, Column: last.Column}
	if list := lookup(parent, last.Value); list != nil && t.tables[list] {
		list.Content = append(list.Content, m)
		return m
	}
	list := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq, Line: last.Line, Column: last.Column, Content: []*yaml.Node{m}}
	t.tables[list] = true
	parent.Content = append(parent.Content, last, list)
	return m
}

// descend returns the table keys name below table, making the ones that
// don't exist yet, the way a dotted key or header does. A key that names
// a list of tables goes to its last table. A key that holds something
// else is set again, which the walk reports.
func (t *tomlTree) descend(table *yaml.Node, keys []*yaml.Node) *yaml.Node {
	for _, k := range keys {
		switch v := lookup(table, k.Value); {
		case v != nil && v.Kind == yaml.MappingNode:
			table = v
		case v != nil && t.tables[v]:
			table = v.Content[len(v.Content)-1]
		default:
			m := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Line: k.Line, Column: k.Column}
			table.Content = append(table.Content, k, m)
			table = m
		}
	}
	return table
}

// value returns the node of a TOML value. at is its key, whose position
// an array takes.
func (t *tomlTree) value(v *unstable.Node, at *yaml.Node) *yaml.Node {
	line, col := at.Line, at.Column
	if v.Raw.Length > 0 {
		p := t.offset(t.parser.Raw(v.Raw))
		line, col = p.Line, p.Column
	}
	switch v.Kind {
	case unstable.Array:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: tagSeq, Line: line, Column: col}
		for it := v.Children(); it.Next(); {
			n.Content = append(n.Content, t.value(it.Node(), at))
		}
		return n
	case unstable.InlineTable:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Line: line, Column: col}
		for it := v.Children(); it.Next(); {
			t.keyValue(n, it.Node())
		}
		return n
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tomlTags[v.Kind], Value: string(v.Data), Line: line, Column: col}
	}
}

// keys returns the parts of a key, each as a string node positioned
// where the file writes it.
func (t *tomlTree) keys(it unstable.Iterator) []*yaml.Node {
	var out []*yaml.Node
	for it.Next() {
		k := it.Node()
		p := t.offset(t.parser.Raw(k.Raw))
		out = append(out, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: string(k.Data), Line: p.Line, Column: p.Column})
	}
	return out
}

// offset returns the position of b, a part of the source.
func (t *tomlTree) offset(b []byte) Pos {
	return position(t.src, cap(t.src)-cap(b))
}

// lookup returns the value of the last key in the map m named name, or
// nil.
func lookup(m *yaml.Node, name string) *yaml.Node {
	for i := len(m.Content) - 2; i >= 0; i -= 2 {
		if m.Content[i].Value == name {
			return m.Content[i+1]
		}
	}
	return nil
}
