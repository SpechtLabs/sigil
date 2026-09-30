package gokind_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// The README's host types.
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
		Internal    int     // untagged: invisible to policies
	}
	None       struct{}
	ReviewData struct {
		Approvers []string `policy:"approvers"`
	}
	ApproveData struct {
		Bake time.Duration `policy:"bake,default=1h"`
	}
)

const deploySchema = `kind DeployApproval version 1

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
  reason: no_rule_matched
}

decision review {
  reason: service_owner | everyone
  approvers: list<string>
}

decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}

collect one
precedence deny > review > approve

default deny(reason: no_rule_matched)
`

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// pkgRelease lets a test declare a local Release next to this one.
type pkgRelease = Release

func deploy() gokind.Options {
	return gokind.Options{
		Name:    "DeployApproval",
		Version: 1,
		Input:   typeOf[Input](),
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: typeOf[None](), Reasons: []string{"no_rule_matched"}},
			{Name: "review", Payload: typeOf[ReviewData](), Reasons: []string{"service_owner", "everyone"}},
			{Name: "approve", Payload: typeOf[ApproveData](), Reasons: []string{"release_manager", "payments_sre"}},
		},
		Default: &gokind.Default{Decision: "deny", Reason: "no_rule_matched"},
		Funcs:   []gokind.Func{{Name: "split", Fn: strings.Split}},
	}
}

