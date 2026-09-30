package config

import (
	"fmt"
	"os"
	"slices"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// Apply reads the configuration as [Load] does and adds to src what eval,
// explain and test read besides their paths: the kind files of kinds:,
// after the ones --kind names, and the trusted: paths of require: as
// trusted sources. A trusted file that's also among the paths stays one
// of the paths, so a run over the whole repository reads what it read
// before, and a run over one team's directory still finds the required
// policies its policies use, and everything those use.
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
	trusted, err := c.Trusted(nil, c.Require)
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
			return nil, humane.Wrap(err, c.File+": the kind file "+k+" can't be read", "kinds: lists kind files relative to "+FileName)
		}
	}
	return c.Kinds, nil
}

// Trusted returns the paths to read as trusted: have, then the trusted:
// paths of reqs that have doesn't hold, each once. A path an entry of
// the file names must exist, and one that doesn't is an error at the
// entry, where the path to fix is.
func (c *Config) Trusted(have []string, reqs []Require) ([]string, humane.Error) {
	out := slices.Clone(have)
	for _, r := range reqs {
		for _, path := range r.Trusted {
			if slices.Contains(out, path) {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				return nil, humane.Wrap(err, fmt.Sprintf("%s: the trusted path %s of %s can't be read", c.At(r.Pos), path, r.Policy), "trusted: paths are relative to "+FileName)
			}
			out = append(out, path)
		}
	}
	return out, nil
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
