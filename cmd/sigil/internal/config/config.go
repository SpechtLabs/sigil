// Package config reads sigil.yaml, the file a policy repository keeps at
// its root to configure the tools:
//
//	kinds:
//	  - ../vendor/deploy_approval.sigil
//	require:
//	  - policy: deploy.guardrails
//	    trusted: [platform/deploy]
//	    roots: ["payments.*", "checkout.*"]
//	lints:
//	  gated-deny: error
//	  qualified-imports: warn
//
// kinds lists kind files that live outside the paths a command reads,
// which every policy command loads as if they were named with --kind.
// require lists the policies check enforces: each names a policy, the
// files and directories to read it from, and the name patterns of the
// policies it applies to. Paths are relative to the directory sigil.yaml
// is in, so the file means the same from wherever a command runs. lints
// sets each lint to off, warn or error; lints it doesn't name keep their
// defaults.
//
// Unknown keys, lints and levels are errors, with the line and column
// they're at and the nearest known name, so a typo can't silently leave a
// setting at its default. A command looks for the file in the working
// directory and then in each parent, and uses the first one it finds.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
)

// FileName is the configuration file's name.
const FileName = "sigil.yaml"

// The keys a configuration and each of its require entries hold, in the
// order messages list them.
var (
	topKeys     = []string{"kinds", "require", "lints"}
	requireKeys = []string{"policy", "trusted", "roots"}
)

// Config is a repository's configuration.
type Config struct {
	Lints   map[string]lint.Level // the level of each lint the file names; the others keep their defaults
	Kinds   []string              // kind files every policy command loads, resolved against the file's directory
	Require []Require             // the policies check enforces, in the order the file lists them
	File    string                // where it was read from; empty for the defaults
}

// Require is one policy check enforces, as an entry of require: lists
// it.
type Require struct {
	Policy  string   // the required policy's name
	Trusted []string // files and directories to read it from, resolved against the file's directory; empty reads it from the paths
	Roots   []string // name patterns of the policies it applies to; empty for every root of its kind that no other policy invokes
	Pos     Pos      // where the entry's policy: is written; zero for a requirement from the command line
}

// Pos is a position in the configuration file, 1-based.
type Pos struct {
	Line, Column int
}

// parser walks a configuration's YAML nodes, and positions its errors in
// file.
type parser struct {
	file string
	dir  string // what relative paths resolve against
}

// Load reads the configuration: from path when it's set, and otherwise
// from the nearest sigil.yaml at or above dir. Without one, every lint
// keeps its default, and there are no kind files or requirements.
func Load(path, dir string) (*Config, humane.Error) {
	if path == "" {
		path = find(dir)
		if path == "" {
			return &Config{}, nil
		}
	}
	src, err := os.ReadFile(path) //nolint:gosec // the path comes from the command line or the repository, which is the point
	if err != nil {
		return nil, humane.Wrap(err, "the configuration "+path+" couldn't be read", "check the path given with --config")
	}
	return Parse(path, src)
}

