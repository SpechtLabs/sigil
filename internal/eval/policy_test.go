package eval_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
)

type (
	ReviewData struct {
		Approvers []string `policy:"approvers"`
	}
	ApproveData struct {
		Bake   time.Duration `policy:"bake,default=1h"`
		Ticket string        `policy:"ticket,default=\"\""`
	}
	TagData struct {
		Labels map[string]string `policy:"labels"`
		Count  int               `policy:"count"`
		Ratio  float64           `policy:"ratio,default=0.5"`
		At     time.Time         `policy:"at"`
		Owners []string          `policy:"owners,default=[]"`
	}
)

const production = `policy deploy.production: Test@1
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
param min_soak: duration = 24h
let owns_service = actor.teams any in service.owners
let cleared = split(service.labels["regions"], ",") all in actor.regions
let eligible = "deployer" in actor.roles and environment == "production"
when not eligible {
  deny(reason: not_eligible)
}
when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}
when cleared {
  when service.tier == "critical"
    and "release_manager" in actor.roles {
    approve(reason: release_manager)
  }
  when service.tier in tiers
    and owns_service {
    review(reason: service_owner, approvers: approvers)
  }
}
when cleared and "payments-sre" in actor.teams {
  approve(reason: payments_sre, bake: 15m, ticket: release.ticket ?? "none")
}`

// evalCase is one row of TestPolicyEval.
type evalCase struct {
	name     string
	src      string
	collect  bool
	input    func(Input) Input
	want     string // describe(out)
	winner   string // "decision reason", or "default"
	payload  map[string]any
	typed    any    // the winner's payload struct, when checked
	failed   string // failing assert reasons, space-separated
	conflict string // the conflict's message, when resolution fails
	cause    string // the runtime error behind the first failure, if any
	err      string
}

