package policy

import (
	"io/fs"
	"maps"
)

// Params binds the root policy's params from Go, by name. Every value
// is type-checked against the param's declaration when the policy is
// compiled, like an invocation's arguments would be, and checked against
// its `min` and `max`. A value is a Go value of the shape NewKind
// accepts for the param's type: a string for `string`, a []string for
// `list<string>`, a time.Duration for `duration`, and so on.
//
//	Deploy.Compile(src, "deploy.gate", policy.Params{
//		"approvers": []string{"payments-leads"},
//		"min_soak":  4 * time.Hour,
//	})
type Params map[string]any //nolint:emptyinterface // values are the host's Go values, checked against the param's type

// LoadOption configures Compile and Load.
type LoadOption interface {
	apply(*loadOptions)
}

// RequireOption configures Require.
type RequireOption interface {
	applyRequire(*requirement)
}

type loadOptions struct {
	params   Params
	requires []requirement
}

// requirement is one required policy and, with From, the source it
// must come from.
type requirement struct {
	from fs.FS
	name string
}

// required lists the required policies' names.
func (o *loadOptions) required() []string {
	names := make([]string, len(o.requires))
	for i, r := range o.requires {
		names[i] = r.name
	}
	return names
}

func (p Params) apply(o *loadOptions) {
	maps.Copy(o.params, p)
}

// Require names a policy the root must invoke unconditionally: reachable
// from the root through top-level invocations only, with no `when` on
// the path. A gated or missing invocation fails the load. With From, the
// policy and everything it uses are read from a trusted source.
//
//	Deploy.Load(policies, "payments.production",
//		policy.Require("deploy.guardrails", policy.From(platformFS)))
func Require(name string, opts ...RequireOption) LoadOption {
	r := requirement{name: name}
	for _, opt := range opts {
		opt.applyRequire(&r)
	}
	return requireOption{r}
}

type requireOption struct{ r requirement }

func (o requireOption) apply(lo *loadOptions) { lo.requires = append(lo.requires, o.r) }

// From names the source a required policy must come from. The source is
// loaded as its own bundle: the required policy, and everything it
// imports and invokes, resolve there and never in the bundle passed to
// Load, and a document in that bundle that takes a name the source
// defines is a compile error.
func From(fsys fs.FS) RequireOption { return fromOption{fsys} }

type fromOption struct{ fsys fs.FS }

func (o fromOption) applyRequire(r *requirement) { r.from = o.fsys }
