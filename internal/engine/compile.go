package engine

import (
	"context"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// checkAdvice is what an op stopped by diagnostics suggests, as the CLI
// does.
const checkAdvice = "fix the errors in the diagnostics; the check op reports every problem in a bundle at once"

// inputHelp describes the input, in advice on one eval can't use.
const inputHelp = "the input is a JSON object with one key per input the kind declares"

// compiled is a policy compile returned a handle to: what eval and
// explain need of it.
type compiled struct {
	kind    *workspace.Kind
	prog    *eval.Policy
	explain workspace.Explanation // the policy flattened, compiled for it with the policy
}

// compiledResp is the compile op's response.
type compiledResp struct {
	envelope
	Policy      string                 `json:"policy"`
	Diagnostics []workspace.Diagnostic `json:"diagnostics"` // always empty, since any error fails the compile; the response keeps the brief's shape
	Handle      uint32                 `json:"handle"`
}

// evaluated is the eval op's response: the `sigil eval -o json` record.
type evaluated struct { //nolint:govet // the field order is the JSON's: the envelope first
	envelope
	*workspace.Report
}

// explained is the explain op's response: the `sigil explain -o json`
// records.
type explained struct {
	envelope
	Explanations []workspace.Explanation `json:"explanations"`
}

// compile answers the compile op the way a host's Kind.Load compiles its
// root: the policy named, or the bundle's only one, in the bundle of every
// document of its kind, with the trusted files behind it. Every document
// is checked, so a broken one fails the compile even when the root never
// uses it, as it fails Load; sigil eval, which narrows to the root and
// what it uses, would evaluate anyway.
func (e *Engine) compile(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	p, err := load(r.Files, nil, r.Trusted)
	if err != nil {
		return nil, err
	}
	p.Check()
	root, err := workspace.Root(p.Policies(), r.Policy)
	if err != nil {
		// The policy asked for may be missing because its document or
		// kind doesn't check.
		if errs := p.Errors(); errs != nil {
			return nil, stopped(p.Resolve(errs), "the bundle doesn't check, so nothing was compiled")
		}
		return nil, err
	}
	// A requirement the host got wrong, such as a required policy its
	// trusted files don't define, is reported before what it causes in
	// the bundle, such as a use of that policy that doesn't resolve.
	g := p.Group(root)
	reqs, err := required(p, g, r)
	if err != nil {
		return nil, err
	}
	if errs := p.Errors(); errs != nil {
		return nil, stopped(p.Resolve(errs), "the bundle doesn't check, so nothing was compiled")
	}
	binding, err := e.bind(g.Kind, r.Functions, r.Stubs)
	if err != nil {
		return nil, err
	}
	prog, errs := g.Bundle.Compile(root, bundle.Options{Binding: binding, Require: reqs})
	static, serrs := g.Bundle.Compile(root, bundle.Options{Static: true})
	if errs == nil {
		errs = serrs // what explain would have found, which eval's compile doesn't look for
	}
	if errs != nil {
		return nil, stopped(p.Resolve(errs), "the policy doesn't compile, so nothing was compiled")
	}
	h := e.handles.keep(&compiled{kind: g.Kind, prog: prog, explain: workspace.Explain(static, g.Bundle)})
	return compiledResp{envelope: env, Handle: h, Policy: root, Diagnostics: []workspace.Diagnostic{}}, nil
}

// evaluate answers the eval op: the compiled policy evaluated against the
// input, as `sigil eval -o json` prints it. A failed evaluation is still
// a result, whose error says why and whose outcome is the fallback.
func (e *Engine) evaluate(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	c, err := e.handles.lookup(r.Handle)
	if err != nil {
		return nil, err
	}
	if r.Input == nil {
		return nil, humane.New("eval needs an input", inputHelp)
	}
	in, derr := c.kind.Binding.DecodeInput(c.kind.Model, r.Input)
	if derr != nil {
		return nil, humane.New("the input: "+derr.Error(), append(derr.Advice(), inputHelp)...)
	}
	ctx := context.Background()
	if r.TimeoutMS > 0 {
		ctx = withDeadline(e.now, e.now().Add(time.Duration(r.TimeoutMS)*time.Millisecond))
	}
	return evaluated{envelope: env, Report: workspace.NewReport(c.kind, result.EvaluateContext(ctx, c.prog, in.Interface()))}, nil
}

// explain answers the explain op: the compiled policy of a handle, or
// the policies of files that the policy pattern matches, every one
// without it, flattened as `sigil explain -o json` prints them.
func (e *Engine) explain(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	switch {
	case r.Handle != 0 && len(r.Files) > 0:
		return nil, humane.New("explain takes a handle or files, not both", "explain a compiled policy by its handle, or the policies of files")
	case r.Handle != 0:
		c, err := e.handles.lookup(r.Handle)
		if err != nil {
			return nil, err
		}
		return explained{envelope: env, Explanations: []workspace.Explanation{c.explain}}, nil
	}
	p, err := load(r.Files, nil, r.Trusted)
	if err != nil {
		return nil, err
	}
	p.Check()
	roots, err := explainRoots(p.Policies(), r.Policy)
	if err != nil {
		// Without roots there's no scope, and a policy may be missing
		// because its document or kind doesn't check.
		if errs := p.Errors(); errs != nil {
			return nil, stopped(p.Resolve(errs), "the bundle doesn't check, so nothing was explained")
		}
		return nil, err
	}
	s := p.ScopeOf(roots)
	if errs := s.Keep(p.Errors()); errs != nil {
		return nil, stopped(errs, "the policies to explain don't check, so nothing was explained")
	}
	out := make([]workspace.Explanation, 0, len(roots))
	for _, root := range roots {
		b := s.Bundle(p.Group(root))
		prog, errs := b.Compile(root, bundle.Options{Static: true})
		if errs != nil {
			return nil, stopped(p.Resolve(errs), "policy "+root+" doesn't compile, so it wasn't explained")
		}
		out = append(out, workspace.Explain(prog, b))
	}
	return explained{envelope: env, Explanations: out}, nil
}

// explainRoots picks the policies to explain, as `sigil explain` does:
// every one, the one named, or the ones matching a pattern.
func explainRoots(policies []string, pattern string) ([]string, humane.Error) {
	if pattern == "" {
		if len(policies) == 0 {
			return nil, humane.New("the bundle holds no policies", "send a file that holds one")
		}
		return policies, nil
	}
	return workspace.Match(policies, []string{pattern})
}

// stopped is the error of an op the diagnostics stopped.
func stopped(errs diag.ErrorList, msg string) *blocked {
	return &blocked{msg: msg, advice: []string{checkAdvice}, diags: workspace.Diagnostics(errs)}
}
