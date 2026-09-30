package eval_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/stub"
	"github.com/spechtlabs/sigil/internal/types"
)

// The host's types: the README example plus optional and timestamp fields.
type (
	Release struct {
		Soak    time.Duration `policy:"soak"`
		Hotfix  bool          `policy:"hotfix"`
		Ticket  *string       `policy:"ticket"`
		BuiltAt time.Time     `policy:"built_at"`
		Parent  *Commit       `policy:"parent"`
	}
	Commit struct {
		ID       int     `policy:"id"`
		Author   Actor   `policy:"author"`
		MergedBy *Actor  `policy:"merged_by"`
		Note     *string `policy:"note"`
	}
	Service struct {
		Name   string            `policy:"name"`
		Tier   string            `policy:"tier"`
		Owners []string          `policy:"owners"`
		Labels map[string]string `policy:"labels"`
		Owner  Actor             `policy:"owner"`
		Scores map[string]int    `policy:"scores"`
		Counts []int             `policy:"counts"`
		ByID   map[int]string    `policy:"by_id"`
	}
	Actor struct {
		Name    string   `policy:"name"`
		Teams   []string `policy:"teams"`
		Roles   []string `policy:"roles"`
		Regions []string `policy:"regions"`
		Ticket  *string  `policy:"ticket"`
	}
	Input struct {
		Release     Release   `policy:"release"`
		Service     Service   `policy:"service"`
		Actor       Actor     `policy:"actor"`
		Environment string    `policy:"environment"`
		Now         time.Time `policy:"now"`
		Ratio       float64   `policy:"ratio"`
		Count       int       `policy:"count"`
	}
	None struct{}
)

var (
	built = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	now   = built.Add(3 * time.Hour)
	chg   = "CHG-1042"
	input = Input{
		Release: Release{Soak: 26 * time.Hour, Hotfix: false, Ticket: &chg, BuiltAt: built,
			Parent: &Commit{ID: 42, Author: Actor{Name: "bob", Roles: []string{"dev"}}}},
		Service: Service{
			Name: "payments-api", Tier: "critical", Owners: []string{"payments", "platform"},
			Labels: map[string]string{"team": "payments", "regions": "eu-1,us-1", "compliance": "pci"},
			Owner:  Actor{Name: "payments-lead"},
			Scores: map[string]int{"risk": 7},
			Counts: []int{1, 2, 3},
			ByID:   map[int]string{7: "seven"},
		},
		Actor: Actor{
			Name: "alice", Teams: []string{"payments-sre", "payments"}, Roles: []string{"deployer", "sre-eu"},
			Regions: []string{"eu-1", "us-1", "ap-1"},
		},
		Environment: "production",
		Now:         now,
		Ratio:       0.5,
		Count:       3,
	}
)

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

func fail(s string) (string, error) { return "", errors.New("boom: " + s) }

func unbound(string) (string, error) { return "", &gokind.ErrUnbound{Name: "unbound"} }

// advisedError is a host's own error with a Help method, which the
// evaluator must not take for advice: only sigil's stand-ins give it.
type advisedError struct{}

func (advisedError) Error() string { return "no quota" }

func (advisedError) Help() string { return "raise the quota" }

func advised(string) (string, error) { return "", advisedError{} }

func unmatched(string) (string, error) {
	return "", &stub.ErrUnmatched{Name: "unmatched", Args: `"x"`}
}

func stubbed(string) (string, error) { return "", &stub.Failure{Msg: "down"} }