func TestPolicyEval(t *testing.T) {
	approvers := reflect.ValueOf([]string{"payments-leads"})
	tests := []evalCase{
		{
			name: "deny outranks approve", src: production,
			input: func(in Input) Input { in.Release.Soak = 2 * time.Hour; return in },
			want: "deny soak_too_short 12:3 [release.soak < 1d and not release.hotfix] *\n" +
				"approve payments_sre 25:3 [cleared and \"payments-sre\" in actor.teams]",
			winner: "deny soak_too_short", payload: map[string]any{},
		},
		{
			name: "only the team rule fires", src: production,
			input:  func(in Input) Input { in.Service.Tier = "standard"; in.Service.Owners = []string{"other"}; return in },
			want:   "approve payments_sre 25:3 [cleared and \"payments-sre\" in actor.teams] *",
			winner: "approve payments_sre", payload: map[string]any{"bake": 15 * time.Minute, "ticket": "CHG-1042"},
			typed: ApproveData{Bake: 15 * time.Minute, Ticket: "CHG-1042"},
		},
		{
			name: "ranked reasons decide a tie within a decision", src: production,
			input: func(in Input) Input { in.Actor.Roles = append(in.Actor.Roles, "release_manager"); return in },
			want: "approve release_manager 17:5 [cleared | service.tier == \"critical\" and \"release_manager\" in actor.roles] *\n" +
				"approve payments_sre 25:3 [cleared and \"payments-sre\" in actor.teams]",
			winner: "approve release_manager", payload: map[string]any{"bake": time.Hour, "ticket": ""},
		},
		{
			name: "equal candidates fold into one", src: "policy p: Test@1\nwhen true { deny(reason: a) }\nwhen release.soak > 0s { deny(reason: a) }",
			want: "deny a 2:13 [true] *\ndeny a 3:26 [release.soak > 0s]", winner: "deny a", payload: map[string]any{},
		},
		{
			name: "the same reason with different payloads conflicts", src: "policy p: Test@1\nwhen true { review(reason: a, approvers: [\"x\"]) }\nwhen release.soak > 0s { review(reason: a, approvers: [\"y\"]) }",
			want: "review a 2:13 [true]\nreview a 3:26 [release.soak > 0s]", conflict: "collect one: 2 candidates at the top rank",
		},
		{
			name: "unranked reasons of one decision conflict", src: "policy p: Test@1\nwhen true { deny(reason: a) }\nwhen true { deny(reason: b) }",
			want: "deny a 2:13 [true]\ndeny b 3:13 [true]", conflict: "collect one: 2 candidates at the top rank",
		},
		{
			name: "an exclusive set conflicts before ranking", src: "policy p: Test@1\nwhen true { deny(reason: d) }\nwhen true { approve(reason: a) }\nwhen true { review(reason: c, approvers: []) }",
			want: "deny d 2:13 [true]\nreview c 4:13 [true]\napprove a 3:13 [true]", conflict: "exclusive review.c, approve.a: more than one fired",
		},
		{
			name: "an exclusive set needs two members", src: "policy p: Test@1\nwhen true { deny(reason: d) }\nwhen true { review(reason: c, approvers: []) }",
			want: "deny d 2:13 [true] *\nreview c 3:13 [true]", winner: "deny d", payload: map[string]any{},
		},
		{
			name: "review with a bound param", src: production,
			input: func(in Input) Input {
				in.Service.Tier = "standard"
				in.Actor.Teams = []string{"payments"}
				return in
			},
			want:   "review service_owner 21:5 [cleared | service.tier in [\"standard\", \"internal\"] and owns_service] *",
			winner: "review service_owner", payload: map[string]any{"approvers": []string{"payments-leads"}},
			typed: ReviewData{Approvers: []string{"payments-leads"}},
		},
		{
			name: "a filter takes the requestor off the approvers", src: "policy p: Test@1\nlet others = filter name in [\"alice\", \"bob\", \"carol\"]: name != actor.name\nwhen true { review(reason: a, approvers: others) }",
			want: "review a 3:13 [true] *", winner: "review a", payload: map[string]any{"approvers": []string{"bob", "carol"}},
			typed: ReviewData{Approvers: []string{"bob", "carol"}},
		},
		{
			name: "a let's quantifier keeps its variable apart from an outer one of the same name", src: "policy p: Test@1\nlet eu = any r in actor.regions: r == \"ap-1\"\nwhen any r in actor.roles: eu and r == \"deployer\" { deny(reason: a) }",
			want: "deny a 3:53 [any r in actor.roles: eu and r == \"deployer\"] *", winner: "deny a", payload: map[string]any{},
		},
		{
			name: "a let's filter keeps its variable apart from an outer one of the same name", src: "policy p: Test@1\nlet others = filter r in actor.teams: r != \"payments\"\nwhen any r in actor.roles: \"payments-sre\" in others and r == \"deployer\" { deny(reason: a) }",
			want: "deny a 3:75 [any r in actor.roles: \"payments-sre\" in others and r == \"deployer\"] *", winner: "deny a", payload: map[string]any{},
		},
		{
			name: "nothing fires", src: production,
			input: func(in Input) Input {
				in.Service.Tier = "standard"
				in.Actor.Teams = []string{"other"}
				return in
			},
			want: "", winner: "default", payload: map[string]any{}, typed: None{},
		},
		{
			name: "payload fields are converted to the host's types",
			src:  "policy p: Test@1\nwhen true { tag(reason: t, labels: {\"team\": service.labels[\"team\"], \"env\": environment}, count: count + 1, at: release.built_at, owners: service.owners) }",
			want: "tag t 2:13 [true] *", winner: "tag t",
			payload: map[string]any{"labels": map[string]string{"team": "payments", "env": "production"}, "count": 4, "ratio": 0.5, "at": built, "owners": []string{"payments", "platform"}},
			typed:   TagData{Labels: map[string]string{"team": "payments", "env": "production"}, Count: 4, Ratio: 0.5, At: built, Owners: []string{"payments", "platform"}},
		},
		{
			name: "payload defaults of every shape",
			src:  "policy p: Test@1\nwhen true { tag(reason: t, labels: {}, count: 0, at: release.built_at) }",
			want: "tag t 2:13 [true] *", winner: "tag t",
			payload: map[string]any{"labels": map[string]string{}, "count": 0, "ratio": 0.5, "at": built, "owners": []string{}},
			typed:   TagData{Labels: map[string]string{}, Count: 0, Ratio: 0.5, At: built, Owners: []string{}},
		},
		{
			name: "several constructors in one body", src: "policy p: Test@1\nwhen true {\n  approve(reason: a)\n  deny(reason: b)\n  review(reason: a, approvers: [])\n}",
			want: "deny b 4:3 [true] *\nreview a 5:3 [true]\napprove a 3:3 [true]", winner: "deny b", payload: map[string]any{},
		},
		{
			name: "a let is evaluated at most once and only when read", src: "policy p: Test@1\nlet boom = fail(\"x\") == \"y\"\nlet ok = release.hotfix or not release.hotfix\nwhen ok and ok { deny(reason: a) }\nwhen false { when boom { deny(reason: b) } }",
			want: "deny a 4:18 [ok and ok] *", winner: "deny a", payload: map[string]any{},
		},
		{
			name: "runtime error in a condition", src: "policy p: Test@1\nwhen actor.roles[9] == \"x\" { deny(reason: a) }",
			err: "p.sigil:2:6: index 9 out of range for a list of 2",
		},
		{
			name: "runtime error in a payload", src: "policy p: Test@1\nwhen true { review(reason: a, approvers: [fail(\"x\")]) }",
			err: "p.sigil:2:43: host function fail failed: boom: x",
		},
		{
			name: "runtime error in an unreached block never happens", src: "policy p: Test@1\nwhen false { when actor.roles[9] == \"x\" { deny(reason: a) } }",
			want: "", winner: "default", payload: map[string]any{},
		},
		{
			name: "asserts run after the outcome", src: "policy p: Test@1\nassert(\"no_deny\", deny not in outcome)\nassert(\"soak\", release.soak >= 0s)\nwhen true {\n  assert(\"approved\", approve in outcome)\n  approve(reason: a)\n}\nwhen false { assert(\"unreached\", false) }",
			want: "approve a 6:3 [true] *", winner: "approve a", payload: map[string]any{"bake": time.Hour, "ticket": ""},
		},
		{
			name: "a failing input assert stops the rules", src: "policy p: Test@1\nassert(\"want_deny\", deny in outcome)\nwhen true {\n  approve(reason: a)\n  assert(\"inner\", false)\n}\nassert(\"empty\", approve not in outcome)",
			want: "", winner: "default", payload: map[string]any{},
			failed: "inner",
		},
		{
			name: "failing outcome asserts are all reported in order", src: "policy p: Test@1\nassert(\"want_deny\", deny in outcome)\nwhen true {\n  approve(reason: a)\n  assert(\"approved\", approve in outcome)\n}\nassert(\"empty\", approve not in outcome)",
			want: "approve a 4:3 [true] *", winner: "approve a", payload: map[string]any{"bake": time.Hour, "ticket": ""},
			failed: "want_deny empty",
		},
		{
			name: "input asserts run before any rule", src: "policy p: Test@1\nassert(\"soak\", release.soak < 0s)\nwhen true { review(reason: a, approvers: [fail(\"x\")]) }",
			want: "", winner: "default", payload: map[string]any{}, failed: "soak",
		},
		{
			name: "a runtime error in an enclosing condition fails the asserts beneath", src: "policy p: Test@1\nwhen actor.roles[9] == \"x\" {\n  assert(\"a\", true)\n  when true { assert(\"b\", true) }\n}",
			want: "", winner: "default", payload: map[string]any{}, failed: "a b", cause: "index 9 out of range for a list of 2",
		},
		{
			name: "a scoped let is evaluated only when its body is reached", src: "policy p: Test@1\nwhen false {\n  let boom = fail(\"x\") == \"y\"\n  when boom { deny(reason: a) }\n}\nwhen true {\n  let two = count + 1\n  when two == 4 { deny(reason: b) }\n}",
			want: "deny b 8:19 [true | two == 4] *", winner: "deny b", payload: map[string]any{},
		},
		{
			name: "outcome is the default when nothing fires", src: "policy p: Test@1\nassert(\"default\", deny in outcome and approve not in outcome)",
			want: "", winner: "default", payload: map[string]any{},
		},
		{
			name: "runtime error in an assert is its failure", src: "policy p: Test@1\nassert(\"a\", actor.roles[9] == \"x\")",
			want: "", winner: "default", payload: map[string]any{}, failed: "a", cause: "p.sigil:2:13: index 9 out of range for a list of 2",
		},
		{
			name: "collecting kind returns every candidate in declaration order", src: "policy p: Test@1\nwhen true {\n  approve(reason: a)\n  deny(reason: b)\n}\nwhen release.hotfix { review(reason: c, approvers: []) }\nwhen not release.hotfix { deny(reason: d) }\nassert(\"both\", [deny, approve] all in outcome)\nassert(\"no_review\", review not in outcome)",
			collect: true,
			want:    "deny b 4:3 [true] *\ndeny d 7:27 [not release.hotfix] *\napprove a 3:3 [true] *", winner: "none",
		},
		{
			name: "an outcome assert reads a candidate's payload", src: "policy p: Test@1\nassert(\"no_self_review\", all r in outcome.review: actor.name not in r.approvers)\nwhen true { review(reason: a, approvers: [\"bob\"]) }",
			want: "review a 3:13 [true] *", winner: "review a", payload: map[string]any{"approvers": []string{"bob"}},
		},
		{
			name: "an outcome assert fails on a candidate's payload", src: "policy p: Test@1\nassert(\"no_self_review\", all r in outcome.review: actor.name not in r.approvers)\nwhen true { review(reason: a, approvers: [\"alice\", \"bob\"]) }",
			want: "review a 3:13 [true] *", winner: "review a", payload: map[string]any{"approvers": []string{"alice", "bob"}}, failed: "no_self_review",
		},
		{
			name: "a collecting kind's assert reads every candidate of the decision", src: "policy p: Test@1\nassert(\"no_self_review\", all r in outcome.review: actor.name not in r.approvers)\nassert(\"some_clean_review\", any r in outcome.review: actor.name not in r.approvers)\nwhen true {\n  review(reason: a, approvers: [\"bob\"])\n  review(reason: service_owner, approvers: [\"alice\"])\n}",
			collect: true, want: "review a 5:3 [true] *\nreview service_owner 6:3 [true] *", winner: "none", failed: "no_self_review",
		},
		{
			name: "the default is the candidate when nothing fires", src: "policy p: Test@1\nassert(\"default\", all d in outcome.deny: d.reason == deny.no_rule_matched)\nassert(\"one\", any d in outcome.deny: true)\nassert(\"no_review\", not (any r in outcome.review: true))",
			want: "", winner: "default", payload: map[string]any{},
		},
		{
			name: "a reason narrows the candidates, and payload defaults are read", src: "policy p: Test@1\nassert(\"bake\", all a in outcome.approve.release_manager: a.bake == 1h and a.ticket == \"\")\nassert(\"one\", any a in outcome.approve.release_manager: a.reason == approve.release_manager)\nassert(\"narrowed\", not (any a in outcome.approve.payments_sre: true))\nwhen true { approve(reason: release_manager) }",
			want: "approve release_manager 5:13 [true] *", winner: "approve release_manager", payload: map[string]any{"bake": time.Hour, "ticket": ""},
		},
		{
			name: "a filter ranges over candidates", src: "policy p: Test@1\nassert(\"bob_reviews\", all r in (filter x in outcome.review: \"bob\" in x.approvers): r.reason == review.a)\nassert(\"found\", any r in (filter x in outcome.review: \"bob\" in x.approvers): true)\nwhen true { review(reason: a, approvers: [\"bob\"]) }",
			want: "review a 4:13 [true] *", winner: "review a", payload: map[string]any{"approvers": []string{"bob"}},
		},
		{
			name: "candidates outranked by precedence aren't in the outcome", src: "policy p: Test@1\nassert(\"hidden\", not (any r in outcome.review: true))\nassert(\"denied\", all d in outcome.deny.a: d.reason == deny.a)\nwhen true { deny(reason: a) }\nwhen true { review(reason: a, approvers: [\"alice\"]) }",
			want: "deny a 4:13 [true] *\nreview a 5:13 [true]", winner: "deny a", payload: map[string]any{},
		},
		{
			name: "collecting kind with nothing fired", src: "policy p: Test@1\nwhen false { deny(reason: a) }\nassert(\"empty\", deny not in outcome)",
			collect: true, want: "", winner: "none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := compilePolicy(t, tt.src, tt.collect, map[string]eval.Value{"approvers": approvers})
			in := input
			if tt.input != nil {
				in = tt.input(in)
			}
			out, err := p.Eval(&in)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("Eval() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Eval() error: %v", err)
			}
			checkOutcome(t, tt, p, out)
		})
	}
}

