// Package policy embeds Sigil in a Go program: define a kind from Go
// types, compile policies against it, and evaluate them to typed
// decisions.
//
// Sigil is a small, statically typed policy language. A policy is a set of
// `when` rules that each construct a decision, such as `approve`, `deny` or
// `review`, with a mandatory reason and a typed payload. Every policy is
// type-checked against a contract, the kind, which the host defines here in
// Go. The language itself is described at https://sigil.specht-labs.de/.
//
// The API mirrors [regexp]: define a kind once at package level, compile
// policies once, and evaluate them many times from any goroutine.
//
// # Defining a kind
//
// A kind starts as ordinary Go structs. The input struct's tagged fields
// become the kind's inputs, and nested structs become its types. Untagged
// fields are invisible to policies.
//
//	type Input struct {
//		Service     Service `policy:"service"`
//		Actor       Actor   `policy:"actor"`
//		Environment string  `policy:"environment"`
//	}
//
//	type Service struct {
//		Name   string            `policy:"name"`
//		Tier   string            `policy:"tier"`
//		Owners []string          `policy:"owners"`
//		Labels map[string]string `policy:"labels"`
//	}
//
// Each decision has a payload struct, or [None] when it carries only a
// reason. A payload field may declare a default after its name, as a Sigil
// constant of the field's type; payload fields are the only ones that take
// it. [NewDecision] declares the decision with the reasons policies may
// give it:
//
//	type ApproveData struct {
//		Bake time.Duration `policy:"bake,default=1h"`
//	}
//
//	var (
//		Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "no_rule_matched")
//		Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "service_owner")
//	)
//
// Go code names a reason through a handle, an [Outcome] from
// [Decision.Reason], so each reason is spelled as a string once. Reason
// panics on a name the decision doesn't declare, with a did-you-mean
// hint, so a typo stops the program at init instead of compiling into a
// comparison that never matches:
//
//	var NoRuleMatched = Deny.Reason("no_rule_matched")
//
// [NewKind] ties the input struct, the decisions and the host functions
// together. Each [Option] corresponds to one declaration of a kind file:
//
//	var Deploy = policy.NewKind[Input]("DeployApproval",
//		policy.WithVersion(1),
//		policy.WithDecisions(Deny, Approve), // precedence order, highest first
//		policy.WithDefault(NoRuleMatched),
//		policy.WithFunc("split", strings.Split),
//	)
//
// Go types map to Sigil types as follows:
//
//	string, bool           string, bool
//	int, int64             int
//	float64                float
//	time.Duration          duration
//	time.Time              timestamp
//	[]T                    list<T>
//	map[K]T, scalar K      map<K, T>
//	*T                     ?T
//	named struct           type, named after the Go type
//
// A named type follows its underlying type, so `type Tier string` is a
// string. Anything else is rejected: other integer and float sizes,
// unsigned integers, channels, funcs, interfaces, anonymous structs, and
// pointers to slices or maps, since a nil slice or map already reads as
// empty. The full rules are at
// https://sigil.specht-labs.de/reference/go-api/#go-type-mapping.
//
// NewKind panics when the contract can't be exported, listing every
// problem at once, the way [regexp.MustCompile] panics on a bad pattern.
// A kind that exists can always be written out with [Kind.Schema] as a
// kind file, which a policy repository checks in so the sigil CLI can
// type-check policies without importing the host.
//
// # Loading and compiling policies
//
// [Kind.Load] reads every `.sigil` file in an [io/fs.FS] into one bundle and
// compiles the policy with the given name as the root. Documents resolve
// each other by the names in their headers, so files are plain containers:
// one document per file, one file per team, or everything in one file.
// [embed.FS], [os.DirFS], a mounted Kubernetes ConfigMap and [MapFS] all
// work. [Kind.Compile] does the same for a single source string.
//
//	p, err := Deploy.Load(policies, "payments.production",
//		policy.Require("deploy.guardrails", policy.From(platformFS)),
//	)
//
// A [LoadOption] adjusts the compile. [Params] binds the root policy's
// params from Go. [Require] makes the root invoke another policy
// unconditionally, so a team can't switch the platform's guardrails off,
// and [From] pins where that policy comes from.
//
// A failed compile returns a [*CompileError] with every problem found,
// each with a [Position] and a fix hint.
//
// # Evaluating
//
// [Policy.Eval] runs a compiled [Policy] against one input and returns a
// [Result]. A Policy is immutable and safe for concurrent use.
//
// Every `when` rule is evaluated, in no particular order, and each decision
// constructor reached becomes a [Candidate]. For a kind declared with
// [WithDecisions] (`collect one`), the candidate of the highest-ranked
// decision wins and fills Result.Decision, Result.Reason and
// Result.Payload; when nothing fires the kind's default applies. For a kind
// declared with [WithCollect] (`collect all`), every candidate applies and
// Result.Outcome lists them. Result.Trace records every candidate either
// way, with the file, line and column of the rule that produced it.
//
// Result.Payload is an untyped map for logging and generic tooling.
// Application code matches on the decision handle instead and gets the
// payload struct back with [Decision.Match] or [Decision.MatchAll]:
//
//	res, err := p.Eval(ctx, input)
//	if err != nil {
//		return err // res holds the kind's default
//	}
//	if a, ok := Approve.Match(res); ok {
//		startRollout(a.Bake)
//	}
//
// [Outcome.Is] does the same for one reason: NoRuleMatched.Is(res).
//
// # Errors
//
// Eval never returns a nil [*Result]. When it returns an error, the result
// holds the kind's default decision, or an empty outcome for a collecting
// kind, so a host that fails closed can use it directly. The error is one
// of:
//
//   - [*RuntimeError]: a list index out of range, integer overflow, or a
//     host function that returned an error, which [errors.Is] finds
//     through it.
//   - [*ConflictError]: two members of an exclusive set fired, or a
//     `collect one` kind has several candidates at its top rank. This is a
//     defect in the policy rather than in the input.
//   - [*AssertionError]: one or more of the policy's asserts didn't hold.
//     Its Phase says whether they were input asserts, which reject the
//     input, or outcome asserts, which reject the policy's own outcome.
//   - The context's error, when ctx was done before or during the
//     evaluation. Eval checks it while it runs, so a deadline bounds the
//     time a large input can take.
//
// Use [errors.As] to tell them apart. Host functions must be pure, must
// terminate and must not panic: Eval can't interrupt one, and a panic
// propagates to the caller unless the kind sets [WithRecoverHostPanics].
//
// # Reloading at run time
//
// Compiled policies are immutable, so replacing one is a pointer swap, and
// in-flight evaluations finish with the policy they started with. Compile
// the new bundle first and keep serving the old policy when that fails:
//
//	var current atomic.Pointer[policy.Policy[Input]]
//
//	func reload(fsys fs.FS) error {
//		p, err := Deploy.Load(fsys, "payments.production")
//		if err != nil {
//			return err // keep the last good policy
//		}
//		current.Store(p)
//		return nil
//	}
//
// # Related packages
//
// Package [github.com/spechtlabs/sigil/pkg/policytest] runs the YAML test
// files of `sigil test` from go test, against the host's own kind. Package
// [github.com/spechtlabs/sigil/pkg/cli] builds the sigil command line into
// the host's binary, so the CLI decodes inputs into the host's types and
// calls its real host functions.
//
// The Go API reference, with every option's kind file equivalent, is at
// https://sigil.specht-labs.de/reference/go-api/, and a step-by-step guide
// from Go structs to a typed decision is at
// https://sigil.specht-labs.de/guides/embed-go/.
package policy
