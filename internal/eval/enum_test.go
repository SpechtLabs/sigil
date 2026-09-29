package eval_test

import (
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
)

// The host's enum types. Plan shares the value `standard` with Tier.
type (
	Tier string
	Plan string

	Account struct {
		Tier   Tier              `policy:"tier"`
		Tiers  []Tier            `policy:"tiers"`
		Limits map[Tier]int      `policy:"limits"`
		Backup *Tier             `policy:"backup"`
		Plan   Plan              `policy:"plan"`
		Groups map[string][]Tier `policy:"groups"`
		Labels map[string]int    `policy:"labels"`
		Matrix [][]string        `policy:"matrix"`
	}
	AccountInput struct {
		Account Account  `policy:"account"`
		Tier    Tier     `policy:"tier"`
		Maybe   *Tier    `policy:"maybe"`
		Before  *Account `policy:"before"`
	}
	MoveData struct {
		To   Tier   `policy:"to"`
		Also []Tier `policy:"also,default=[]"`
		Plan Plan   `policy:"plan,default=basic"`
		From *Tier  `policy:"from,default=internal"`
	}
)

// account is an input every enum value of which is declared.
func account() AccountInput {
	backup := Tier("internal")
	return AccountInput{
		Account: Account{
			Tier: "critical", Tiers: []Tier{"critical", "standard"}, Limits: map[Tier]int{"critical": 3, "internal": 1},
			Backup: &backup, Plan: "standard", Groups: map[string][]Tier{"eu": {"internal"}},
		},
		Tier: "standard",
	}
}

