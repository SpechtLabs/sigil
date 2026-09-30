package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lint"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// checked is the check op's response.
type checked struct {
	envelope
	Diagnostics []workspace.Diagnostic `json:"diagnostics"`
}

// check answers the check op: what `sigil check -o json` prints for the
// same files, with the requirements and lint levels of a configuration
// file that configures all of them. The diagnostics may hold errors: the
// check itself succeeded.
func (e *Engine) check(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	levels, err := lintLevels(r.Lints)
	if err != nil {
		return nil, err
	}
	trusted, err := trustedPaths(append(slices.Clone(r.Files), r.Trusted...), r.Require)
	if err != nil {
		return nil, err
	}
	p, err := load(r.Files, trusted, r.Trusted)
	if err != nil {
		return nil, err
	}
	reqs := make([]workspace.Requirement, len(r.Require))
	for i, req := range r.Require {
		if req.Policy == "" {
			return nil, humane.New(where(i)+" names no policy", `give each requirement the policy it requires, as {"policy": "deploy.guardrails"}`)
		}
		reqs[i] = workspace.Requirement{Policy: req.Policy, Where: where(i), Trusted: cleaned(req.Trusted), Roots: req.Roots}
	}
	// The request holds every file the requirements can name, as a run
	// over a whole repository does, so the check is strict.
	errs, err := p.Diagnose(workspace.Checks{Lints: levels, Patterns: r.Policies, Require: reqs, Strict: true})
	if err != nil {
		return nil, err
	}
	return checked{envelope: env, Diagnostics: workspace.Diagnostics(p.Resolve(errs))}, nil
}

// lintLevels reads the lint levels of a request, rejecting an unknown
// lint or level as the configuration file does.
func lintLevels(lints map[string]string) (map[string]lint.Level, humane.Error) {
	levels := make(map[string]lint.Level, len(lints))
	for _, name := range slices.Sorted(maps.Keys(lints)) {
		if !slices.Contains(lint.Names(), name) {
			advice := []string{"the lints are " + strings.Join(lint.Names(), ", ")}
			if near, ok := diag.Nearest(name, lint.Names()); ok {
				advice = append([]string{fmt.Sprintf("did you mean %q?", near)}, advice...)
			}
			return nil, humane.New(fmt.Sprintf("lints: unknown lint %q", name), advice...)
		}
		level, ok := lint.ParseLevel(lints[name])
		if !ok {
			return nil, humane.New(fmt.Sprintf("lints: %s: unknown level %q", name, lints[name]), "set a lint to off, warn or error")
		}
		levels[name] = level
	}
	return levels, nil
}
