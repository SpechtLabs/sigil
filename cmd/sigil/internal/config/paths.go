package config

import (
	"fmt"
	"os"
	"slices"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// noSigil is the advice for a trusted path that holds no .sigil file.
const noSigil = "a trusted path is a .sigil file, or a directory with .sigil files below it; one with none would protect nothing, so check the path"

// Apply reads the configuration as [Load] does and adds to src what eval,
// explain and test read besides their paths: the kind files of kinds:,
// after the ones --kind names, and the trusted: paths, top-level and of
// require:, as trusted sources. A trusted file that's also among the
// paths stays one of the paths, so a run over the whole repository reads
// what it read before, and a run over one team's directory still finds
// the trusted documents its policies use, and everything those use.
func Apply(path, dir string, src *project.Sources) humane.Error {
	c, err := Load(path, dir)
	if err != nil {
		return err
	}
	kinds, err := c.KindFiles()
	if err != nil {
		return err
	}
	src.Kinds = append(src.Kinds, kinds...)
	trusted, err := c.TrustedPaths(nil, c.Require)
	if err != nil {
		return err
	}
	src.Trusted = append(src.Trusted, outside(trusted, src.Paths)...)
	return nil
}

// KindFiles returns the kind files kinds: lists, for a policy command to
// load after the ones --kind names, once it has checked that each one
// exists: a missing one is reported as the configuration's, where the
// path to fix is, rather than as a kind file from the command line.
func (c *Config) KindFiles() ([]string, humane.Error) {
	for _, k := range c.Kinds {
		if _, err := os.Stat(k); err != nil {
			return nil, humane.Wrap(err, c.File+": the kind file "+k+" can't be read", "kinds lists kind files relative to "+c.Base())
		}
	}
	return c.Kinds, nil
}

// TrustedPaths returns the paths to read as trusted: have, then the
// file's trusted: paths, then the trusted: paths of reqs, each once. A
// trusted path must hold a .sigil file, since one that holds none would
// protect nothing; have names the --trusted paths, and one that doesn't
// is an error naming the flag. A path the file names must also exist;
// one that doesn't, or holds no .sigil file, is an error at the require:
// entry that names it, or at the file for the top-level trusted:, where
// the path to fix is.
func (c *Config) TrustedPaths(have []string, reqs []Require) ([]string, humane.Error) {
	for _, path := range have {
		if empty(path) {
			return nil, humane.New("--trusted "+path+" holds no .sigil files", noSigil)
		}
	}
	out := slices.Clone(have)
	add := func(path, where, advice string) humane.Error {
		if slices.Contains(out, path) {
			return nil
		}
		if _, err := os.Stat(path); err != nil {
			return humane.Wrap(err, where+" can't be read", advice)
		}
		if empty(path) {
			return humane.New(where+" holds no .sigil files", noSigil, advice)
		}
		out = append(out, path)
		return nil
	}
	for _, path := range c.Trusted {
		if err := add(path, c.File+": the trusted path "+path, "trusted lists files and directories relative to "+c.Base()); err != nil {
			return nil, err
		}
	}
	for _, r := range reqs {
		for _, path := range r.Trusted {
			if err := add(path, fmt.Sprintf("%s: the trusted path %s of %s", c.At(r.Pos), path, r.Policy), "trusted paths are relative to "+c.Base()); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// empty reports whether path is a directory with no .sigil file below
// it, by [project.Expand]'s rules. A path that can't be read isn't, for
// the loader to report.
func empty(path string) bool {
	files, err := project.Expand([]string{path}, project.IsSigil)
	return err == nil && len(files) == 0
}

// outside returns the files below extra that paths don't hold, by
// [project.Expand]'s rules. When either can't be read it returns extra
// as it is, for the loader to report.
func outside(extra, paths []string) []string {
	if len(extra) == 0 {
		return nil
	}
	files, err := project.Expand(extra, project.IsSigil)
	held, herr := project.Expand(paths, project.IsSigil)
	if err != nil || herr != nil {
		return extra
	}
	in := map[string]bool{}
	for _, f := range held {
		in[f] = true
	}
	return slices.DeleteFunc(files, func(f string) bool { return in[f] })
}