// setup builds the kind, checks src as an expression with a param and a
// let in scope, compiles it, and returns the compiled expression and a
// frame with the param and let bound.
func setup(t *testing.T, src string, assert bool) (eval.Expr, *eval.Frame) {
	t.Helper()
	k, b, errs := gokind.Build(gokind.Options{
		Name: "Test", Version: 1, Input: typeOf[Input](),
		Decisions: []gokind.Decision{{Name: "deny", Payload: typeOf[None](), Reasons: []string{"b", "a", "d", "not_eligible", "soak_too_short"}}, {Name: "approve", Payload: typeOf[None](), Reasons: []string{"a", "release_manager", "payments_sre"}}},
		Default:   &gokind.Default{Decision: "deny", Reason: "a"},
		Funcs: []gokind.Func{
			{Name: "split", Fn: strings.Split},
			{Name: "len", Fn: func(xs []string) int { return len(xs) }},
			{Name: "fail", Fn: fail},
			{Name: "upper", Fn: strings.ToUpper},
			{Name: "unbound", Fn: unbound},
			{Name: "advised", Fn: advised},
			{Name: "unmatched", Fn: unmatched},
			{Name: "stubbed", Fn: stubbed},
		},
	})
	if errs != nil {
		t.Fatal(errs)
	}
	x, perrs := parser.ParseExpr("p.sigil", []byte(src))
	if perrs != nil {
		t.Fatal(perrs)
	}
	env := check.NewEnv(k)
	env.InAssert = assert
	env.Declare("min_soak", check.Binding{Entity: check.Param, Type: types.Duration})
	env.Declare("tiers", check.Binding{Entity: check.Let, Type: &types.List{Elem: types.String}})
	c := check.New("p.sigil")
	if got := c.Expr(x, env); got == types.Invalid {
		t.Fatalf("check: %v", c.Errors())
	}

	scope := eval.NewScope(b)
	minSoak, tiers := scope.Declare("min_soak"), scope.Declare("tiers")
	e, cerr := eval.Compile(x, c.Info(), scope)
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	f := eval.NewFrame(&input, scope)
	f.Set(minSoak, reflect.ValueOf(24*time.Hour))
	f.Set(tiers, reflect.ValueOf([]any{"standard", "internal"}))
	f.Outcome = reflect.ValueOf([]string{"approve"})
	return e, f
}

