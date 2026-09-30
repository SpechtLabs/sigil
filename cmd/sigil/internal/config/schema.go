package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

// The YAML tags of the nodes every format decodes into.
const (
	tagMap       = "!!map"
	tagSeq       = "!!seq"
	tagStr       = "!!str"
	tagInt       = "!!int"
	tagFloat     = "!!float"
	tagBool      = "!!bool"
	tagNull      = "!!null"
	tagTimestamp = "!!timestamp"
)

// The keys of a configuration, and of each of its require entries.
const (
	keyKinds   = "kinds"
	keyRequire = "require"
	keyLints   = "lints"
	keyPolicy  = "policy"
	keyTrusted = "trusted"
	keyRoots   = "roots"
)

// schemaKey names the JSON Schema a file follows, for editors. A file may
// set it at the top level, and the tools ignore it.
const schemaKey = "$schema"

// The keys a configuration and each of its require entries hold, in the
// order messages list them.
var (
	topKeys     = []string{keyKinds, keyRequire, keyLints}
	requireKeys = []string{keyPolicy, keyTrusted, keyRoots}
)

// parser walks a configuration's nodes, whatever format they were
// decoded from, and positions its errors in file. Every format decodes
// into YAML nodes, which carry the line and column of what they hold.
type parser struct {
	file   string
	dir    string // what relative paths resolve against
	syntax syntax // the file's format, for its name and its advice
}

