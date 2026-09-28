package policy_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// A collecting kind, for the access bundle.
type (
	AccessInput struct {
		Actor Actor `policy:"actor"`
	}
	AdminData struct {
		TTL time.Duration `policy:"ttl,default=8h"`
	}
)

var (
	Read  = policy.NewDecision[policy.None]("read", "engineering_member", "everyone")
	Write = policy.NewDecision[policy.None]("write", "platform_member")
	Admin = policy.NewDecision[AdminData]("admin", "platform_member", "oncall")

	Access = policy.NewKind[AccessInput]("AccessGrant", policy.WithVersion(1), policy.WithCollect(Read, Write, Admin))

	// The same kind with a default grant, for the case where nothing fires.
	AccessWithDefault = policy.NewKind[AccessInput]("AccessGrant", policy.WithVersion(1),
		policy.WithCollect(Read, Write, Admin), policy.WithDefault(Read, "everyone"))

	// A compartment kind: an actor may be granted A or B, never both, and
	// a deny outranks either.
	GrantA       = policy.NewDecision[policy.None]("grant_a", "member")
	GrantB       = policy.NewDecision[policy.None]("grant_b", "member")
	Suspend      = policy.NewDecision[policy.None]("deny", "suspended", "none")
	Compartments = policy.NewKind[AccessInput]("Compartments", policy.WithVersion(1),
		policy.WithDecisions(Suspend, GrantA, GrantB),
		policy.WithExclusive(GrantA, GrantB),
		policy.WithDefault(Suspend, "none"))

	// A collect all kind with precedence: every review reaches the host
	// unless a deny fired.
	ReviewAny = policy.NewDecision[ReviewData]("review", "security", "owner")
	ApproveHF = policy.NewDecision[ApproveData]("approve", "hotfix")
	DenySoak  = policy.NewDecision[policy.None]("deny", "soak_too_short")
	Reviews   = policy.NewKind[Input]("Reviews", policy.WithVersion(1),
		policy.WithCollect(DenySoak, ReviewAny, ApproveHF),
		policy.WithPrecedence(DenySoak, ReviewAny, ApproveHF))
)

// The testdata bundles the tables refer to.
const (
	teamBundle   = "team"
	accessBundle = "access"
)

// eligible is a deploy every rule of the team bundle lets through:
// managed, ga, cleared for its regions, by a deployer on payments-sre.
var eligible = Input{
	Release: Release{Soak: 26 * time.Hour},
	Service: Service{
		Name: "payments-api", Tier: "critical", Owners: []string{"payments"},
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "argocd", "platform.example.com/lifecycle": "ga",
			"regions": "eu-1,us-1",
		},
	},
	Actor:       Actor{Name: "alice", Teams: []string{"payments-sre"}, Roles: []string{"deployer"}, Regions: []string{"eu-1", "us-1"}},
	Environment: "production",
}

// scenario is one input a golden case evaluates, by name.
type scenario[In any] struct {
	name  string
	input In
}

