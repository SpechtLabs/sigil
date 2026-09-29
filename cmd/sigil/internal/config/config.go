// Package config reads sigil.yaml, the file a policy repository keeps at
// its root to configure the tools. It sets the level of each lint:
//
//	lints:
//	  gated-deny: error
//	  qualified-imports: warn
//
// Each lint is set to off, warn or error; lints it doesn't name keep their
// defaults. A command looks for the file in the working directory and
// then in each parent, and uses the first one it finds.
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

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/lint"
)

// FileName is the configuration file's name.
const FileName = "sigil.yaml"

// Config is a repository's configuration.
type Config struct {
	Lints map[string]lint.Level // the level of each lint the file names; the others keep their defaults
	File  string                // where it was read from; empty for the defaults
}

// file is the configuration as written.
type file struct {
	Lints map[string]string `yaml:"lints"`
}

// Load reads the configuration: from path when it's set, and otherwise
// from the nearest sigil.yaml at or above dir. Without one, every lint
// keeps its default.
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

// Parse reads a configuration's source. Unknown keys, lints and levels
// are errors, so a typo can't silently leave a lint at its default.
func Parse(path string, src []byte) (*Config, humane.Error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var f file
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, humane.Wrap(err, path+" isn't a valid configuration", "sigil.yaml holds `lints:`, a map from lint name to off, warn or error")
	}
	c := &Config{File: path, Lints: map[string]lint.Level{}}
	names := lint.Names()
	keys := make([]string, 0, len(f.Lints))
	for k := range f.Lints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if !slices.Contains(names, name) {
			help := "lints: " + strings.Join(names, ", ")
			if near, ok := check.Nearest(name, names); ok {
				help = fmt.Sprintf("did you mean %q? %s", near, help)
			}
			return nil, humane.New(fmt.Sprintf("%s: unknown lint %q", path, name), help)
		}
		lv, ok := lint.ParseLevel(f.Lints[name])
		if !ok {
			return nil, humane.New(fmt.Sprintf("%s: lint %s has unknown level %q", path, name, f.Lints[name]), "a lint is off, warn or error")
		}
		c.Lints[name] = lv
	}
	return c, nil
}

// find returns the nearest sigil.yaml at or above dir, or "".
func find(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, FileName)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