// TestPolicyIsConcurrent evaluates one policy from several goroutines,
// as a host does; the race detector checks the frames don't share state.
func TestPolicyIsConcurrent(t *testing.T) {
	p := compilePolicy(t, production, false, map[string]eval.Value{"approvers": reflect.ValueOf([]string{"a"})})
	done := make(chan string, 8)
	for i := range 8 {
		go func() {
			in := input
			if i%2 == 0 {
				in.Release.Soak = time.Hour
			}
			out, err := p.Eval(&in)
			if err != nil {
				done <- err.Error()
				return
			}
			done <- out.Top[0].Reason
		}()
	}
	for range 8 {
		got := <-done
		if got != "soak_too_short" && got != "payments_sre" {
			t.Errorf("winner = %q", got)
		}
	}
}

// TestConflictOutcome checks the candidate a kind's conflict outcome
// compiles to: its decision and reason, the payload with field defaults
// filled in, typed too, and no position or policy, since no rule
// produced it. A kind without one has none, and a conflict falls back to
// the default.
func TestConflictOutcome(t *testing.T) {
	const src = "policy p: Test@1\n"
	o := testOptions(false)
	o.Conflict = &gokind.Default{Decision: "approve", Reason: "a"}
	c := compileWith(t, src, o, nil).ConflictOutcome()
	if c == nil {
		t.Fatal("ConflictOutcome() = nil")
	}
	if got := c.Decision.Name + " " + c.Reason; got != "approve a" {
		t.Errorf("ConflictOutcome() = %s, want approve a", got)
	}
	if want := map[string]any{"bake": time.Hour, "ticket": ""}; !reflect.DeepEqual(c.Payload, want) {
		t.Errorf("Payload = %#v, want %#v", c.Payload, want)
	}
	if want := (ApproveData{Bake: time.Hour}); !reflect.DeepEqual(c.Typed.Interface(), want) {
		t.Errorf("Typed = %#v, want %#v", c.Typed.Interface(), want)
	}
	if c.Pos.IsValid() || c.Policy != "" {
		t.Errorf("the conflict outcome has a position or policy: %+v", c.Rule)
	}
	if got := compilePolicy(t, src, false, nil).ConflictOutcome(); got != nil {
		t.Errorf("ConflictOutcome() without a declaration = %v, want nil", got)
	}
}