// goldenCases says how each testdata/*.sigil bundle is compiled and
// which inputs it's evaluated against. A bundle without an entry is
// only compiled, which is what the error cases need.
var goldenCases = map[string]func(t *testing.T, src string) string{
	teamBundle: run(Deploy, "payments.production",
		[]policy.LoadOption{policy.Params{"approvers": []string{"payments-leads"}, "min_soak": 4 * time.Hour}},
		[]scenario[Input]{
			{"the team rule fires alone", with(func(in *Input) { in.Service.Tier = "standard"; in.Service.Owners = nil })},
			{"a guardrail deny outranks the team's approve", with(func(in *Input) { in.Release.Soak = 2 * time.Hour })},
			{"a hotfix skips the soak", with(func(in *Input) { in.Release.Soak = 2 * time.Hour; in.Release.Hotfix = true })},
			{"review with the bound approvers", with(func(in *Input) { in.Service.Tier = "internal"; in.Actor.Teams = []string{"payments"} })},
			{"a tie goes to the earliest constructor", with(func(in *Input) { in.Actor.Roles = []string{"deployer", "release_manager"} })},
			{"not eligible", with(func(in *Input) { in.Environment = "staging" })},
			{"nothing fires", with(func(in *Input) { in.Service.Tier = "standard"; in.Actor.Teams = nil })},
			{"a negative soak fails the assert", with(func(in *Input) { in.Release.Soak = -time.Hour })},
		}),
	accessBundle: run(Access, "access.engineering", nil, []scenario[AccessInput]{
		{"an engineer", AccessInput{Actor: Actor{Teams: []string{"engineering"}}}},
		{"a platform engineer on call", AccessInput{Actor: Actor{Teams: []string{"engineering", "platform"}, Roles: []string{"oncall"}}}},
		{"a platform engineer off call fails the separation assert", AccessInput{Actor: Actor{Teams: []string{"engineering", "platform"}}}},
		{"nobody", AccessInput{}},
	}),
	"access_default": run(AccessWithDefault, "access.engineering", nil, []scenario[AccessInput]{
		{"an engineer", AccessInput{Actor: Actor{Teams: []string{"engineering"}}}},
		{"nobody gets the default", AccessInput{}},
		{"an assert failure hides the default", AccessInput{Actor: Actor{Teams: []string{"engineering", "platform"}}}},
	}),
	"conflicts": run(Deploy, "p", nil, []scenario[Input]{
		{"one review", with(func(in *Input) { in.Actor.Teams = nil })},
		{"the same reason with different payloads", eligible},
		{"two unranked reasons of one decision", with(func(in *Input) { in.Release.Hotfix = true; in.Actor.Teams = nil })},
	}),
	"ranked": run(Deploy, "p", nil, []scenario[Input]{
		{"the team rule alone", eligible},
		{"both approvals fire and release_manager outranks payments_sre", with(func(in *Input) { in.Actor.Roles = []string{"release_manager"} })},
		{"the same outcome from two branches folds", with(func(in *Input) { in.Actor.Roles = []string{"release_manager"}; in.Actor.Teams = nil })},
	}),
	"exclusive": run(Compartments, "p", nil, []scenario[AccessInput]{
		{"compartment a", AccessInput{Actor: Actor{Name: "alice", Teams: []string{"a"}}}},
		{"both compartments conflict", AccessInput{Actor: Actor{Name: "alice", Teams: []string{"a", "b"}}}},
		{"a deny doesn't hide the conflict", AccessInput{Actor: Actor{Name: "alice", Teams: []string{"a", "b"}, Roles: []string{"suspended"}}}},
		{"nobody", AccessInput{Actor: Actor{Name: "alice"}}},
	}),
	"collect_top": run(Reviews, "p", nil, []scenario[Input]{
		{"every review at the top rank", with(func(in *Input) { in.Release.Hotfix = true })},
		{"a deny leaves only itself", with(func(in *Input) { in.Release.Soak = time.Minute })},
		{"one approval", with(func(in *Input) { in.Service.Tier = "standard"; in.Service.Owners = nil; in.Release.Hotfix = true })},
	}),
	"asserts": run(Deploy, "p", nil, []scenario[Input]{
		{"every failing input assert is reported and no rule runs", with(func(in *Input) {
			in.Release.Soak = -time.Hour
			delete(in.Service.Labels, "team")
		})},
		{"outcome asserts see the outcome", with(func(in *Input) {
			in.Service.Labels["team"] = "payments"
			in.Actor.Roles = []string{"a", "b", "c", "d"}
		})},
	}),
	"lets": run(Deploy, "p", nil, []scenario[Input]{
		{"the scoped let under a false condition never runs", eligible},
		{"a scoped let in a reached body", with(func(in *Input) { in.Service.Tier = "standard" })},
	}),
	"params_bounds": run(Deploy, "p", []policy.LoadOption{policy.Params{"min_soak": 30 * time.Minute}}, nil),
	"runtime": run(Deploy, "p", nil, []scenario[Input]{
		{"an index out of range", eligible},
		{"an index out of range in a payload", with(func(in *Input) { in.Actor.Roles = []string{"a", "b", "c", "d"} })},
	}),
	"kind_export":    run(Deploy, "p", nil, []scenario[Input]{{"compiles against a matching export", eligible}}),
	"params_type":    run(Deploy, "p", []policy.LoadOption{policy.Params{"min_soak": 4}}, nil),
	"params_go_type": run(Deploy, "p", []policy.LoadOption{policy.Params{"min_soak": int32(4)}}, nil),
	"params_unknown": run(Deploy, "p", []policy.LoadOption{policy.Params{"soak": time.Hour}}, nil),
	"errors_root":    run(Deploy, "q", nil, []scenario[Input]{}),
	"errors_module":  run(Deploy, "m", nil, []scenario[Input]{}),
}

