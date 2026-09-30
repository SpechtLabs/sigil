package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"
)

// The formats a configuration file can be written in, by extension.
var (
	yamlSyntax = syntax{
		name:   "YAML",
		decode: decodeYAML,
		key:    func(k string) string { return "`" + k + ":`" },
		entry:  "write each required policy as an entry, such as `- policy: deploy.guardrails`",
		level:  "`gated-deny: error`",
	}
	jsonSyntax = syntax{
		name:   "JSON",
		decode: decodeJSON,
		key:    func(k string) string { return "`\"" + k + "\"`" },
		entry:  "write each required policy as an object, such as `{\"policy\": \"deploy.guardrails\"}`",
		level:  "`\"gated-deny\": \"error\"`",
	}
	tomlSyntax = syntax{
		name:   "TOML",
		decode: decodeTOML,
		key:    func(k string) string { return "`" + k + "`" },
		entry:  "write each required policy as a `[[require]]` table, such as `[[require]]` followed by `policy = \"deploy.guardrails\"`",
		level:  "`gated-deny = \"error\"`",
	}
	syntaxes = map[string]syntax{".yaml": yamlSyntax, ".yml": yamlSyntax, ".json": jsonSyntax, ".toml": tomlSyntax}

	// yamlLine finds the line in a YAML syntax error, which the decoder
	// reports without a column.
	yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)
)

// syntax is what a configuration's format changes: how its source is
// decoded into the YAML nodes the walk reads, and how advice writes keys
// and examples in it.
type syntax struct {
	name   string                                                 // YAML, JSON or TOML
	decode func(p *parser, src []byte) (*yaml.Node, humane.Error) // the root map; nil for an empty file
	key    func(k string) string                                  // how a key is written, quoted for advice
	entry  string                                                 // advice for a require: entry that isn't one
	level  string                                                 // an example of a lint's level, quoted for advice
}

// syntaxOf returns the format path's extension names.
func syntaxOf(path string) (syntax, humane.Error) {
	ext := strings.ToLower(filepath.Ext(path))
	if s, ok := syntaxes[ext]; ok {
		return s, nil
	}
	return syntax{}, humane.New(fmt.Sprintf("the configuration %s isn't a .yaml, .yml, .json or .toml file", path),
		"the extension says which format the file is in; rename it, or name another file with --config")
}

// decodeYAML reads a YAML configuration: one document, whose root the
// walk reads.
func decodeYAML(p *parser, src []byte) (*yaml.Node, humane.Error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, p.invalid(err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, p.fail(&extra, "a second YAML document starts here", "keep one, and move what the others set into it")
	}
	root := resolve(&doc)
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = resolve(root.Content[0])
	}
	return root, nil
}

// invalid is the error for a file the YAML decoder rejects, positioned
// at the line the decoder names, when it names one.
func (p *parser) invalid(err error) humane.Error {
	advice := "fix the YAML syntax; the file is a map that holds " + p.keyList(topKeys)
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		line, _ := strconv.Atoi(m[1])
		return humane.New(fmt.Sprintf("%s:%d: invalid YAML: %s", p.file, line, m[2]), advice)
	}
	return humane.Wrap(err, p.file+" isn't valid YAML", advice)
}

// position returns the line and column of a byte offset in src, clamped
// to src.
func position(src []byte, offset int) Pos {
	offset = max(0, min(offset, len(src)))
	before := src[:offset]
	line := bytes.Count(before, []byte("\n")) + 1
	col := offset - (bytes.LastIndexByte(before, '\n') + 1) + 1
	return Pos{Line: line, Column: col}
}