// compilePolicy builds the Test kind (ranked, or collecting when collect
// is set), checks src against it and compiles it with params bound.
func compilePolicy(t *testing.T, src string, collect bool, params map[string]eval.Value) *eval.Policy {
	t.Helper()
	return compileWith(t, src, testOptions(collect), params)
}

// testOptions declares the Test kind, ranked or collecting.
func testOptions(collect bool) gokind.Options {
	o := gokind.Options{
		Name: "Test", Version: 1, Input: typeOf[Input](),
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: typeOf[None](), Reasons: []string{"b", "a", "d", "not_eligible", "soak_too_short", "no_rule_matched"}},
			{Name: "review", Payload: typeOf[ReviewData](), Reasons: []string{"c", "a", "service_owner"}},
			{Name: "approve", Payload: typeOf[ApproveData](), Reasons: []string{"a", "release_manager", "payments_sre"}},
			{Name: "tag", Payload: typeOf[TagData](), Reasons: []string{"t"}},
		},
		Funcs:     []gokind.Func{{Name: "split", Fn: strings.Split}, {Name: "fail", Fn: fail}},
		Rankings:  []gokind.Ranking{{Decision: "approve", Reasons: []string{"release_manager", "payments_sre", "a"}}},
		Exclusive: [][]kind.Outcome{{{Decision: "review", Reason: "c"}, {Decision: "approve", Reason: "a"}}},
	}
	if collect {
		o.Collect = true
	} else {
		o.Ranked = true
		o.Default = &gokind.Default{Decision: "deny", Reason: "no_rule_matched"}
	}
	return o
}

