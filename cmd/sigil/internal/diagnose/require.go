package diagnose

import (
	"path/filepath"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// configure reads the configuration, from configFile or the nearest
// configuration file, and applies it to src: its kind files, its
// trusted: paths after the --trusted ones, and the trusted paths of the
// requirements the run enforces, which it returns: the flags' when
// --require is given, and otherwise the file's.
func configure(configFile string, src *project.Sources, patterns, requires []string) (*config.Config, []config.Require, humane.Error) {
	cfg, err := config.Load(configFile, ".")
	if err != nil {
		return nil, nil, err
	}
	kinds, err := cfg.KindFiles()
	if err != nil {
		return nil, nil, err
	}
	src.Kinds = append(src.Kinds, kinds...)
	reqs := cfg.Requirements(requires, src.Trusted, patterns)
	if src.Trusted, err = cfg.TrustedPaths(src.Trusted, reqs); err != nil {
		return nil, nil, err
	}
	return cfg, reqs, nil
}

// requirements turns the requirements the run enforces into the ones
// workspace enforces, each from the configuration file named at its
// entry, so messages point there.
func requirements(cfg *config.Config, reqs []config.Require) []workspace.Requirement {
	out := make([]workspace.Requirement, len(reqs))
	for i, r := range reqs {
		out[i] = workspace.Requirement{Policy: r.Policy, Trusted: r.Trusted, Roots: r.Roots}
		if r.Pos.Line > 0 {
			out[i].Where = cfg.At(r.Pos)
		}
	}
	return out
}

// whole reports whether the run reads the whole repository the
// configuration file configures: one of the paths is the file's directory
// or above it. A run that reads part of it, such as one team's directory,
// doesn't hold every policy the file's roots: name.
func whole(cfg *config.Config, paths []string) bool {
	dir, err := filepath.Abs(filepath.Dir(cfg.File))
	if err != nil {
		return true
	}
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil || path == "-" {
			continue
		}
		if rel, err := filepath.Rel(abs, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}
