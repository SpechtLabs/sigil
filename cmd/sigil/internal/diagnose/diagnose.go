// Package diagnose is the check `sigil check` runs, shared with `sigil
// compile`, which runs the same check on a bundle before it compiles it.
//
// [Run] reads the configuration file, adds its kind files and the trusted
// paths of the requirements it enforces to the sources, reads and loads
// the project, and diagnoses it: the requirements, the compiles and the
// lints, strict about roots: when the run reads the whole repository the
// configuration file configures. The commands report what it found in
// their own way.
package diagnose

import (
	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// Result is what one check read and found.
type Result struct {
	Config  *config.Config   // the configuration the check read
	Require []config.Require // the requirements it enforced: --require's, or the configuration's require:
	Files   *project.Files   // the files it read, the configuration's kind files and trusted paths included
	Project *project.Project // the files, loaded
	Errs    diag.ErrorList   // what it found, unresolved; nil when nothing
}

// Run checks the project src names, with the configuration configFile
// names or the nearest configuration file: every document, or with
// patterns the policies they match and what those use. With no paths it
// reads the working directory. It enforces the requirements requires
// names, read from src.Trusted, or the configuration's require: without
// them, and runs the lints at the configuration's levels. kinds are the
// kinds linked into the binary. Only what stops the check from running,
// such as a configuration file or a path that can't be read, is an error;
// what the check finds is in the result.
func Run(configFile string, src project.Sources, patterns, requires []string, kinds []project.Linked) (*Result, humane.Error) {
	if len(src.Paths) == 0 {
		src.Paths = []string{"."}
	}
	cfg, reqs, err := configure(configFile, &src, patterns, requires)
	if err != nil {
		return nil, err
	}
	files, err := project.Read(src)
	if err != nil {
		return nil, err
	}
	p, err := project.LoadFiles(files, kinds)
	if err != nil {
		return nil, err
	}
	errs, err := p.Diagnose(workspace.Checks{Lints: cfg.Lints, Patterns: patterns, Require: requirements(cfg, reqs), Strict: whole(cfg, src.Paths)})
	if err != nil {
		return nil, err
	}
	return &Result{Config: cfg, Require: reqs, Files: files, Project: p, Errs: errs}, nil
}

// Advice is what a failed check suggests: the general hint, and when an
// error sits in a kind document, how to fix a kind file, including the
// rewrite of the decision syntax the language dropped.
func Advice(p *project.Project, diags diag.ErrorList, general string) []string {
	for _, d := range diags {
		if d.Severity == diag.SeverityError && p.InKind(d) {
			return []string{general, "fix the kind file, or regenerate it from the host's Schema(); `sigil fmt --write` rewrites the old decision syntax"}
		}
	}
	return []string{general}
}