func TestBuildDeploy(t *testing.T) {
	k, b, errs := gokind.Build(deploy())
	if errs != nil {
		t.Fatalf("Build() errors:\n%v", errs)
	}
	if got := k.Source(); got != deploySchema {
		t.Errorf("Source() =\n%s\nwant\n%s", got, deploySchema)
	}
	if b.Input != typeOf[Input]() {
		t.Errorf("Binding.Input = %v", b.Input)
	}
	if b.Structs["Service"] != typeOf[Service]() {
		t.Errorf("Binding.Structs[Service] = %v", b.Structs["Service"])
	}
	if b.Payloads["approve"] != typeOf[ApproveData]() {
		t.Errorf("Binding.Payloads[approve] = %v", b.Payloads["approve"])
	}
	if !b.Funcs["split"].IsValid() {
		t.Error("Binding.Funcs[split] is not set")
	}
	for key, want := range map[string][]int{".service": {1}, "Service.tier": {1}, "decision approve.bake": {0}} {
		if got := b.Fields[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("Binding.Fields[%q] = %v, want %v", key, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	type (
		Empty      struct{}
		AllScalars struct {
			B  bool          `policy:"b"`
			I  int           `policy:"i"`
			I6 int64         `policy:"i6"`
			F  float64       `policy:"f"`
			S  string        `policy:"s"`
			D  time.Duration `policy:"d"`
			T  time.Time     `policy:"t"`
		}
		Composite struct {
			Matrix   [][]string          `policy:"matrix"`
			Lookup   map[int][]string    `policy:"lookup"`
			ByTime   map[time.Time]int   `policy:"by_time"`
			Maybe    *string             `policy:"maybe"`
			Nested   *AllScalars         `policy:"nested"`
			Deep     map[string]*Release `policy:"deep"`
			Skipped  string              `policy:"-"`
			Untagged string
		}
		Node struct {
			Next *Node `policy:"next"`
		}
		Unsupported struct {
			I32  int32           `policy:"i32"`
			U    uint            `policy:"u"`
			F32  float32         `policy:"f32"`
			Ch   chan int        `policy:"ch"`
			Fn   func()          `policy:"f"`
			Arr  [2]int          `policy:"arr"`
			Any  any             `policy:"iface"`
			PP   **int           `policy:"pp"`
			PS   *[]int          `policy:"ps"`
			PM   *map[string]int `policy:"pm"`
			Anon struct {
				X int `policy:"x"`
			} `policy:"anon"`
			Keyed map[Release]int `policy:"keyed"`
		}
		BadTags struct {
			Empty  string `policy:""`
			hidden string `policy:"hidden"` //nolint:unused // the tag is the point
			Release
		}
		EmbeddedTagged struct {
			Release `policy:"release"`
		}
		Keyword struct {
			Type string `policy:"type"`
			Kind string `policy:"kind"`
		}
		HasKeyword struct {
			Resource Keyword `policy:"resource"`
		}
		BadName struct {
			X string `policy:"when"`
		}
		Defaults struct {
			Bake  time.Duration  `policy:"bake,default=1h30m"`
			Tags  []string       `policy:"tags,default=[\"a\", \"b\"]"`
			Limit int            `policy:"limit,default=-3"`
			Tiers map[string]int `policy:"tiers,default={\"a\": 1}"`
			Ratio float64        `policy:"ratio,default=0.5 + 0.25"`
			Flag  bool           `policy:"flag,default=true"`
			Opt   *string        `policy:"opt,default=\"x\""`
		}
		BadDefaults struct {
			Bake time.Duration `policy:"bake,default=1x"`
			N    int           `policy:"n,default=\"one\""`
			S    string        `policy:"s,default=name"`
			O    string        `policy:"s2,optional"`
		}
		OptionFields struct {
			Soak   time.Duration `policy:"soak,default=1h"`
			Hotfix bool          `policy:"hotfix"`
		}
		InputOptions struct {
			Environment string       `policy:"environment,default=\"prod\""`
			Tier        string       `policy:"tier,omitempty"`
			Release     OptionFields `policy:"release"`
		}
		PayloadOptions struct {
			Release OptionFields `policy:"release"`
		}
	)

	tests := []struct {
		name   string
		mutate func(o *gokind.Options)
		want   string   // schema, when valid
		errs   []string // messages, when not
		help   string   // of the first error, when it's the point
	}{
		{name: "all scalars", mutate: func(o *gokind.Options) { o.Input = typeOf[AllScalars]() },
			want: "input b: bool\ninput i: int\ninput i6: int\ninput f: float\ninput s: string\ninput d: duration\ninput t: timestamp\n"},
		{name: "composite types", mutate: func(o *gokind.Options) { o.Input = typeOf[Composite]() },
			want: "type AllScalars {\n  b: bool\n  i: int\n  i6: int\n  f: float\n  s: string\n  d: duration\n  t: timestamp\n}\n\ntype Release {\n  soak: duration\n  hotfix: bool\n}\n\n" +
				"input matrix: list<list<string>>\ninput lookup: map<int, list<string>>\ninput by_time: map<timestamp, int>\ninput maybe: ?string\ninput nested: ?AllScalars\ninput deep: map<string, ?Release>\n"},
		{name: "keyword field names", mutate: func(o *gokind.Options) { o.Input = typeOf[HasKeyword]() },
			want: "type Keyword {\n  type: string\n  kind: string\n}\n\ninput resource: Keyword\n"},
		{name: "keyword input name", mutate: func(o *gokind.Options) { o.Input = typeOf[Keyword]() },
			errs: []string{`invalid input name "type"`, `invalid input name "kind"`}, help: "an input name is a plain identifier, not a keyword"},
		{name: "empty input", mutate: func(o *gokind.Options) { o.Input = typeOf[Empty]() }, want: ""},
		{name: "defaults of every shape", mutate: func(o *gokind.Options) {
			o.Decisions = []gokind.Decision{{Name: "d", Payload: typeOf[Defaults](), Reasons: []string{"x"}}}
			o.Default = &gokind.Default{Decision: "d", Reason: "x"}
		}, want: "decision d {\n  reason: x\n  bake: duration = 1h30m\n  tags: list<string> = [\"a\", \"b\"]\n  limit: int = -3\n  tiers: map<string, int> = {\"a\": 1}\n  ratio: float = 0.75\n  flag: bool = true\n  opt: ?string = \"x\"\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\n"},
		{name: "collecting kind", mutate: func(o *gokind.Options) {
			o.Collect = true
			o.Default = nil
		}, want: "precedence-less"},
		{name: "function returning an error too", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "parse", Fn: time.ParseDuration}}
		}, want: "fn parse(string) -> duration\n"},
		{name: "function taking a struct", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "owner", Fn: func(s Service) string { return s.Name }}}
		}, want: "fn owner(Service) -> string\n"},
		{name: "function without parameters", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "now", Fn: time.Now}}
		}, want: "fn now() -> timestamp\n"},

		// Input shape.
		{name: "input is not a struct", mutate: func(o *gokind.Options) { o.Input = typeOf[string]() },
			errs: []string{"input type string is not a struct"}, help: "NewKind's type parameter is the input struct; its tagged fields become the inputs"},
		{name: "input is a pointer", mutate: func(o *gokind.Options) { o.Input = typeOf[*Input]() },
			errs: []string{"input type *gokind_test.Input is not a struct"}},
		{name: "recursive type", mutate: func(o *gokind.Options) { o.Input = typeOf[Node]() },
			errs: []string{"type Node is recursive: Node -> Node"}},
		{name: "same type name from two Go types", mutate: func(o *gokind.Options) {
			type Release struct {
				V int `policy:"v"`
			}
			o.Input = typeOf[struct {
				A pkgRelease `policy:"a"`
				B Release    `policy:"b"`
			}]()
		}, errs: []string{"b: type gokind_test.Release and type gokind_test.Release both export as `Release`"}, help: "two Go types would export as one Sigil type; rename one"},

		// Unsupported field types, each reported.
		{name: "unsupported types", mutate: func(o *gokind.Options) { o.Input = typeOf[Unsupported]() }, errs: []string{
			"i32: unsupported type int32",
			"u: unsupported type uint",
			"f32: unsupported type float32",
			"ch: unsupported type chan int",
			"f: unsupported type func()",
			"arr: unsupported type [2]int",
			"iface: unsupported type interface {}",
			"pp: **int is a pointer to a pointer",
			"ps: *[]int is a pointer to a slice",
			"pm: *map[string]int is a pointer to a map",
			"anon: anonymous struct types can't be exported",
			`input "keyed": map key type can't be Release`,
		}, help: "policies read bool, int, int64, float64, string, time.Duration, time.Time, slices, maps, pointers and tagged structs"},

		// Tags.
		{name: "bad tags", mutate: func(o *gokind.Options) { o.Input = typeOf[BadTags]() }, errs: []string{
			"input: field Empty has an empty policy tag",
			"input: field hidden is unexported but tagged \"hidden\"",
		}, help: "write the name policies use, like `policy:\"tier\"`"},
		{name: "embedded tagged struct", mutate: func(o *gokind.Options) { o.Input = typeOf[EmbeddedTagged]() },
			errs: []string{"input: embedded field Release can't be tagged"}, help: "give the embedded struct a field name, or tag its fields directly"},
		{name: "input named like a keyword", mutate: func(o *gokind.Options) { o.Input = typeOf[BadName]() },
			errs: []string{`invalid input name "when"`}},
		{name: "options on inputs and type fields", mutate: func(o *gokind.Options) { o.Input = typeOf[InputOptions]() }, errs: []string{
			`input: field Environment has tag option "default=\"prod\"", which only a decision payload field takes`,
			`input: field Tier has tag option "omitempty", which only a decision payload field takes`,
			`type OptionFields: field Soak has tag option "default=1h", which only a decision payload field takes`,
		}, help: "only decision payload fields take a tag option, `default=<constant>`; drop it from this tag"},
		{name: "options on a payload's struct type", mutate: func(o *gokind.Options) {
			o.Decisions = append(o.Decisions, gokind.Decision{Name: "hold", Payload: typeOf[PayloadOptions](), Reasons: []string{"x"}})
		}, errs: []string{`type OptionFields: field Soak has tag option "default=1h", which only a decision payload field takes`}},

		// Decisions.
		{name: "payload is not a struct", mutate: func(o *gokind.Options) {
			o.Decisions[1].Payload = typeOf[[]string]()
		}, errs: []string{"decision review: payload type []string is not a struct"}, help: "a decision's payload is a struct with tagged fields, or policy.None"},
		{name: "bad defaults", mutate: func(o *gokind.Options) {
			o.Decisions = []gokind.Decision{{Name: "d", Payload: typeOf[BadDefaults](), Reasons: []string{"x"}}}
			o.Default = &gokind.Default{Decision: "d", Reason: "x"}
		}, errs: []string{
			"decision d: field bake: default \"1x\": unknown duration unit `x` in `1x`",
			"decision d: field n: default \"\\\"one\\\"\": expected int, found string",
			"decision d: field s: default \"name\": `name` isn't a constant",
			"decision d: field s2 has unknown tag option \"optional\"",
			`default: field "s2" is required and has no value`,
		}, help: "write the default as a Sigil literal, like `default=1h` or `default=[\"a\"]`"},
		{name: "default needs every payload field", mutate: func(o *gokind.Options) {
			o.Default = &gokind.Default{Decision: "review", Reason: "everyone"}
		}, errs: []string{`default: field "approvers" is required and has no value`}},
		{name: "default names an unknown decision", mutate: func(o *gokind.Options) {
			o.Default = &gokind.Default{Decision: "escalate", Reason: "x"}
		}, errs: []string{`default names undeclared decision "escalate"`}},
		{name: "conflict outcome", mutate: func(o *gokind.Options) {
			o.Conflict = &gokind.Default{Decision: "approve", Reason: "payments_sre"}
		}, want: "default deny(reason: no_rule_matched)\nconflict approve(reason: payments_sre)\n"},
		{name: "conflict outcome needs every payload field", mutate: func(o *gokind.Options) {
			o.Conflict = &gokind.Default{Decision: "review", Reason: "everyone"}
		}, errs: []string{`conflict: field "approvers" is required and has no value`}, help: "review takes reason: service_owner | everyone, and approvers: list<string>"},
		{name: "conflict outcome with an undeclared reason", mutate: func(o *gokind.Options) {
			o.Conflict = &gokind.Default{Decision: "deny", Reason: "conflicting_rules"}
		}, errs: []string{`conflict: decision deny has no reason "conflicting_rules"`}},
		{name: "conflict outcome on a collecting kind", mutate: func(o *gokind.Options) {
			o.Collect = true
			o.Conflict = &gokind.Default{Decision: "deny", Reason: "no_rule_matched"}
		}, errs: []string{"kind DeployApproval collects all decisions and can't declare a conflict outcome"}},
		{name: "no decisions", mutate: func(o *gokind.Options) {
			o.Decisions = nil
			o.Default = nil
		}, errs: []string{"kind DeployApproval declares no decisions", "kind DeployApproval doesn't declare how many decisions it returns", "kind DeployApproval has no default decision"}},
		{name: "decision declared twice", mutate: func(o *gokind.Options) {
			o.Decisions = append(o.Decisions, gokind.Decision{Name: "deny", Payload: typeOf[None](), Reasons: []string{"no_rule_matched"}})
		}, errs: []string{`decision "deny" is declared twice`, `precedence names "deny" twice`}},

		// Reason rankings.
		{name: "a ranking", mutate: func(o *gokind.Options) {
			o.Rankings = []gokind.Ranking{{Decision: "approve", Reasons: []string{"payments_sre", "release_manager"}}}
		}, want: "precedence approve: payments_sre > release_manager\n"},
		{name: "a ranking without reasons", mutate: func(o *gokind.Options) {
			o.Rankings = []gokind.Ranking{{}}
		}, errs: []string{"WithReasonPrecedence names no reasons"},
			help: "pass one decision's reasons, highest first, like `WithReasonPrecedence(Approve.Reason(\"release_manager\"), Approve.Reason(\"payments_sre\"))`"},
		{name: "a ranking with another decision's reasons", mutate: func(o *gokind.Options) {
			o.Rankings = []gokind.Ranking{{Decision: "approve", Reasons: []string{"release_manager", "payments_sre"},
				Mixed: []kind.Outcome{{Decision: "deny", Reason: "no_rule_matched"}, {Decision: "review", Reason: "everyone"}}}}
		}, errs: []string{"precedence approve: reason no_rule_matched belongs to decision deny", "precedence approve: reason everyone belongs to decision review"},
			help: "a ranking orders the reasons of one decision; rank each decision's reasons in a WithReasonPrecedence of its own"},
		{name: "a ranking of an undeclared decision", mutate: func(o *gokind.Options) {
			o.Rankings = []gokind.Ranking{{Decision: "hold", Reasons: []string{"freeze"}}}
		}, errs: []string{`precedence: undeclared decision "hold"`}, help: "WithReasonPrecedence ranks the reasons of a declared decision"},
		{name: "a decision ranked twice", mutate: func(o *gokind.Options) {
			r := gokind.Ranking{Decision: "approve", Reasons: []string{"release_manager", "payments_sre"}}
			o.Rankings = []gokind.Ranking{r, r}
		}, errs: []string{"precedence approve is declared twice"}, help: "rank a decision's reasons once"},

		// Functions.
		{name: "not a function", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "split", Fn: "strings.Split"}}
		}, errs: []string{"function split: strings.Split is not a function"}, help: "pass a Go function, like `policy.WithFunc(\"split\", strings.Split)`"},
		{name: "nil function", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "split", Fn: nil}}
		}, errs: []string{"function split: <nil> is not a function"}},
		{name: "variadic function", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "join", Fn: func(xs ...string) string { return "" }}}
		}, errs: []string{"function join: variadic functions aren't supported"}, help: "policies pass a fixed number of arguments; wrap the function"},
		{name: "function without results", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "f", Fn: func(s string) {}}}
		}, errs: []string{"function f: unsupported results func(string)"}, help: "a host function returns a value, or a value and an error"},
		{name: "function returning only an error", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "f", Fn: func(s string) error { return errors.New("x") }}}
		}, errs: []string{"function f: unsupported results func(string) error"}},
		{name: "function returning three values", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "f", Fn: func() (int, int, error) { return 0, 0, nil }}}
		}, errs: []string{"function f: unsupported results func() (int, int, error)"}},
		{name: "function with unsupported parameter", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "f", Fn: func(n int32) int { return 0 }}}
		}, errs: []string{"function f, parameter 1: unsupported type int32"}},
		{name: "function returning an optional", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "f", Fn: func() *string { return nil }}}
		}, errs: []string{"function f: the result can't be optional"}},
		{name: "function collides with input", mutate: func(o *gokind.Options) {
			o.Funcs = []gokind.Func{{Name: "actor", Fn: strings.ToUpper}}
		}, errs: []string{`function "actor" collides with input "actor"`}},

		// Header.
		{name: "version zero", mutate: func(o *gokind.Options) { o.Version = 0 }, errs: []string{"invalid kind version 0"}},
		{name: "keyword name", mutate: func(o *gokind.Options) { o.Name = "kind" }, errs: []string{`invalid kind name "kind"`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := deploy()
			tt.mutate(&o)
			k, _, errs := gokind.Build(o)

			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Msg
			}
			if g, w := strings.Join(got, "\n"), strings.Join(tt.errs, "\n"); g != w {
				t.Errorf("errors:\n%s\nwant:\n%s", g, w)
			}
			if tt.help != "" && (len(errs) == 0 || errs[0].Help != tt.help) {
				var h string
				if len(errs) > 0 {
					h = errs[0].Help
				}
				t.Errorf("help = %q\nwant   %q", h, tt.help)
			}
			if tt.want == "" {
				return
			}
			if k == nil {
				t.Fatal("Build() returned no kind")
			}
			src := k.Source()
			if tt.want == "precedence-less" {
				if !strings.HasSuffix(src, "\ncollect all\n") || strings.Contains(src, "precedence") {
					t.Errorf("Source() =\n%s\nwant a collecting kind", src)
				}
				return
			}
			if !strings.Contains(src, tt.want) {
				t.Errorf("Source() =\n%s\nwant it to contain\n%s", src, tt.want)
			}
		})
	}
}