// Parse reads a configuration's source. path is where it came from:
// messages name it, and relative paths in it resolve against its
// directory. Unknown keys, lints and levels are errors, so a typo can't
// silently leave a setting at its default.
func Parse(path string, src []byte) (*Config, humane.Error) {
	p := &parser{file: path, dir: filepath.Dir(path)}
	c := &Config{File: path, Lints: map[string]lint.Level{}}
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return c, nil
		}
		return nil, humane.Wrap(err, path+" isn't valid YAML", "sigil.yaml is a map that holds "+keyList(topKeys))
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, p.fail(&extra, path+" holds more than one YAML document", "keep one, and move what the others set into it")
	}
	root := resolve(&doc)
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = resolve(root.Content[0])
	}
	if isNull(root) {
		return c, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, p.fail(root, "the configuration isn't a map", "sigil.yaml holds "+keyList(topKeys))
	}
	err := p.mapping(root, "", topKeys, "sigil.yaml holds "+keyList(topKeys), func(key string, n *yaml.Node) humane.Error {
		var err humane.Error
		switch key {
		case "kinds":
			c.Kinds, err = p.paths(n, "kinds", "a kind file")
		case "require":
			c.Require, err = p.require(n)
		case "lints":
			err = p.lints(n, c.Lints)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Requirements returns what check enforces in a run: one requirement per
// --require policy, each with the --trusted paths and the --policy
// patterns, when --require is given, and otherwise the configuration's
// require:. --require replaces the file's requirements rather than
// adding to them, so a run can check one requirement on its own.
// --policy without it keeps the file's requirements, since it only
// narrows what check covers, and so does --trusted, which check rejects
// without --require.
func (c *Config) Requirements(policies, trusted, roots []string) []Require {
	if len(policies) == 0 {
		return c.Require
	}
	out := make([]Require, len(policies))
	for i, name := range policies {
		out[i] = Require{Policy: name, Trusted: trusted, Roots: roots}
	}
	return out
}

// At returns where pos is in the configuration file, as file:line:column
// for messages. It's the file alone for the zero Pos.
func (c *Config) At(pos Pos) string {
	if pos.Line == 0 {
		return c.File
	}
	return fmt.Sprintf("%s:%d:%d", c.File, pos.Line, pos.Column)
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
	const example = "write each required policy as an entry, such as `- policy: deploy.guardrails`"
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
		err := p.mapping(entry, where, requireKeys, "a require entry holds "+keyList(requireKeys), func(key string, v *yaml.Node) humane.Error {
			var err humane.Error
			switch key {
			case "policy":
				named = true
				r.Pos = pos(v)
				r.Policy, err = p.policy(v, where+".policy")
			case "trusted":
				r.Trusted, err = p.paths(v, where+".trusted", "a file or directory")
			case "roots":
				r.Roots, err = p.list(v, where+".roots", "a policy name or pattern, such as \"payments.*\"")
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		if !named {
			return nil, p.fail(entry, where+" names no policy", "add `policy:` with the name of the policy check enforces")
		}
		if at, ok := first[r.Policy]; ok {
			return nil, p.failAt(r.Pos, r.Policy+" is required twice", fmt.Sprintf("it's first required at line %d; merge the two entries' trusted: and roots:", at.Line))
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
		return "", p.fail(n, fmt.Sprintf("%s %q is a pattern", where, n.Value), "name one policy; roots: takes the patterns of the policies it applies to")
	}
	return n.Value, nil
}

// paths reads a list of paths, or a single one, and resolves each
// against the configuration's directory. what describes an entry.
func (p *parser) paths(n *yaml.Node, where, what string) ([]string, humane.Error) {
	out, err := p.list(n, where, what+", relative to sigil.yaml")
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
		return p.fail(n, "lints isn't a map", "set each lint to off, warn or error, such as `gated-deny: error`")
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

// fail returns an error positioned at n.
func (p *parser) fail(n *yaml.Node, msg string, advice ...string) humane.Error {
	return p.failAt(pos(n), msg, advice...)
}

// failAt returns an error positioned at at.
func (p *parser) failAt(at Pos, msg string, advice ...string) humane.Error {
	return humane.New(fmt.Sprintf("%s:%d:%d: %s", p.file, at.Line, at.Column, msg), advice...)
}

// find returns the nearest sigil.yaml at or above dir, or "". A file in
// the working directory or one of its parents is returned relative to it,
// such as ../../sigil.yaml, so messages and the paths resolved against it
// read the way the command line does; any other is absolute.
func find(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, FileName)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return relative(p)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// relative returns p relative to the working directory when it is in
// the working directory or above it, and p otherwise.
func relative(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(wd, p)
	if err != nil {
		return p
	}
	for d := filepath.Dir(rel); d != "."; d = filepath.Dir(d) {
		if filepath.Base(d) != ".." {
			return p
		}
	}
	return rel
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
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

// keyList lists two or more keys the way advice does: `kinds:`,
// `require:` and `lints:`.
func keyList(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = "`" + k + ":`"
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