func TestEval(t *testing.T) {
	tests := []struct {
		src    string
		want   any    // the Go value, compared with reflect.DeepEqual
		err    string // a runtime error's message
		help   string // the help it carries, if any
		span   string
		assert bool
	}{
		// Names and literals.
		{src: "environment", want: "production"},
		{src: "count", want: 3},
		{src: "min_soak", want: 24 * time.Hour},
		{src: "tiers", want: []any{"standard", "internal"}},
		{src: "42", want: int64(42)},
		{src: "1h30m", want: 90 * time.Minute},
		{src: `["a", "b"]`, want: []any{"a", "b"}},
		{src: `{"a": 1}`, want: map[any]any{"a": int64(1)}},
		{src: "deny", assert: true, want: "deny"},
		{src: "outcome", assert: true, want: []string{"approve"}},

		// Fields, indexing, calls.
		{src: "release.soak", want: 26 * time.Hour},
		{src: "service.owner.name", want: "payments-lead"},
		{src: "actor.roles", want: []string{"deployer", "sre-eu"}},
		{src: "actor.roles[1]", want: "sre-eu"},
		{src: `service.labels["team"]`, want: "payments"},
		{src: `service.labels["absent"]`, want: ""},
		{src: `service.scores["absent"]`, want: 0},
		{src: `service.by_id[7]`, want: "seven"},
		{src: `service.by_id[8]`, want: ""},
		{src: `{"a": 1}["a"]`, want: int64(1)},
		{src: `{"a": 1}["b"]`, want: int64(0)},
		{src: `{1: "x"}[1]`, want: "x"},
		{src: `{1: "x"}[2]`, want: ""},
		{src: `{"a": 1.5}["b"]`, want: 0.0},
		{src: `{"a": true}["b"]`, want: false},
		{src: `{"a": 1h}["b"]`, want: time.Duration(0)},
		{src: `{"a": [1]}["b"]`, want: []any{}},
		{src: `{"a": {"b": 1}}["b"]`, want: map[any]any{}},
		{src: `{1h: "x"}[1h]`, want: "x"},
		{src: `{1.5: "x"}[1.5]`, want: "x"},
		{src: `{true: "x"}[true]`, want: "x"},
		{src: `{now: "x"}[now]`, want: "x"},
		{src: `split(service.labels["regions"], ",")`, want: []string{"eu-1", "us-1"}},
		{src: `split(service.labels["regions"], ",")[0]`, want: "eu-1"},
		{src: "len(actor.roles)", want: 2},
		{src: `len(["a", "b", "c"])`, want: 3},
		{src: "len([])", want: 0},
		{src: `upper(service.tier)`, want: "CRITICAL"},

		// Boolean operators.
		{src: "release.hotfix or environment == \"production\"", want: true},
		{src: "release.hotfix and environment == \"production\"", want: false},
		{src: "release.hotfix xor environment == \"production\"", want: true},
		{src: "not release.hotfix", want: true},
		{src: "release.hotfix and actor.roles[9] == \"x\"", want: false}, // short-circuit skips the bad index
		{src: "not release.hotfix or actor.roles[9] == \"x\"", want: true},

		// Comparison.
		{src: `service.tier == "critical"`, want: true},
		{src: `service.tier != "critical"`, want: false},
		{src: "release.soak < min_soak", want: false},
		{src: "release.soak >= min_soak", want: true},
		{src: "count > 2", want: true},
		{src: "count <= 2", want: false},
		{src: "ratio < 0.75", want: true},
		{src: "now > release.built_at", want: true},
		{src: "now < release.built_at", want: false},
		{src: "release.built_at < now", want: true},
		{src: "now >= now", want: true},
		{src: "now == release.built_at", want: false},
		{src: "release.built_at == release.built_at", want: true},
		{src: "release.built_at != now", want: true},
		{src: "0.5 == ratio", want: true},
		{src: "release.hotfix != false", want: false},
		{src: "release.hotfix == false", want: true},
		{src: "deny == approve", assert: true, want: false},
		{src: "deny != approve", assert: true, want: true},

		// Membership.
		{src: `"deployer" in actor.roles`, want: true},
		{src: `"admin" in actor.roles`, want: false},
		{src: `"admin" not in actor.roles`, want: true},
		{src: `service.labels has "team"`, want: true},
		{src: `not service.labels has "env"`, want: true},
		{src: `"payments" in service.name`, want: true},
		{src: `"billing" in service.name`, want: false},
		{src: `service.tier in ["critical", "standard"]`, want: true},
		{src: `service.tier in tiers`, want: false},
		{src: "service.by_id has 7", want: true},
		{src: "2 in service.counts", want: true},
		{src: "approve in outcome", assert: true, want: true},
		{src: "deny in outcome", assert: true, want: false},
		{src: `["eu-1"] in [["eu-1"], ["us-1"]]`, want: true},
		{src: `["eu-1"] in [["eu-1", "us-1"]]`, want: false},
		{src: `["eu-1"] in [["us-1"]]`, want: false},
		{src: `[] in [["eu-1"], []]`, want: true},
		{src: `{"a": 1} in [{"a": 1}]`, want: true},
		{src: `{"a": 1} in [{"a": 2}]`, want: false},
		{src: `{"a": 1} in [{"b": 1}]`, want: false},
		{src: `{"a": 1} in [{"a": 1, "b": 2}]`, want: false},
		{src: `[release.ticket] in [[release.ticket]]`, want: true},
		{src: `[release.ticket] in [[service.owner.ticket]]`, want: false},
		{src: `[service.owner.ticket] in [[service.owner.ticket]]`, want: true},
		{src: `[now] in [[now]]`, want: true},

		// List operators.
		{src: "actor.teams any in service.owners", want: true},
		{src: `["x"] any in service.owners`, want: false},
		{src: `split(service.labels["regions"], ",") all in actor.regions`, want: true},
		{src: `["eu-1", "mars"] all in actor.regions`, want: false},
		{src: "[] all in actor.regions", want: true},
		{src: "[] any in actor.regions", want: false},
		{src: `["deployer", "admin"] one in actor.roles`, want: true},
		{src: `["deployer", "sre-eu"] one in actor.roles`, want: false},
		{src: `["deployer", "deployer"] one in actor.roles`, want: true},
		{src: `["deployer", "sre-eu"] exclusive in actor.roles`, want: false},
		{src: `["admin", "root"] exclusive in actor.roles`, want: true},
		{src: `["deployer", "root"] exclusive in actor.roles`, want: true},
		{src: "[deny, approve] one in outcome", assert: true, want: true},
		{src: "[deny, approve] exclusive in outcome", assert: true, want: true},

		// Map containment and patterns.
		{src: `service.labels has {"team": "payments"}`, want: true},
		{src: `service.labels has {"team": "payments", "compliance": "pci"}`, want: true},
		{src: `service.labels has {"team": "billing"}`, want: false},
		{src: `service.labels has {"env": "prod"}`, want: false},
		{src: "service.labels has {}", want: true},
		{src: `service.labels has "team"`, want: true},
		{src: `service.labels has "env"`, want: false},
		{src: `service.name like "payments-*"`, want: true},
		{src: `service.name like "payments-???"`, want: true},
		{src: `service.name like "payments"`, want: false},
		{src: `service.name like "*-api"`, want: true},
		{src: `service.name like "pay?ents-api"`, want: true},
		{src: "service.labels[\"team\"] matches `^pay`", want: true},
		{src: "service.labels[\"team\"] matches `^team-[a-z]+$`", want: false},
		{src: "service.name matches `api`", want: true},

		// Optionals.
		{src: `release.ticket ?? "none"`, want: "CHG-1042"},
		{src: "release.parent?.id ?? 0", want: 42},
		{src: "present release.ticket", want: true},
		{src: "present release.parent?.author", want: true},
		{src: "present release.parent?.merged_by", want: false},
		{src: "present release.parent?.note", want: false},
		{src: `release.parent?.author.name ?? ""`, want: "bob"},
		{src: `release.parent?.author.roles[0] ?? ""`, want: "dev"},
		{src: `release.parent?.merged_by?.name ?? "nobody"`, want: "nobody"},
		{src: `release.parent?.note ?? "none"`, want: "none"},
		{src: `release.parent?.author.roles[5] ?? ""`, err: "index 5 out of range for a list of 1", span: "1:1-1:32"},
		{src: `release.ticket ?? "none" == "CHG-1042"`, want: true},

		// Arithmetic.
		{src: "count + 1", want: int64(4)},
		{src: "count - 5", want: int64(-2)},
		{src: "ratio + 0.25", want: 0.75},
		{src: "release.soak + 2h", want: 28 * time.Hour},
		{src: "release.soak - 26h", want: time.Duration(0)},
		{src: "now - release.built_at", want: 3 * time.Hour},
		{src: "release.built_at + 3h == now", want: true},
		{src: "now - 3h == release.built_at", want: true},
		{src: "-count", want: int64(-3)},
		{src: "-ratio", want: -0.5},
		{src: "-min_soak", want: -24 * time.Hour},
		{src: "release.soak + 2h >= min_soak", want: true},

		// Quantifiers.
		{src: `any r in actor.roles: r like "sre-*"`, want: true},
		{src: `any r in actor.roles: r like "admin-*"`, want: false},
		{src: `all r in actor.roles: r != "admin"`, want: true},
		{src: `all r in actor.roles: r == "deployer"`, want: false},
		{src: "all a in actor.teams: any b in service.owners: a == b", want: false},
		{src: "any a in actor.teams: any b in service.owners: a == b", want: true},
		{src: "any n in service.counts: n > count - 1", want: true},
		{src: "any r in actor.roles: r == \"sre-eu\" and actor.roles[9] == \"\"", err: "index 9 out of range for a list of 2", span: "1:41-1:55"},

		// Filters keep the range's Go type and its order.
		{src: `filter r in actor.roles: r like "sre-*"`, want: []string{"sre-eu"}},
		{src: `filter r in actor.roles: r != actor.name`, want: []string{"deployer", "sre-eu"}},
		{src: "filter r in actor.roles: false", want: []string{}},
		{src: "filter n in service.counts: n > 1", want: []int{2, 3}},
		{src: `filter s in ["a", "b", "c"]: s != "b"`, want: []any{"a", "c"}},
		{src: `filter s in {"a": ["x"]}["b"]: true`, want: []any{}},
		{src: "filter t in actor.teams: any o in service.owners: t == o", want: []string{"payments"}},
		{src: `"sre-eu" in (filter r in actor.roles: r != "deployer")`, want: true},
		{src: `(filter r in actor.regions: r != "ap-1") all in ["eu-1", "us-1"]`, want: true},
		{src: "any r in (filter r2 in actor.roles: false): true", want: false},
		{src: "filter r in actor.roles: actor.roles[9] == r", err: "index 9 out of range for a list of 2", span: "1:26-1:40"},

		// Runtime errors.
		{src: "actor.roles[2]", err: "index 2 out of range for a list of 2", span: "1:1-1:15"},
		{src: "actor.roles[-1]", err: "index -1 out of range for a list of 2", span: "1:1-1:16"},
		{src: "9223372036854775807 + count", err: "integer overflow in `(9223372036854775807 + count)`", span: "1:1-1:28"},
		{src: "-9223372036854775807 - count", err: "integer overflow in `((-9223372036854775807) - count)`", span: "1:1-1:29"},
		{src: "106751d + release.soak", err: "duration overflow in `(106751d + release.soak)`", span: "1:1-1:23"},
		{src: `fail("x")`, err: "host function fail failed: boom: x", span: "1:1-1:10"},
		{src: `fail("x") == "y" or true`, err: "host function fail failed: boom: x", span: "1:1-1:10"},
		{src: `unbound("x")`, err: "host function unbound failed: no implementation in this sigil binary", help: "this sigil binary has only unbound's signature from the kind file; stub it with `stubs:` in the test file or `--stub unbound=VALUE` on sigil eval, or evaluate with a host binary built with sigil's pkg/cli, which links the real function in", span: "1:1-1:13"},
		{src: `advised("x")`, err: "host function advised failed: no quota", span: "1:1-1:13"},
		{src: `unmatched("x")`, err: `host function unmatched failed: no stubbed call matches unmatched("x")`, help: "add these args under the stub's calls, or give the stub a returns or error for every other call", span: "1:1-1:15"},
		{src: `stubbed("x")`, err: "host function stubbed failed: down", help: "the stub's error: fails the call, as the host function failing would; a test case expects it with `expect: {error: ...}`", span: "1:1-1:13"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			e, f := setup(t, tt.src, tt.assert)
			v, err := eval.Run(e, f)
			if tt.err != "" {
				if err == nil {
					t.Fatalf("Run() = %v, want error %q", v, tt.err)
				}
				if err.Msg != tt.err {
					t.Errorf("Msg  = %q\nwant   %q", err.Msg, tt.err)
				}
				if err.Help != tt.help {
					t.Errorf("Help = %q\nwant   %q", err.Help, tt.help)
				}
				if got := err.Pos.String() + "-" + err.End.String(); got != tt.span {
					t.Errorf("span = %s, want %s", got, tt.span)
				}
				return
			}
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			got := v.Interface()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Run() = %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

// TestEvalIsRepeatable evaluates one compiled expression against two
// inputs, as a host does: compile once, evaluate many.
func TestEvalIsRepeatable(t *testing.T) {
	e, f := setup(t, `"deployer" in actor.roles and release.soak >= min_soak`, false)
	if v, err := eval.Run(e, f); err != nil || !eval.Bool(v) {
		t.Fatalf("first input: %v, %v", v, err)
	}
	other := input
	other.Actor.Roles = []string{"viewer"}
	f.Input = reflect.ValueOf(other)
	if v, err := eval.Run(e, f); err != nil || eval.Bool(v) {
		t.Fatalf("second input: %v, %v", v, err)
	}
}

func TestNilCollections(t *testing.T) {
	e, f := setup(t, `"a" in actor.roles or actor.roles any in service.owners or service.labels has "k" or service.labels has {"k": "v"}`, false)
	empty := Input{}
	f.Input = reflect.ValueOf(empty)
	v, err := eval.Run(e, f)
	if err != nil || eval.Bool(v) {
		t.Fatalf("nil collections: %v, %v", v, err)
	}
	e, f = setup(t, `service.labels["k"] == "" and len(actor.roles) == 0 and actor.roles all in service.owners and service.labels has {} and (all r in actor.roles: false) and not (any x in (filter t in actor.teams: true): true)`, false)
	f.Input = reflect.ValueOf(empty)
	if v, err := eval.Run(e, f); err != nil || !eval.Bool(v) {
		t.Fatalf("nil collections read as empty: %v, %v", v, err)
	}
}

// TestUnsetList ranges over a list with no value at all, which a host
// binding a slot can produce: quantifiers and filters read it as empty.
func TestUnsetList(t *testing.T) {
	tests := []struct {
		src  string
		want any
	}{
		{src: "any t in tiers: true", want: false},
		{src: "all t in tiers: false", want: true},
		{src: "filter t in tiers: true", want: []any{}},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			e, f := setup(t, tt.src, false)
			f.Set(1, reflect.Value{}) // tiers, the second name setup declares
			v, err := eval.Run(e, f)
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
			if got := v.Interface(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Run() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAbsentOptional(t *testing.T) {
	e, f := setup(t, `release.ticket ?? "none"`, false)
	noTicket := input
	noTicket.Release.Ticket = nil
	f.Input = reflect.ValueOf(noTicket)
	v, err := eval.Run(e, f)
	if err != nil || v.Interface() != "none" {
		t.Fatalf("absent optional: %v, %v", v, err)
	}
}

func TestAbsentChain(t *testing.T) {
	noParent := input
	noParent.Release.Parent = nil
	for src, want := range map[string]any{
		"release.parent?.id ?? 0":                     int64(0),
		`release.parent?.author.roles[5] ?? "none"`:   "none",
		`release.parent?.merged_by?.name ?? "nobody"`: "nobody",
		"present release.parent":                      false,
		"present release.parent?.author":              false,
	} {
		e, f := setup(t, src, false)
		f.Input = reflect.ValueOf(noParent)
		v, err := eval.Run(e, f)
		if err != nil || v.Interface() != want {
			t.Errorf("%s: %v, %v; want %v", src, v, err, want)
		}
	}
}

func TestCompileUnchecked(t *testing.T) {
	x, _ := parser.ParseExpr("p.sigil", []byte("a + b"))
	_, err := eval.Compile(x, check.New("p.sigil").Info(), eval.NewScope(&gokind.Binding{}))
	if err == nil || !strings.Contains(err.Msg, "wasn't checked") {
		t.Errorf("Compile() error = %v", err)
	}
}
