package policy_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// The README example, as a host writes it.
type (
	Release struct {
		Soak   time.Duration `policy:"soak"`
		Hotfix bool          `policy:"hotfix"`
	}
	Service struct {
		Name   string            `policy:"name"`
		Tier   string            `policy:"tier"`
		Owners []string          `policy:"owners"`
		Labels map[string]string `policy:"labels"`
	}
	Actor struct {
		Name    string   `policy:"name"`
		Teams   []string `policy:"teams"`
		Roles   []string `policy:"roles"`
		Regions []string `policy:"regions"`
	}
	Input struct {
		Release     Release `policy:"release"`
		Service     Service `policy:"service"`
		Actor       Actor   `policy:"actor"`
		Environment string  `policy:"environment"`
	}
	ReviewData struct {
		Approvers []string `policy:"approvers"`
	}
	ApproveData struct {
		Bake time.Duration `policy:"bake,default=1h"`
	}
)

var (
	Deny    = policy.NewDecision[policy.None]("deny", "a", "no_rule_matched", "never", "b", "not_eligible", "soak_too_short", "x")
	Review  = policy.NewDecision[ReviewData]("review", "b", "a", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "owned", "a", "release_manager", "payments_sre")

	Deploy = policy.NewKind[Input]("DeployApproval",
		policy.WithVersion(1),
		policy.WithDecisions(Deny, Review, Approve), // order = precedence
		policy.WithReasonPrecedence(Approve, "release_manager", "payments_sre", "owned", "a"),
		policy.WithDefault(Deny, "no_rule_matched"),
		policy.WithFunc("split", strings.Split),
	)
)

func TestSchema(t *testing.T) {
	want := `kind DeployApproval version 1

type Release {
  soak: duration
  hotfix: bool
}
type Service {
  name: string
  tier: string
  owners: list<string>
  labels: map<string, string>
}
type Actor {
  name: string
  teams: list<string>
  roles: list<string>
  regions: list<string>
}

input release: Release
input service: Service
input actor: Actor
input environment: string

fn split(string, string) -> list<string>

decision deny {
  a
  no_rule_matched
  never
  b
  not_eligible
  soak_too_short
  x
}
decision review(approvers: list<string>) {
  b
  a
  service_owner
}
decision approve(bake: duration = 1h) {
  owned
  a
  release_manager
  payments_sre
}

collect one
precedence deny > review > approve
precedence approve: release_manager > payments_sre > owned > a
default deny(no_rule_matched)
`
	if got := Deploy.Schema(); got != want {
		t.Errorf("Schema() =\n%s\nwant\n%s", got, want)
	}
	if Deploy.Name() != "DeployApproval" {
		t.Errorf("Name() = %q", Deploy.Name())
	}
	if Review.Name() != "review" {
		t.Errorf("Review.Name() = %q", Review.Name())
	}
}

// TestOptionsCompose checks that the multi-valued options add up: one
// call with several arguments and several calls with one are the same
// kind.
func TestOptionsCompose(t *testing.T) {
	split := policy.NewKind[Input]("DeployApproval",
		policy.WithVersion(1),
		policy.WithDecisions(Deny),
		policy.WithDecisions(Review, Approve),
		policy.WithReasonPrecedence(Approve, "release_manager", "payments_sre", "owned", "a"),
		policy.WithDefault(Deny, "no_rule_matched"),
		policy.WithFunc("split", strings.Split),
	)
	if split.Schema() != Deploy.Schema() {
		t.Errorf("split declarations differ:\n%s\n%s", split.Schema(), Deploy.Schema())
	}

	funcs := policy.NewKind[Input]("K", policy.WithVersion(1), policy.WithDecisions(Deny), policy.WithDefault(Deny, "x"),
		policy.WithFunc("upper", strings.ToUpper), policy.WithFunc("lower", strings.ToLower))
	if !strings.Contains(funcs.Schema(), "fn upper(string) -> string\nfn lower(string) -> string\n") {
		t.Errorf("Schema() =\n%s", funcs.Schema())
	}
}

