package policy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// The host's enum types for the enum tests.
type (
	Tier string

	TierInput struct {
		Tier  Tier   `policy:"tier"`
		Tiers []Tier `policy:"tiers"`
	}
	MoveData struct {
		To Tier `policy:"to,default=internal"`
	}
)

const (
	TierCritical Tier = "critical"
	TierStandard Tier = "standard"
	TierInternal Tier = "internal"
)

var (
	Stay = policy.NewDecision[policy.None]("stay", "no_rule_matched")
	Move = policy.NewDecision[MoveData]("move", "promote")

	Tiers = policy.NewKind[TierInput]("Tiers",
		policy.WithVersion(1),
		policy.WithEnum(TierCritical, TierStandard, TierInternal),
		policy.WithDecisions(Stay, Move),
		policy.WithDefault(Stay.Reason("no_rule_matched")),
		policy.WithFunc("up", func(t Tier) Tier { return t + "+" }),
	)
)

func TestWithEnumSchema(t *testing.T) {
	want := "kind Tiers version 1\n\nenum Tier: critical | standard | internal\n\ninput tier: Tier\ninput tiers: list<Tier>\n\nfn up(Tier) -> Tier\n\n" +
		"decision stay {\n  reason: no_rule_matched\n}\n\ndecision move {\n  reason: promote\n  to: Tier = internal\n}\n"
	if got := Tiers.Schema(); !strings.HasPrefix(got, want) {
		t.Errorf("Schema() =\n%s\nwant it to start with\n%s", got, want)
	}
}

// TestEnumParams binds params of enum types from Go: the enum's own type
// is accepted when its value is declared, and anything else is a
// compile error.
func TestEnumParams(t *testing.T) {
	tests := []struct {
		name  string
		param string // the declaration
		value any
		want  string // the diagnostic's message; empty when the value is accepted
		help  string
	}{
		{name: "a value", param: "floor: Tier = standard", value: TierCritical},
		{name: "a list", param: "floors: list<Tier> = []", value: []Tier{TierStandard, TierInternal}},
		{name: "a map", param: "limits: map<Tier, int> = {}", value: map[Tier]int{TierCritical: 1}},
		{name: "a value outside the enum", param: "floor: Tier = standard", value: Tier("gold"),
			want: `param floor: "gold" is not a value of Tier`, help: "Tier declares: critical, standard, internal"},
		{name: "a list element outside the enum", param: "floors: list<Tier> = []", value: []Tier{TierStandard, ""},
			want: `param floors: "" is not a value of Tier`},
		{name: "a map key outside the enum", param: "limits: map<Tier, int> = {}", value: map[Tier]int{"zinc": 1, "gold": 2},
			want: `param limits: "gold" is not a value of Tier`},
		{name: "a map of lists", param: "groups: map<string, list<Tier>> = {}", value: map[string][]Tier{"eu": {TierCritical}, "us": {"gold"}},
			want: `param groups: "gold" is not a value of Tier`},
		{name: "optionals", param: "backups: list<?Tier> = []", value: []*Tier{nil, new(Tier("gold"))}, want: `param backups: "gold" is not a value of Tier`},
		{name: "a plain string", param: "floor: Tier = standard", value: "critical", want: "param floor: expected Tier, found string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, _, _ := strings.Cut(tt.param, ":")
			src := "policy p: Tiers@1\nparam " + tt.param + "\nwhen true { stay(reason: no_rule_matched) }"
			_, err := Tiers.Compile(src, "p", policy.Params{name: tt.value})
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Compile() = %v, want no error", err)
				}
				return
			}
			ce, ok := errors.AsType[*policy.CompileError](err)
			if !ok {
				t.Fatalf("error = %v, want a *CompileError", err)
			}
			if len(ce.Diagnostics) != 1 || ce.Diagnostics[0].Message != tt.want {
				t.Fatalf("Diagnostics = %+v, want one with message %q", ce.Diagnostics, tt.want)
			}
			if tt.help != "" && ce.Diagnostics[0].Help != tt.help {
				t.Errorf("help = %q, want %q", ce.Diagnostics[0].Help, tt.help)
			}
		})
	}
}

