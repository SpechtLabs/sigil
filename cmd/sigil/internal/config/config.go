// Package config reads the configuration file a policy repository keeps
// at its root to configure the tools. It's named sigil.yaml, sigil.json
// or sigil.toml, or the same with a leading dot to keep it out of
// directory listings, and holds the same keys in every format:
//
//	kinds:
//	  - ../vendor/deploy_approval.sigil
//	trusted:
//	  - platform/vocabulary
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
// trusted lists files and directories read as trusted, as --trusted
// reads them: no other document may take a name their documents define.
// require lists the policies check enforces: each names a policy, the
// files and directories to read it from, and the name patterns of the
// policies it applies to. Paths are relative to the directory the file
// is in, so it means the same from wherever a command runs. lints sets
// each lint to off, warn or error; lints it doesn't name keep their
// defaults.
//
// Unknown keys, lints and levels are errors, with the line and column
// they're at and the nearest known name, so a typo can't silently leave a
// setting at its default. A command looks for the file in the working
// directory and then in each parent, and uses the nearest directory that
// has one. Two in the same directory are an error, since neither would
// be the obvious one to edit.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/lint"
)

// Names lists the configuration file's names, the way help and messages
// do.
const Names = "sigil.yaml, sigil.json or sigil.toml"

// FileNames are the names a directory's configuration file can have, in
// the order messages list them.
var FileNames = []string{"sigil.yaml", "sigil.json", "sigil.toml", ".sigil.yaml", ".sigil.json", ".sigil.toml"}

// Config is a repository's configuration.
type Config struct {
	Lints   map[string]lint.Level // the level of each lint the file names; the others keep their defaults
	Kinds   []string              // kind files every policy command loads, resolved against the file's directory
	Trusted []string              // files and directories read as trusted, resolved against the file's directory
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

// Load reads the configuration: from path when it's set, and otherwise
// from the nearest configuration file at or above dir. Its extension
// picks the format: .yaml or .yml, .json or .toml. Without a file, every
// lint keeps its default, and there are no kind files or requirements.
func Load(path, dir string) (*Config, humane.Error) {
	if path == "" {
		found, err := find(dir)
		if err != nil || found == "" {
			return &Config{}, err
		}
		path = found
	}
	if _, err := syntaxOf(path); err != nil {
		return nil, err
	}
	src, err := os.ReadFile(path) //nolint:gosec // the path comes from the command line or the repository, which is the point
	if err != nil {
		return nil, humane.Wrap(err, "the configuration "+path+" couldn't be read", "check the path given with --config")
	}
	return Parse(path, src)
}

// Parse reads a configuration's source, in the format path's extension
// names. path is where it came from: messages name it, and relative
// paths in it resolve against its directory. Unknown keys, lints and
// levels are errors, so a typo can't silently leave a setting at its
// default.
func Parse(path string, src []byte) (*Config, humane.Error) {
	s, err := syntaxOf(path)
	if err != nil {
		return nil, err
	}
	p := &parser{file: path, dir: filepath.Dir(path), syntax: s}
	root, err := s.decode(p, src)
	if err != nil {
		return nil, err
	}
	return p.config(root)
}

// Find returns the configuration file of the nearest directory at or
// above dir that has one, or "" when none does: the file [Load] reads
// without a path. A directory with two is an error.
func Find(dir string) (string, humane.Error) { return find(dir) }

// Requirements returns what check enforces in a run: one requirement per
// --require policy, each with the --trusted paths and the --policy
// patterns, when --require is given, and otherwise the configuration's
// require:. --require replaces the file's requirements rather than
// adding to them, so a run can check one requirement on its own.
// --policy without it keeps the file's requirements, since it only
// narrows what check covers, and so does --trusted, which adds trusted
// paths without requiring anything.
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

// Base returns the configuration file's name without its directory, for
// messages that say what paths are relative to. It's "the configuration
// file" for the defaults.
func (c *Config) Base() string {
	if c.File == "" {
		return "the configuration file"
	}
	return filepath.Base(c.File)
}

// find returns the configuration file of the nearest directory at or
// above dir that has one, or "". A directory with two is an error. A
// file in the working directory or one of its parents is returned
// relative to it, such as ../../sigil.yaml, so messages and the paths
// resolved against it read the way the command line does; any other is
// absolute.
func find(dir string) (string, humane.Error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", nil
	}
	for {
		var found []string
		for _, name := range FileNames {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				found = append(found, name)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", humane.Wrap(err, relative(p)+" can't be read", "check the permissions of its directory")
			}
		}
		switch len(found) {
		case 0:
		case 1:
			return relative(filepath.Join(dir, found[0])), nil
		default:
			where := relative(dir)
			if where == "." {
				where = "the working directory"
			}
			quantity := "all"
			if len(found) == 2 {
				quantity = "both"
			}
			return "", humane.New(fmt.Sprintf("%s in %s are %s configuration files", and(found), where, quantity),
				"keep one of them; a command reads one configuration file, the nearest")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
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

// and joins two or more items the way messages list them: "a, b and c".
func and(items []string) string {
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
