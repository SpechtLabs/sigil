package policy

import (
	"io/fs"
	"maps"
)

// Params binds the root policy's params from Go, by name, for example
// from a CRD or a config file. It is a [LoadOption]; several Params
// merge, a later value winning for the same name.
//
// Every value is type-checked against the param's declaration when the
// policy is compiled, like an invocation's arguments would be, and checked
// against its `min` and `max`. A value is a Go value of the shape
// [NewKind] accepts for the param's type: a string for `string`, a
// []string for `list<string>`, a [time.Duration] for `duration`, and so
// on. A name the root doesn't declare, a value of the wrong type or out
// of bounds, and a param without a default that Params leaves unbound are
// all compile errors.
//
//	Deploy.Compile(src, "deploy.gate", policy.Params{
//		"approvers": []string{"payments-leads"},
//		"min_soak":  4 * time.Hour,
//	})
type Params map[string]any //nolint:emptyinterface // values are the host's Go values, checked against the param's type

// LoadOption configures [Kind.Compile] and [Kind.Load]. The load options
// are [Params], [Require] and [Trusted].
type LoadOption interface {
	apply(*loadOptions)
}

// RequireOption configures [Require]. The only one is [From].
type RequireOption interface {
	applyRequire(*requirement)
}

type loadOptions struct {
	params   Params
	requires []requirement
	trusted  []fs.FS // the sources Trusted names, in order
}

// requirement is one required policy and, with From, the source it
// must come from.
type requirement struct {
	from     fs.FS
	name     string
	trusting bool // From was given, even with a nil source
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
// the path. A gated or missing invocation fails the load with a
// [*CompileError] pointing at the gated call or at the root's header. The
// requirement is transitive: a shared baseline policy that invokes the
// required one at its top level satisfies it for every root that invokes
// the baseline at theirs.
//
// Require checks that a policy of that name is invoked, not which one. On
// its own it looks the name up in the bundle, so anyone who can write to
// the bundle can satisfy it with an empty policy. Pass [From] whenever
// someone other than the policy's owner can write to the bundle.
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

// From names the trusted source a required policy must come from, such as
// an [embed.FS] built into the host or a separately mounted ConfigMap.
// The source is loaded as its own bundle: the required policy, and
// everything it imports and invokes, resolve there and never in the bundle
// passed to [Kind.Load], and a document in that bundle that takes a name
// the source defines is a compile error, unless it's a byte-for-byte
// copy of that document. A nil source, or one that holds no policy or
// module, fails the load.
//
// Several requirements may name the same source, which is then read once.
// Sources are the same when they compare equal, as [embed.FS] and
// [os.DirFS] values do, or, for a map-backed source such as [MapFS], when
// they are the same map. Two values that aren't the same but hold the
// same files, such as two [io/fs.Sub] calls for one directory, are each
// read; the second one's documents are copies, and are left out.
func From(fsys fs.FS) RequireOption { return fromOption{fsys} }

type fromOption struct{ fsys fs.FS }

func (o fromOption) applyRequire(r *requirement) { r.from, r.trusting = o.fsys, true }

// Trusted adds a trusted source without requiring a policy from it, such
// as the vocabulary modules a platform ships for team policies to import.
// It's read into the same trusted bundle as the sources [From] names: its
// documents resolve before the bundle's, and a document in the bundle
// passed to [Kind.Load] that takes a name it defines is a compile error.
// Every document in it is checked, so a broken one fails the load even
// when nothing uses it. A nil source, or one that holds no policy or
// module, such as the wrong directory of an [io/fs.Sub], fails the load:
// it would protect nothing.
//
// A document in the bundle that's a byte-for-byte copy of a trusted one
// is the same definition, and is left out rather than reported. So the
// trusted source may be a directory of the bundle's own fs.FS, as in a
// repository that holds the platform's directory and the teams':
//
//	platform, err := fs.Sub(repo, "platform")
//	// ...
//	p, err := Deploy.Load(repo, "payments.production", policy.Trusted(platform))
//
// A copy that differs from the trusted document is a compile error. The
// CLI reads a file under both a path and a --trusted path as trusted
// only; a copy at another path is an error there.
//
// Trusted may be repeated, and may name a source a [From] names too,
// which is then read once; sources are the same as [From] compares them.
// Two sources, or a source and the bundle, may hold files of the same
// path: every diagnostic quotes the file it's about. A required policy
// without From that a trusted source defines comes from there, as it
// would with From.
//
//	Deploy.Load(teamFS, "payments.production", policy.Trusted(vocabularyFS))
func Trusted(fsys fs.FS) LoadOption { return trustedOption{fsys} }

type trustedOption struct{ fsys fs.FS }

func (o trustedOption) apply(lo *loadOptions) { lo.trusted = append(lo.trusted, o.fsys) }