func TestAccepts(t *testing.T) {
	k := policy.NewKind[Input]("K", policy.WithVersion(3), policy.WithAccepts(2), policy.WithDecisions(Deny), policy.WithDefault(Deny, "x"))
	if !strings.HasPrefix(k.Schema(), "kind K version 3, accepts: 2\n") {
		t.Errorf("Schema() =\n%s", k.Schema())
	}
}

func TestCollect(t *testing.T) {
	type AccessInput struct {
		Actor Actor `policy:"actor"`
	}
	type AdminData struct {
		TTL time.Duration `policy:"ttl,default=8h"`
	}
	read := policy.NewDecision[policy.None]("read", "engineering_member")
	admin := policy.NewDecision[AdminData]("admin", "platform_member", "oncall")
	access := policy.NewKind[AccessInput]("AccessGrant", policy.WithVersion(1), policy.WithCollect(read), policy.WithCollect(admin))
	want := "kind AccessGrant version 1\n\ntype Actor {\n  name: string\n  teams: list<string>\n  roles: list<string>\n  regions: list<string>\n}\n\ninput actor: Actor\n\ndecision read {\n  engineering_member\n}\ndecision admin(ttl: duration = 8h) {\n  platform_member\n  oncall\n}\n\ncollect all\n"
	if got := access.Schema(); got != want {
		t.Errorf("Schema() =\n%s\nwant\n%s", got, want)
	}
}

func TestNewKindPanics(t *testing.T) {
	tests := []struct {
		name string
		fn   func()
		want []string // fragments of the panic message
	}{
		{name: "no decisions", fn: func() { policy.NewKind[Input]("K", policy.WithVersion(1)) },
			want: []string{"policy.NewKind(K): invalid kind:", "kind K declares no decisions", "(declare at least one decision)"}},
		{name: "default without every field", fn: func() {
			policy.NewKind[Input]("K", policy.WithVersion(1), policy.WithDecisions(Deny, Review), policy.WithDefault(Review, "x"))
		}, want: []string{`default: field "approvers" is required and has no value`}},
		{name: "unsupported field", fn: func() {
			type Bad struct {
				N int32 `policy:"n"`
			}
			policy.NewKind[Bad]("K", policy.WithVersion(1), policy.WithDecisions(Deny), policy.WithDefault(Deny, "x"))
		}, want: []string{"n: unsupported type int32"}},
		{name: "decisions and collect mixed", fn: func() {
			policy.NewKind[Input]("K", policy.WithVersion(1), policy.WithDecisions(Deny), policy.WithCollect(Review))
		}, want: []string{"kind K mixes WithDecisions and WithCollect", "(a kind ranks its decisions with WithDecisions, or applies them all with WithCollect; use one)"}},
		{name: "no version", fn: func() {
			policy.NewKind[Input]("K", policy.WithDecisions(Deny), policy.WithDefault(Deny, "x"))
		}, want: []string{"invalid kind version 0"}},
		{name: "every problem is listed", fn: func() {
			policy.NewKind[Input]("kind", policy.WithDecisions(Deny))
		}, want: []string{`invalid kind name "kind"`, "invalid kind version 0", "kind kind has no default decision"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("NewKind did not panic")
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("panic value %T, want string", r)
				}
				for _, w := range tt.want {
					if !strings.Contains(msg, w) {
						t.Errorf("panic message\n%s\nwant it to contain %q", msg, w)
					}
				}
			}()
			tt.fn()
		})
	}
}

func TestPosition(t *testing.T) {
	tests := []struct {
		pos  policy.Position
		want string
	}{
		{policy.Position{}, "-"},
		{policy.Position{Line: 4, Column: 3}, "4:3"},
		{policy.Position{File: "policies.sigil", Document: "payments.production", Line: 42, Column: 5}, "policies.sigil:42:5 (payments.production)"},
		{policy.Position{File: "deploy/production.sigil", Document: "deploy.production", Line: 16, Column: 5}, "deploy/production.sigil:16:5"},
		{policy.Position{File: "/etc/sigil/deploy/production.sigil", Document: "deploy.production", Line: 16, Column: 5}, "/etc/sigil/deploy/production.sigil:16:5"},
		{policy.Position{File: "production.sigil", Document: "deploy.production", Line: 1, Column: 1}, "production.sigil:1:1 (deploy.production)"},
		{policy.Position{Document: "p", Line: 1, Column: 1}, "1:1 (p)"},
	}
	for _, tt := range tests {
		if got := tt.pos.String(); got != tt.want {
			t.Errorf("%+v = %q, want %q", tt.pos, got, tt.want)
		}
	}
}