// TestGolden compiles every testdata/*.sigil bundle as its case says,
// evaluates the case's inputs, and compares the rendered results with
// the matching .golden file. Run with -update to accept changes; review
// the diff, since the golden files pin what a host gets back.
func TestGolden(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no testdata/*.sigil files")
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".sigil")
		t.Run(name, func(t *testing.T) {
			render, ok := goldenCases[name]
			if !ok {
				render = run(Deploy, "p", nil, []scenario[Input]{})
			}
			got := render(t, readBundle(t, name))
			goldenPath := strings.TrimSuffix(path, ".sigil") + ".golden"
			if *update {
				if werr := os.WriteFile(goldenPath, []byte(got), 0o644); werr != nil {
					t.Fatal(werr)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", goldenPath, got, want)
			}
		})
	}
}

// readBundle returns the source of testdata/<name>.sigil.
func readBundle(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name+".sigil"))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// run returns a golden renderer: it compiles root from the bundle
// against k, reports the compile error if any, and otherwise renders
// every scenario's result.
func run[In any](k *policy.Kind[In], root string, opts []policy.LoadOption, scenarios []scenario[In]) func(*testing.T, string) string {
	return func(_ *testing.T, src string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "== compile %s ==\n", root)
		p, err := k.Compile(src, root, opts...)
		if err != nil {
			b.WriteString(err.Error() + "\n")
			return b.String()
		}
		b.WriteString("ok\n")
		for _, s := range scenarios {
			fmt.Fprintf(&b, "\n== %s ==\n", s.name)
			res, err := p.Eval(context.Background(), s.input)
			b.WriteString(renderResult(res, err))
		}
		return b.String()
	}
}

// renderResult renders a result and its error in the layout the CLI's
// eval command is designed to print.
func renderResult(res *policy.Result, err error) string {
	var b strings.Builder
	if len(res.Outcome) == 1 && res.Decision != "" {
		fmt.Fprintf(&b, "decision  %s\nreason    %s\npolicy    %s\npayload   %s\n", res.Decision, res.Reason, res.Policy, payload(res.Payload))
	} else {
		b.WriteString("outcome\n")
		if len(res.Outcome) == 0 {
			b.WriteString("  (none)\n")
		}
		for _, e := range res.Outcome {
			fmt.Fprintf(&b, "  %-8s %-18s %s\n", e.Decision, e.Reason, payload(e.Payload))
		}
	}
	if err != nil {
		b.WriteString("error     " + strings.ReplaceAll(err.Error(), "\n", "\n          ") + "\n")
		if ae, ok := errors.AsType[*policy.AssertionError](err); ok {
			for _, f := range ae.Failures {
				for _, c := range f.Outcome {
					fmt.Fprintf(&b, "          %s: %s\n", f.Reason, c)
				}
			}
		}
	}
	b.WriteString("\ncandidates\n")
	if len(res.Trace.Candidates) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, c := range res.Trace.Candidates {
		fmt.Fprintf(&b, "  %-8s %-18s %s", c.Decision, c.Reason, c.Location())
		if len(c.Payload) > 0 {
			b.WriteString("  " + payload(c.Payload))
		}
		b.WriteString("\n")
		for _, cond := range c.Conditions {
			fmt.Fprintf(&b, "           %s\n", cond.Text)
		}
	}
	return b.String()
}

// payload renders a payload map with its fields sorted, or (none).
func payload(p map[string]any) string {
	if len(p) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(p))
	for name, v := range p {
		parts = append(parts, fmt.Sprintf("%s: %v", name, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