func TestEnumEval(t *testing.T) {
	gold := Tier("gold")
	tests := []struct {
		name   string
		src    string // the policy's rules
		input  func(*AccountInput)
		winner string // "decision reason", or "default"
		typed  any    // the winner's payload, when checked
		conds  string // the winner's conditions, when checked
		err    string // the runtime error's message
		help   string // and its help
	}{
		{name: "equality", src: `when account.tier == critical { move(reason: ok, to: account.tier) }`,
			winner: "move ok", typed: MoveData{To: "critical", Also: []Tier{}, Plan: "basic", From: new(Tier("internal"))}},
		{name: "inequality", src: `when account.tier != critical { move(reason: ok, to: standard) }`, winner: "default"},
		{name: "in a list literal", src: `when tier in [standard, internal] { move(reason: ok, to: tier) }`,
			winner: "move ok", typed: MoveData{To: "standard", Also: []Tier{}, Plan: "basic", From: new(Tier("internal"))}},
		{name: "not in a host list", src: `when internal not in account.tiers { move(reason: ok, to: internal) }`, winner: "move ok"},
		{name: "list operators", src: "when [critical] all in account.tiers and [internal, standard] any in account.tiers and [standard, internal] one in account.tiers and [critical, critical] exclusive in account.tiers { move(reason: ok, to: critical) }",
			winner: "move ok"},
		{name: "has and index on a host map", src: `when account.limits has critical and account.limits[critical] == 3 and not (account.limits has standard) { move(reason: ok, to: critical) }`,
			winner: "move ok"},
		{name: "map has a map", src: `when account.limits has {internal: 1} { move(reason: ok, to: internal) }`, winner: "move ok"},
		{name: "a literal map indexed by a host value", src: "when {critical: \"a\", standard: \"b\"}[account.tier] == \"a\" { move(reason: ok, to: critical) }",
			winner: "move ok"},
		{name: "a literal map has a host value", src: `when {critical: 1} has account.tier { move(reason: ok, to: critical) }`, winner: "move ok"},
		{name: "coalesce and present", src: `when present account.backup and (account.backup ?? critical) == internal and (maybe ?? standard) == standard { move(reason: ok, to: internal) }`,
			winner: "move ok"},
		{name: "a literal map has a host list's element", src: `when any x in account.tiers: {internal: 1, critical: 2} has x { move(reason: ok, to: standard) }`, winner: "move ok"},
		{name: "a value two enums declare, typed by the other side", src: `when any x in account.tiers: {standard: 1} has x { move(reason: ok, to: standard, plan: standard) }`, winner: "move ok"},
		{name: "a quantifier over a host list", src: `when any x in account.tiers: x == standard { move(reason: ok, to: standard) }`, winner: "move ok"},
		{name: "a filter keeps host values", src: `when standard in (filter x in account.tiers: x != critical) { move(reason: ok, to: standard) }`, winner: "move ok"},
		{name: "a let holds a value", src: "let top = critical\nwhen account.tier == top { move(reason: ok, to: top) }", winner: "move ok"},
		{name: "a qualified value", src: `when account.tier == Tier.critical and Tier.critical != Tier.internal { move(reason: ok, to: Tier.critical) }`,
			winner: "move ok", typed: MoveData{To: "critical", Also: []Tier{}, Plan: "basic", From: new(Tier("internal"))}},
		{name: "qualified values in a list", src: `when Tier.standard in account.tiers and account.tier in [Tier.internal, Tier.critical] { move(reason: ok, to: critical) }`, winner: "move ok"},
		{name: "a qualified value two enums declare", src: "let plan = Plan.standard\nlet level = Tier.standard\nwhen account.plan == plan and level in account.tiers { move(reason: ok, to: level, plan: Plan.standard) }",
			winner: "move ok", typed: MoveData{To: "standard", Also: []Tier{}, Plan: "standard", From: new(Tier("internal"))}},
		{name: "a qualified map key", src: `when account.limits[Tier.critical] == 3 and {Tier.critical: 1} has account.tier { move(reason: ok, to: critical) }`, winner: "move ok"},
		{name: "a param's value in the trace", src: "param floor: Tier = standard\nwhen account.tier != floor { move(reason: ok, to: floor) }",
			winner: "move ok", conds: "account.tier != standard"},
		{name: "a list param's value in the trace", src: "param floors: list<Tier> = [standard, internal]\nwhen account.tier not in floors { move(reason: ok, to: critical) }",
			winner: "move ok", conds: "account.tier not in [standard, internal]"},
		{name: "a host function takes and returns values", src: `when next(account.tier) == standard and any_of([internal, standard]) { move(reason: ok, to: next(account.tier)) }`,
			winner: "move ok"},
		{name: "payload values set back into Go", src: `when true { move(reason: ok, to: internal, also: [critical, account.tier], plan: standard, from: account.backup) }`,
			winner: "move ok", typed: MoveData{To: "internal", Also: []Tier{"critical", "critical"}, Plan: "standard", From: new(Tier("internal"))}},
		{name: "a value no rule reads never fails", src: `when true { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Plan = ""; in.Account.Tiers = []Tier{"nope"} }, winner: "move ok"},

		{name: "a plain field outside its enum", src: `when account.tier == critical { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Tier = "critcal" },
			err:   `account.tier: "critcal" is not a value of Tier`, help: "Tier declares: critical, standard, internal"},
		{name: "the zero value", src: `when account.plan == basic { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Plan = "" },
			err:   `account.plan: "" is not a value of Plan`, help: "Plan declares: basic, standard"},
		{name: "an input outside its enum", src: `when tier == critical { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Tier = "gold" }, err: `tier: "gold" is not a value of Tier`},
		{name: "a list element", src: `when critical in account.tiers { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Tiers = []Tier{"critical", "gold"} }, err: `account.tiers[1]: "gold" is not a value of Tier`},
		{name: "a map key", src: `when account.limits has critical { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Limits = map[Tier]int{"zinc": 1, "gold": 2, "critical": 3} },
			err:   `account.limits: key "gold" is not a value of Tier`},
		{name: "a list in a map value", src: `when account.groups has "eu" { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Groups = map[string][]Tier{"us": {"gold"}, "eu": {"internal"}} },
			err:   `account.groups["us"][0]: "gold" is not a value of Tier`},
		{name: "a present optional", src: `when present account.backup { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Backup = &gold }, err: `account.backup: "gold" is not a value of Tier`},
		{name: "an optional chain, absent", src: `when (before?.tier ?? critical) == critical { move(reason: ok, to: critical) }`, winner: "move ok"},
		{name: "an optional chain, present", src: `when (before?.tier ?? critical) == critical { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Before = &Account{Tier: "gold"} }, err: `before?.tier: "gold" is not a value of Tier`},
		{name: "a nil map", src: `when not (account.limits has critical) { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Limits = nil }, winner: "move ok"},
		{name: "values of types without enums", src: "when not (account.labels has \"x\") and not ([] in account.matrix) { move(reason: ok, to: critical) }", winner: "move ok"},
		{name: "an absent optional", src: `when not present maybe { move(reason: ok, to: critical) }`, winner: "move ok"},
		{name: "a host function's result", src: `when next(account.tier) == critical { move(reason: ok, to: critical) }`,
			input: func(in *AccountInput) { in.Account.Tier = "internal" }, err: `next(account.tier): "gold" is not a value of Tier`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := compileEnums(t, tt.src)
			in := account()
			if tt.input != nil {
				tt.input(&in)
			}
			out, err := p.Eval(&in)
			if tt.err != "" {
				if err == nil || err.Msg != tt.err {
					t.Fatalf("Eval() error = %v, want %q", err, tt.err)
				}
				if tt.help != "" && err.Help != tt.help {
					t.Errorf("help = %q, want %q", err.Help, tt.help)
				}
				return
			}
			if err != nil {
				t.Fatalf("Eval() error: %v", err)
			}
			winner := p.Default()
			if len(out.Top) > 0 {
				winner = out.Top[0]
			}
			got := winner.Decision.Name + " " + winner.Reason
			if len(out.Top) == 0 {
				got = "default"
			}
			if got != tt.winner {
				t.Fatalf("winner = %s, want %s", got, tt.winner)
			}
			if tt.typed != nil && !reflect.DeepEqual(winner.Typed.Interface(), tt.typed) {
				t.Errorf("Typed = %#v, want %#v", winner.Typed.Interface(), tt.typed)
			}
			if tt.conds != "" && (len(winner.Conds) == 0 || winner.Conds[0].Text != tt.conds) {
				t.Errorf("Conds = %v, want %q", winner.Conds, tt.conds)
			}
		})
	}
}