// TestMatch checks the typed accessors against results of each decision,
// the default, and a collecting kind.
func TestMatch(t *testing.T) {
	ranked := compile(t, Deploy, teamBundle, "payments.production", policy.Params{"approvers": []string{"payments-leads"}, "min_soak": 4 * time.Hour})
	collecting := compile(t, Access, accessBundle, "access.engineering")
	tests := []struct {
		name    string
		res     *policy.Result
		approve any // Approve.Match's payload, or nil when it doesn't match
		review  any
		deny    any
		admins  []string // admin.MatchAll's reasons
		panics  bool     // Match on this result panics
	}{
		{name: "approve wins", res: eval(t, ranked, eligible), approve: ApproveData{Bake: 15 * time.Minute}},
		{name: "review wins", res: eval(t, ranked, with(func(in *Input) { in.Service.Tier = "internal"; in.Actor.Teams = []string{"payments"} })),
			review: ReviewData{Approvers: []string{"payments-leads"}}},
		{name: "deny wins", res: eval(t, ranked, with(func(in *Input) { in.Release.Soak = time.Hour })), deny: policy.None{}},
		{name: "the default", res: eval(t, ranked, with(func(in *Input) { in.Service.Tier = "standard"; in.Actor.Teams = nil })), deny: policy.None{}},
		{name: "nil result", res: nil},
		{name: "collecting kind", res: eval(t, collecting, AccessInput{Actor: Actor{Teams: []string{"platform"}, Roles: []string{"oncall"}}}),
			admins: []string{"platform_member", "oncall"}, panics: true},
		{name: "collecting kind with nothing fired", res: eval(t, collecting, AccessInput{}), admins: nil, panics: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.panics {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("Match on a collecting result didn't panic")
						}
					}()
					Approve.Match(tt.res)
				}()
				var reasons []string
				for _, g := range Admin.MatchAll(tt.res) {
					reasons = append(reasons, g.Reason)
					if g.Payload.TTL == 0 {
						t.Errorf("admin %q has no ttl", g.Reason)
					}
				}
				if !reflect.DeepEqual(reasons, tt.admins) {
					t.Errorf("Admin.MatchAll = %q, want %q", reasons, tt.admins)
				}
				return
			}
			check := func(name string, got any, ok bool, want any) {
				if ok != (want != nil) || (ok && !reflect.DeepEqual(got, want)) {
					t.Errorf("%s.Match = %#v, %v; want %#v", name, got, ok, want)
				}
			}
			a, ok := Approve.Match(tt.res)
			check("Approve", a, ok, tt.approve)
			r, ok := Review.Match(tt.res)
			check("Review", r, ok, tt.review)
			d, ok := Deny.Match(tt.res)
			check("Deny", d, ok, tt.deny)
			if tt.res != nil {
				all := Approve.MatchAll(tt.res)
				if (len(all) == 1) != (tt.approve != nil) {
					t.Errorf("Approve.MatchAll = %+v", all)
				}
			}
		})
	}
}