// Enum types for TestBuildEnums.
type (
	Tier    string
	Plan    string
	Unused  string
	Region  string // never registered: a plain string
	string_ = string

	EnumService struct {
		Tier   Tier         `policy:"tier"`
		Tiers  []Tier       `policy:"tiers"`
		Limits map[Tier]int `policy:"limits"`
		Backup *Tier        `policy:"backup"`
		Region Region       `policy:"region"`
	}
	EnumInput struct {
		Service EnumService `policy:"service"`
	}
	PlanData struct {
		Plan Plan `policy:"plan,default=basic"`
	}
	TierValues struct {
		ByName map[string]Tier `policy:"by_name"`
	}
)

func TestBuildEnums(t *testing.T) {
	enums := func(o *gokind.Options) {
		o.Input = typeOf[EnumInput]()
		o.Decisions = []gokind.Decision{
			{Name: "deny", Payload: typeOf[None](), Reasons: []string{"no_rule_matched"}},
			{Name: "upgrade", Payload: typeOf[PlanData](), Reasons: []string{"asked"}},
		}
		o.Default = &gokind.Default{Decision: "deny", Reason: "no_rule_matched"}
		o.Funcs = []gokind.Func{{Name: "plan_of", Fn: func(Tier) Plan { return "" }}}
		o.Enums = []gokind.Enum{
			{Type: typeOf[Unused](), Values: []string{"nothing"}},
			{Type: typeOf[Plan](), Values: []string{"basic", "pro"}},
			{Type: typeOf[Tier](), Values: []string{"critical", "standard", "internal"}},
		}
	}

	tests := []struct {
		name   string
		mutate func(o *gokind.Options)
		want   string   // schema, when valid
		errs   []string // messages, when not
		help   string   // of the first error, when it's the point
	}{
		{name: "first reached first, unreached last", mutate: enums,
			want: "enum Tier: critical | standard | internal\nenum Plan: basic | pro\nenum Unused: nothing\n\ntype EnumService {\n" +
				"  tier: Tier\n  tiers: list<Tier>\n  limits: map<Tier, int>\n  backup: ?Tier\n  region: string\n}\n\n" +
				"input service: EnumService\n\nfn plan_of(Tier) -> Plan\n\ndecision deny {\n  reason: no_rule_matched\n}\n\n" +
				"decision upgrade {\n  reason: asked\n  plan: Plan = basic\n}\n"},
		{name: "a payload reaches an enum", mutate: func(o *gokind.Options) {
			enums(o)
			o.Input = typeOf[struct{}]()
			o.Funcs = nil
		}, want: "enum Plan: basic | pro\nenum Unused: nothing\nenum Tier: critical | standard | internal\n\ndecision deny"},
		{name: "unregistered named string", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums = []gokind.Enum{{Type: typeOf[Plan](), Values: []string{"basic", "pro"}}}
			o.Funcs = nil
		}, want: "  tier: string\n  tiers: list<string>\n  limits: map<string, int>\n  backup: ?string\n"},
		{name: "not a named type", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums = append(o.Enums, gokind.Enum{Type: typeOf[string_](), Values: []string{"a"}}, gokind.Enum{Values: []string{"b"}})
		}, errs: []string{"WithEnum: string is not a named type", "WithEnum: <nil> is not a named type"},
			help: "declare a named type, like `type Tier string`; its name becomes the enum's name in policies"},
		{name: "registered twice", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums = append(o.Enums, gokind.Enum{Type: typeOf[Tier](), Values: []string{"gold"}})
		}, errs: []string{"enum Tier is registered twice"}, help: "pass each enum type to WithEnum once, with all its values"},
		{name: "an enum as a map value", mutate: func(o *gokind.Options) {
			enums(o)
			o.Input = typeOf[TierValues]()
		}, errs: []string{`input "by_name": map value type can't be enum Tier`}},
		{name: "no values", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums[0].Values = nil
		}, errs: []string{"enum Unused declares no values"}},
		{name: "a duplicate value", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums[1].Values = []string{"basic", "pro", "basic"}
		}, errs: []string{`enum Plan: value "basic" is declared twice`}},
		{name: "a value that isn't an identifier", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums[1].Values = []string{"basic", "pro-plus", "when"}
		}, errs: []string{`enum Plan: invalid value "pro-plus"`, `enum Plan: invalid value "when"`}},
		{name: "an enum named like a struct type", mutate: func(o *gokind.Options) {
			type Release string
			o.Enums = []gokind.Enum{{Type: typeOf[Release](), Values: []string{"x"}}}
		}, errs: []string{`type "Release" is declared twice`}},
		{name: "a value named like a decision", mutate: func(o *gokind.Options) {
			enums(o)
			o.Enums[0].Values = []string{"deny", "service", "plan_of"}
		}, errs: []string{`enum Unused: value "deny" collides with decision "deny"`, `enum Unused: value "service" collides with input "service"`, `enum Unused: value "plan_of" collides with function "plan_of"`}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := deploy()
			tt.mutate(&o)
			k, b, errs := gokind.Build(o)
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Msg
			}
			if g, w := strings.Join(got, "\n"), strings.Join(tt.errs, "\n"); g != w {
				t.Errorf("errors:\n%s\nwant:\n%s", g, w)
			}
			if tt.help != "" && (len(errs) == 0 || errs[0].Help != tt.help) {
				t.Errorf("help = %v\nwant   %q", errs, tt.help)
			}
			if tt.want == "" {
				return
			}
			if k == nil {
				t.Fatal("Build() returned no kind")
			}
			if src := k.Source(); !strings.Contains(src, tt.want) {
				t.Errorf("Source() =\n%s\nwant it to contain\n%s", src, tt.want)
			}
			for _, e := range o.Enums {
				if got, ok := b.TypeOf(e.Type); !ok || got != k.Enum(e.Type.Name()) {
					t.Errorf("TypeOf(%v) = %v, %v, want the kind's enum", e.Type, got, ok)
				}
			}
		})
	}
}