// TestEnumEval evaluates rules over enum inputs: a declared value flows
// into the typed payload, and one outside the enum is a runtime error
// with the default, but only when a rule reads it.
func TestEnumEval(t *testing.T) {
	p, err := Tiers.Compile(`policy p: Tiers@1

when tier == standard {
  move(reason: promote, to: critical)
}

when tier == internal and critical in tiers {
  move(reason: promote)
}
`, "p")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		input TierInput
		want  Tier   // the move's target; empty for stay
		err   string // the runtime error's message
	}{
		{name: "a declared value", input: TierInput{Tier: TierStandard}, want: TierCritical},
		{name: "the payload default", input: TierInput{Tier: TierInternal, Tiers: []Tier{TierCritical}}, want: TierInternal},
		{name: "an unread value outside the enum", input: TierInput{Tier: TierCritical, Tiers: []Tier{"gold"}}},
		{name: "a read value outside the enum", input: TierInput{Tier: "gold"}, err: `tier: "gold" is not a value of Tier`},
		{name: "a read list element outside the enum", input: TierInput{Tier: TierInternal, Tiers: []Tier{"gold"}}, err: `tiers[0]: "gold" is not a value of Tier`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := p.Eval(context.Background(), tt.input)
			if tt.err != "" {
				re, ok := errors.AsType[*policy.RuntimeError](err)
				if !ok || re.Message != tt.err {
					t.Fatalf("Eval() error = %v, want a *RuntimeError %q", err, tt.err)
				}
				if re.Help != "Tier declares: critical, standard, internal" {
					t.Errorf("Help = %q", re.Help)
				}
				if !Stay.Reason("no_rule_matched").Is(res) {
					t.Errorf("result = %s(%s), want the default", res.Decision, res.Reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("Eval() error = %v", err)
			}
			m, moved := Move.Match(res)
			if got := map[bool]Tier{true: m.To}[moved]; got != tt.want {
				t.Errorf("moved to %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEnumHostFunction checks a host function's enum result like an
// input's: a value outside the enum is a runtime error.
func TestEnumHostFunction(t *testing.T) {
	p, err := Tiers.Compile("policy p: Tiers@1\nwhen up(tier) == critical { move(reason: promote) }", "p")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Eval(context.Background(), TierInput{Tier: TierStandard})
	if re, ok := errors.AsType[*policy.RuntimeError](err); !ok || re.Message != `up(tier): "standard+" is not a value of Tier` {
		t.Errorf("Eval() error = %v", err)
	}
}

func TestWithEnumPanics(t *testing.T) {
	type Plan string
	tests := []struct {
		name string
		fn   func()
		want []string // fragments of the panic message
	}{
		{name: "string itself", fn: func() {
			policy.NewKind[TierInput]("K", policy.WithVersion(1), policy.WithEnum("a", "b"),
				policy.WithDecisions(Stay), policy.WithDefault(Stay.Reason("no_rule_matched")))
		}, want: []string{"WithEnum: string is not a named type (declare a named type, like `type Tier string`"}},
		{name: "registered twice", fn: func() {
			policy.NewKind[TierInput]("K", policy.WithVersion(1), policy.WithEnum(TierCritical), policy.WithEnum(TierStandard),
				policy.WithDecisions(Stay), policy.WithDefault(Stay.Reason("no_rule_matched")))
		}, want: []string{"enum Tier is registered twice"}},
		{name: "no values", fn: func() {
			policy.NewKind[TierInput]("K", policy.WithVersion(1), policy.WithEnum[Tier](),
				policy.WithDecisions(Stay), policy.WithDefault(Stay.Reason("no_rule_matched")))
		}, want: []string{"enum Tier declares no values"}},
		{name: "a value named like an input", fn: func() {
			policy.NewKind[TierInput]("K", policy.WithVersion(1), policy.WithEnum(TierCritical, "tiers"), policy.WithEnum[Plan]("stay", "when"),
				policy.WithDecisions(Stay), policy.WithDefault(Stay.Reason("no_rule_matched")))
		}, want: []string{`value "tiers" collides with input "tiers"`, `value "stay" collides with decision "stay"`, `invalid value "when"`}},
		{name: "an enum as a map value", fn: func() {
			type Bad struct {
				M map[string]Tier `policy:"m"`
			}
			policy.NewKind[Bad]("K", policy.WithVersion(1), policy.WithEnum(TierCritical),
				policy.WithDecisions(Stay), policy.WithDefault(Stay.Reason("no_rule_matched")))
		}, want: []string{"map value type can't be enum Tier"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				msg, ok := recover().(string)
				if !ok {
					t.Fatal("NewKind did not panic with a message")
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