// TestDiagnostics checks the structured side of a compile error: each
// diagnostic's position, document and hint. The rendered text is pinned
// by the golden tests.
func TestDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		src  string
		root string
		opts []policy.LoadOption
		want []policy.Diagnostic
	}{
		{
			name: "a typo in the second document of a file",
			src:  "policy p: DeployApproval@1\nwhen true { deny(a) }\n---\npolicy q: DeployApproval@1\nwhen servce.tier == \"x\" { deny(a) }",
			root: "p",
			want: []policy.Diagnostic{{
				Message:  "unknown name `servce`",
				Help:     "did you mean `service`?",
				Position: policy.Position{Document: "q", Line: 5, Column: 6},
				End:      policy.Position{Line: 5, Column: 12},
			}},
		},
		{
			name: "a missing root has no position",
			src:  "policy p: DeployApproval@1\nwhen true { deny(a) }",
			root: "q",
			want: []policy.Diagnostic{{Message: "bundle has no policy q", Help: "the bundle defines: p"}},
		},
		{
			name: "a nil param value",
			src:  "policy p: DeployApproval@1\nparam a: int\nwhen true { deny(a) }",
			root: "p",
			opts: []policy.LoadOption{policy.Params{"a": nil}},
			want: []policy.Diagnostic{{Message: "param a: expected int, found nil", Help: "Params values are Go values of the shape NewKind accepts for the param's type",
				Position: policy.Position{Document: "p", Line: 2, Column: 1}, End: policy.Position{Line: 2, Column: 13}}},
		},
		{
			name: "every param problem is reported",
			src:  "policy p: DeployApproval@1\nparam a: int\nparam b: duration\nwhen true { deny(a) }",
			root: "p",
			opts: []policy.LoadOption{policy.Params{"c": 1, "b": 2}},
			want: []policy.Diagnostic{ // in param name order
				{Message: "param b: expected duration, found int", Help: "Params values are Go values of the shape NewKind accepts for the param's type",
					Position: policy.Position{Document: "p", Line: 3, Column: 1}, End: policy.Position{Line: 3, Column: 18}},
				{Message: "policy p has no param \"c\"", Help: "p declares: a, b"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Deploy.Compile(tt.src, tt.root, tt.opts...)
			var ce *policy.CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("error = %v, want a *CompileError", err)
			}
			if !reflect.DeepEqual(ce.Diagnostics, tt.want) {
				t.Errorf("Diagnostics = %+v\nwant %+v", ce.Diagnostics, tt.want)
			}
		})
	}
}

// TestEvalContext checks that a done context returns its error with the
// default result, without evaluating.
func TestEvalContext(t *testing.T) {
	p := compile(t, Deploy, teamBundle, "payments.production", policy.Params{"approvers": []string{"a"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := p.Eval(ctx, eligible)
	if !errors.Is(err, context.Canceled) || res.Reason != "no_rule_matched" || len(res.Trace.Candidates) != 0 {
		t.Errorf("Eval with a done context = %+v, %v", res, err)
	}
}

// TestEvalIsConcurrent evaluates one policy from several goroutines under
// the race detector, as a host does after a single compile.
func TestEvalIsConcurrent(t *testing.T) {
	p := compile(t, Deploy, teamBundle, "payments.production", policy.Params{"approvers": []string{"a"}, "min_soak": 4 * time.Hour})
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := eligible
			if i%2 == 0 {
				in.Release.Soak = time.Hour
			}
			res, err := p.Eval(context.Background(), in)
			if err != nil {
				t.Error(err)
				return
			}
			if (i%2 == 0) != (res.Reason == "soak_too_short") {
				t.Errorf("goroutine %d: %s", i, res.Reason)
			}
		}(i)
	}
	wg.Wait()
}

// compile compiles root from a testdata bundle against k.
func compile[In any](t *testing.T, k *policy.Kind[In], bundle, root string, opts ...policy.LoadOption) *policy.Policy[In] {
	t.Helper()
	p, err := k.Compile(readBundle(t, bundle), root, opts...)
	if err != nil {
		t.Fatalf("Compile:\n%v", err)
	}
	if p.Name() != root {
		t.Errorf("Name() = %q, want %q", p.Name(), root)
	}
	return p
}

// eval evaluates p against input, failing on any error.
func eval[In any](t *testing.T, p *policy.Policy[In], input In) *policy.Result {
	t.Helper()
	res, err := p.Eval(context.Background(), input)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	return res
}

// with returns a copy of the eligible input changed by fn.
func with(fn func(*Input)) Input {
	in := eligible
	in.Service.Labels = maps.Clone(eligible.Service.Labels)
	fn(&in)
	return in
}