// config reads the configuration from the root node a format decoded:
// a map, or nil for an empty file.
func (p *parser) config(root *yaml.Node) (*Config, humane.Error) {
	c := &Config{File: p.file, Lints: map[string]lint.Level{}}
	if root == nil || isNull(root) {
		return c, nil
	}
	holds := filepath.Base(p.file) + " holds " + p.keyList(topKeys)
	if root.Kind != yaml.MappingNode {
		return nil, p.fail(root, "the configuration isn't a map", holds)
	}
	err := p.mapping(root, "", append(slices.Clone(topKeys), schemaKey), holds, func(key string, n *yaml.Node) humane.Error {
		var err humane.Error
		switch key {
		case keyKinds:
			c.Kinds, err = p.paths(n, keyKinds, "a kind file")
		case keyRequire:
			c.Require, err = p.require(n)
		case keyLints:
			err = p.lints(n, c.Lints)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// mapping calls each for every entry of the map n, in the order it's
// written, after checking that n is a map whose keys are all known and
// none is repeated. where names n in messages; it's empty for the
// top level. holds is the advice for an unknown key.
func (p *parser) mapping(n *yaml.Node, where string, known []string, holds string, each func(key string, value *yaml.Node) humane.Error) humane.Error {
	seen := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], resolve(n.Content[i+1])
		if k.Kind != yaml.ScalarNode {
			return p.fail(k, "a key"+in(where)+" isn't a name", holds)
		}
		if !slices.Contains(known, k.Value) {
			advice := []string{holds}
			if near, ok := diag.Nearest(k.Value, known); ok {
				advice = []string{fmt.Sprintf("did you mean %q?", near), holds}
			}
			return p.fail(k, fmt.Sprintf("unknown key %q%s", k.Value, in(where)), advice...)
		}
		if first, ok := seen[k.Value]; ok {
			return p.fail(k, fmt.Sprintf("%s is set twice%s", k.Value, in(where)), fmt.Sprintf("it's first set at line %d; keep one of the two", first.Line))
		}
		seen[k.Value] = k
		if err := each(k.Value, v); err != nil {
			return err
		}
	}
	return nil
}

// require reads require:, a list of entries that each name a policy.
func (p *parser) require(n *yaml.Node) ([]Require, humane.Error) {
	example := p.syntax.entry
	if isNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, p.fail(n, "require isn't a list", example)
	}
	var out []Require
	first := map[string]Pos{}
	for i, entry := range n.Content {
		entry = resolve(entry)
		where := fmt.Sprintf("require[%d]", i)
		if entry.Kind != yaml.MappingNode {
			return nil, p.fail(entry, where+" isn't a map", example)
		}
		r := Require{Pos: pos(entry)}
		named := false
		err := p.mapping(entry, where, requireKeys, "a require entry holds "+p.keyList(requireKeys), func(key string, v *yaml.Node) humane.Error {
			var err humane.Error
			switch key {
			case keyPolicy:
				named = true
				r.Pos = pos(v)
				r.Policy, err = p.policy(v, where+".policy")
			case keyTrusted:
				r.Trusted, err = p.paths(v, where+".trusted", "a file or directory")
			case keyRoots:
				r.Roots, err = p.list(v, where+".roots", "a policy name or pattern, such as \"payments.*\"")
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		if !named {
			return nil, p.fail(entry, where+" names no policy", "add "+p.syntax.key(keyPolicy)+" with the name of the policy check enforces")
		}
		if at, ok := first[r.Policy]; ok {
			return nil, p.failAt(r.Pos, r.Policy+" is required twice",
				fmt.Sprintf("it's first required at line %d; merge the two entries' %s and %s", at.Line, p.syntax.key(keyTrusted), p.syntax.key(keyRoots)))
		}
		first[r.Policy] = r.Pos
		out = append(out, r)
	}
	return out, nil
}

// policy reads a required policy's name: one name, not a pattern.
func (p *parser) policy(n *yaml.Node, where string) (string, humane.Error) {
	const advice = "name the policy check enforces, such as deploy.guardrails"
	switch {
	case n.Kind != yaml.ScalarNode:
		return "", p.fail(n, where+" isn't a name", advice)
	case isNull(n) || n.Value == "":
		return "", p.fail(n, where+" is empty", advice)
	}
	if strings.Contains(n.Value, "*") {
		return "", p.fail(n, fmt.Sprintf("%s %q is a pattern", where, n.Value), "name one policy; "+p.syntax.key(keyRoots)+" takes the patterns of the policies it applies to")
	}
	return n.Value, nil
}

// paths reads a list of paths, or a single one, and resolves each
// against the configuration's directory. what describes an entry.
func (p *parser) paths(n *yaml.Node, where, what string) ([]string, humane.Error) {
	out, err := p.list(n, where, what+", relative to "+filepath.Base(p.file))
	if err != nil {
		return nil, err
	}
	for i, path := range out {
		if !filepath.IsAbs(path) {
			out[i] = filepath.Join(p.dir, filepath.FromSlash(path))
		}
	}
	return out, nil
}

// list reads a list of non-empty strings, or a single one. what
// describes an entry.
func (p *parser) list(n *yaml.Node, where, what string) ([]string, humane.Error) {
	if isNull(n) {
		return nil, nil
	}
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		item = resolve(item)
		at := where
		if n.Kind == yaml.SequenceNode {
			at = fmt.Sprintf("%s[%d]", where, i)
		}
		switch {
		case item.Kind != yaml.ScalarNode:
			return nil, p.fail(item, at+" isn't a string", "each entry is "+what)
		case isNull(item) || item.Value == "":
			return nil, p.fail(item, at+" is empty", "each entry is "+what)
		}
		out = append(out, item.Value)
	}
	return out, nil
}

// lints reads lints:, a map from lint name to level, into levels.
func (p *parser) lints(n *yaml.Node, levels map[string]lint.Level) humane.Error {
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return p.fail(n, "lints isn't a map", "set each lint to off, warn or error, such as "+p.syntax.level)
	}
	names := lint.Names()
	type entry struct{ key, value *yaml.Node }
	entries := make([]entry, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		entries = append(entries, entry{n.Content[i], resolve(n.Content[i+1])})
	}
	// Sorted by name, so the first error is the same however the file
	// orders them.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key.Value < entries[j].key.Value })
	for _, e := range entries {
		name := e.key.Value
		if !slices.Contains(names, name) {
			help := "lints: " + strings.Join(names, ", ")
			if near, ok := diag.Nearest(name, names); ok {
				help = fmt.Sprintf("did you mean %q? %s", near, help)
			}
			return p.fail(e.key, fmt.Sprintf("unknown lint %q", name), help)
		}
		if _, ok := levels[name]; ok {
			return p.fail(e.key, "lint "+name+" is set twice", "keep one of the two")
		}
		lv, ok := lint.ParseLevel(e.value.Value)
		if e.value.Kind != yaml.ScalarNode || !ok {
			return p.fail(e.value, fmt.Sprintf("lint %s has unknown level %q", name, e.value.Value), "a lint is off, warn or error")
		}
		levels[name] = lv
	}
	return nil
}

// keyList lists two or more keys the way the file's format writes them,
// such as `kinds:`, `require:` and `lints:`.
func (p *parser) keyList(keys []string) string {
	written := make([]string, len(keys))
	for i, k := range keys {
		written[i] = p.syntax.key(k)
	}
	return and(written)
}

// fail returns an error positioned at n.
func (p *parser) fail(n *yaml.Node, msg string, advice ...string) humane.Error {
	return p.failAt(pos(n), msg, advice...)
}

// failAt returns an error positioned at at.
func (p *parser) failAt(at Pos, msg string, advice ...string) humane.Error {
	return humane.New(fmt.Sprintf("%s:%d:%d: %s", p.file, at.Line, at.Column, msg), advice...)
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == tagNull
}

func pos(n *yaml.Node) Pos {
	return Pos{Line: n.Line, Column: n.Column}
}

// in names where a key is, for messages: " in require[0]", or nothing at
// the top level.
func in(where string) string {
	if where == "" {
		return ""
	}
	return " in " + where
}
