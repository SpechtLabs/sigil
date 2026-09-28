package policy_test

import (
	"strings"
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
	Deny    = policy.Decision[policy.None]("deny")
	Review  = policy.Decision[ReviewData]("review")
	Approve = policy.Decision[ApproveData]("approve")

	Deploy = policy.NewKind[Input]("DeployApproval",
		policy.WithVersion(1),
		policy.WithDecisions(Deny, Review, Approve), // order = precedence
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

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

collect one
precedence deny > review > approve
default deny("no_rule_matched")
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

func TestCollect(t *testing.T) {
	type AccessInput struct {
		Actor Actor `policy:"actor"`
	}
	type AdminData struct {
		TTL time.Duration `policy:"ttl,default=8h"`
	}
	read := policy.Decision[policy.None]("read")
	admin := policy.Decision[AdminData]("admin")
	access := policy.NewKind[AccessInput]("AccessGrant", policy.WithVersion(1), policy.WithCollect(read), policy.WithCollect(admin))
	want := "kind AccessGrant version 1\n\ntype Actor {\n  name: string\n  teams: list<string>\n  roles: list<string>\n  regions: list<string>\n}\n\ninput actor: Actor\n\ndecision read(reason: string)\ndecision admin(reason: string, ttl: duration = 8h)\n\ncollect all\n"
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