func TestHasEnums(t *testing.T) {
	type Tier string
	type withEnum struct {
		Tier Tier `policy:"tier"`
	}
	type withoutEnum struct {
		Name string `policy:"name"`
	}
	for _, tt := range []struct {
		name string
		o    gokind.Options
		want bool
	}{
		{name: "an enum", o: gokind.Options{Input: reflect.TypeFor[withEnum](), Enums: []gokind.Enum{{Type: reflect.TypeFor[Tier](), Values: []string{"a", "b"}}}}, want: true},
		{name: "no enum", o: gokind.Options{Input: reflect.TypeFor[withoutEnum]()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.o.Name, tt.o.Version, tt.o.Collect = "K", 1, true
			tt.o.Decisions = []gokind.Decision{{Name: "ok", Payload: reflect.TypeFor[struct{}](), Reasons: []string{"yes"}}}
			k, b, errs := gokind.Build(tt.o)
			if errs != nil {
				t.Fatal(errs)
			}
			if b.HasEnums != tt.want {
				t.Errorf("Build: HasEnums = %v, want %v", b.HasEnums, tt.want)
			}
			// The stock CLI's binding, synthesized from the kind file, says
			// the same, so it checks enum values too.
			if s := gokind.Synthesize(k); s.HasEnums != tt.want {
				t.Errorf("Synthesize: HasEnums = %v, want %v", s.HasEnums, tt.want)
			}
		})
	}
}