// compileWith builds the kind o declares, checks src against it and
// compiles it with params bound.
func compileWith(t *testing.T, src string, o gokind.Options, params map[string]eval.Value) *eval.Policy {
	t.Helper()
	k, b, errs := gokind.Build(o)
	if errs != nil {
		t.Fatal(errs)
	}
	f, perrs := parser.ParseFile("p.sigil", []byte(src))
	if perrs != nil {
		t.Fatal(perrs)
	}
	doc := f.Docs[0].(*ast.PolicyDoc)
	c := check.New("p.sigil")
	c.Policy(doc, k)
	if errs := c.Errors(); errs != nil {
		t.Fatalf("check: %v", errs)
	}
	p, cerr := eval.CompilePolicy(&eval.Source{Doc: doc, Info: c.Info(), File: "p.sigil", Src: []byte(src)}, k, b, nil, eval.Options{Params: params})
	if cerr != nil {
		t.Fatalf("compile: %v (%s)", cerr, cerr.Help)
	}
	return p
}

// describe renders an outcome as one line per candidate, "decision reason
// line:col [conds]", with the winner marked.
func describe(out *eval.Outcome) string {
	lines := make([]string, 0, len(out.Candidates))
	for _, c := range out.Candidates {
		s := c.Decision.Name + " " + c.Reason + " " + c.Pos.String()
		if len(c.Conds) > 0 {
			conds := make([]string, len(c.Conds))
			for i, cond := range c.Conds {
				conds[i] = cond.Text
			}
			s += " [" + strings.Join(conds, " | ") + "]"
		}
		for _, t := range out.Top {
			if c == t {
				s += " *"
			}
		}
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n")
}

// checkOutcome compares an outcome with a table row's expectations.
func checkOutcome(t *testing.T, tt evalCase, p *eval.Policy, out *eval.Outcome) {
	t.Helper()
	if got := describe(out); got != tt.want {
		t.Errorf("candidates:\n%s\nwant:\n%s", got, tt.want)
	}
	if tt.conflict != "" {
		if out.Conflict == nil || out.Conflict.Msg != tt.conflict {
			t.Fatalf("Conflict = %v, want %q", out.Conflict, tt.conflict)
		}
		return
	}
	if out.Conflict != nil {
		t.Fatalf("unexpected conflict: %s", out.Conflict.Msg)
	}
	var winner *eval.Candidate
	if len(out.Top) > 0 {
		winner = out.Top[0]
	}
	switch tt.winner {
	case "default":
		if len(out.Top) != 0 {
			t.Fatalf("Top = %v, want the default", out.Top)
		}
		winner = p.Default()
	case "none":
		if p.Default() != nil {
			t.Fatalf("collecting kind has a default")
		}
		winner = nil
	default:
		if winner == nil || winner.Decision.Name+" "+winner.Reason != tt.winner {
			t.Fatalf("Top = %v, want %q", out.Top, tt.winner)
		}
	}
	if winner != nil && !reflect.DeepEqual(winner.Payload, tt.payload) {
		t.Errorf("Payload = %#v, want %#v", winner.Payload, tt.payload)
	}
	if tt.typed != nil && !reflect.DeepEqual(winner.Typed.Interface(), tt.typed) {
		t.Errorf("Typed = %#v, want %#v", winner.Typed.Interface(), tt.typed)
	}
	if tt.winner == "default" && (winner.Pos.IsValid() || winner.Policy != "") {
		t.Errorf("the default has a position or policy: %+v", winner.Rule)
	}
	var failed []string
	for _, fl := range out.Failed {
		failed = append(failed, fl.Assert.Reason)
	}
	if got := strings.Join(failed, " "); got != tt.failed {
		t.Errorf("Failed = %q, want %q", got, tt.failed)
	}
	cause := ""
	if len(out.Failed) > 0 && out.Failed[0].Err != nil {
		cause = out.Failed[0].Err.Error()
	}
	if !strings.HasSuffix(cause, tt.cause) || (tt.cause == "" && cause != "") {
		t.Errorf("cause = %q, want %q", cause, tt.cause)
	}
}