// compileEnums checks the rules as a policy against the Accounts kind
// and compiles them.
func compileEnums(t testing.TB, rules string) *eval.Policy {
	t.Helper()
	k, b := accountsKind(t)
	src := "policy p: Accounts@1\n" + rules
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
	p, cerr := eval.CompilePolicy(&eval.Source{Doc: doc, Info: c.Info(), File: "p.sigil", Src: []byte(src)}, k, b, nil, eval.Options{})
	if cerr != nil {
		t.Fatalf("compile: %v", cerr)
	}
	return p
}

// accountsKind builds the Accounts kind over AccountInput, with Tier and
// Plan registered.
func accountsKind(t testing.TB) (*kind.Kind, *gokind.Binding) {
	t.Helper()
	k, b, errs := gokind.Build(gokind.Options{
		Name: "Accounts", Version: 1, Input: typeOf[AccountInput](), Ranked: true,
		Enums: []gokind.Enum{
			{Type: typeOf[Tier](), Values: []string{"critical", "standard", "internal"}},
			{Type: typeOf[Plan](), Values: []string{"basic", "standard"}},
		},
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: typeOf[None](), Reasons: []string{"no_rule_matched"}},
			{Name: "move", Payload: typeOf[MoveData](), Reasons: []string{"ok"}},
		},
		Default: &gokind.Default{Decision: "deny", Reason: "no_rule_matched"},
		Funcs: []gokind.Func{
			{Name: "next", Fn: func(t Tier) Tier {
				if t == "critical" {
					return "standard"
				}
				return "gold"
			}},
			{Name: "any_of", Fn: func(ts []Tier) bool { return len(ts) > 0 }},
		},
	})
	if errs != nil {
		t.Fatal(errs)
	}
	return k, b
}
