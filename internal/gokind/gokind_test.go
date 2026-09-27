package gokind_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/gokind"
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

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

precedence deny > review > approve
default deny("no_rule_matched")
`

func typeOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// pkgRelease lets a test declare a local Release next to this one.
type pkgRelease = Release

func deploy() gokind.Options {
	return gokind.Options{
		Name:    "DeployApproval",
		Version: 1,
		Input:   typeOf[Input](),
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: typeOf[None]()},
			{Name: "review", Payload: typeOf[ReviewData]()},
			{Name: "approve", Payload: typeOf[ApproveData]()},
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
			I32  int32    `policy:"i32"`
			U    uint     `policy:"u"`
			F32  float32  `policy:"f32"`
			Ch   chan int `policy:"ch"`
			Fn   func()   `policy:"f"`
			Arr  [2]int   `policy:"arr"`
			Any  any      `policy:"iface"`
			PP   **int    `policy:"pp"`
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
			want: "type AllScalars {\n  b: bool\n  i: int\n  i6: int\n  f: float\n  s: string\n  d: duration\n  t: timestamp\n}\ntype Release {\n  soak: duration\n  hotfix: bool\n}\n\n" +
				"input matrix: list<list<string>>\ninput lookup: map<int, list<string>>\ninput by_time: map<timestamp, int>\ninput maybe: ?string\ninput nested: ?AllScalars\ninput deep: map<string, ?Release>\n"},
		{name: "keyword field names", mutate: func(o *gokind.Options) { o.Input = typeOf[HasKeyword]() },
			want: "type Keyword {\n  type: string\n  kind: string\n}\n\ninput resource: Keyword\n"},
		{name: "keyword input name", mutate: func(o *gokind.Options) { o.Input = typeOf[Keyword]() },
			errs: []string{`invalid input name "type"`, `invalid input name "kind"`}, help: "an input name is a plain identifier, not a keyword"},
		{name: "empty input", mutate: func(o *gokind.Options) { o.Input = typeOf[Empty]() }, want: ""},
		{name: "defaults of every shape", mutate: func(o *gokind.Options) {
			o.Decisions = []gokind.Decision{{Name: "d", Payload: typeOf[Defaults]()}}
			o.Default = &gokind.Default{Decision: "d", Reason: "x"}
		}, want: "decision d(reason: string, bake: duration = 1h30m, tags: list<string> = [\"a\", \"b\"], limit: int = -3, tiers: map<string, int> = {\"a\": 1}, ratio: float = 0.75, flag: bool = true, opt: ?string = \"x\")\n\nprecedence d\ndefault d(\"x\")\n"},
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

		// Decisions.
		{name: "payload is not a struct", mutate: func(o *gokind.Options) {
			o.Decisions[1].Payload = typeOf[[]string]()
		}, errs: []string{"decision review: payload type []string is not a struct"}, help: "a decision's payload is a struct with tagged fields, or policy.None"},
		{name: "bad defaults", mutate: func(o *gokind.Options) {
			o.Decisions = []gokind.Decision{{Name: "d", Payload: typeOf[BadDefaults]()}}
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
		}, errs: []string{`default: field "approvers" is required and has no value`}, help: "review is declared as: decision review(reason: string, approvers: list<string>)"},
		{name: "default names an unknown decision", mutate: func(o *gokind.Options) {
			o.Default = &gokind.Default{Decision: "escalate", Reason: "x"}
		}, errs: []string{`default names undeclared decision "escalate"`}},
		{name: "no decisions", mutate: func(o *gokind.Options) {
			o.Decisions = nil
			o.Default = nil
		}, errs: []string{"kind DeployApproval declares no decisions", "kind DeployApproval has neither precedence nor collect all", "kind DeployApproval has no default decision"}},
		{name: "decision declared twice", mutate: func(o *gokind.Options) {
			o.Decisions = append(o.Decisions, gokind.Decision{Name: "deny", Payload: typeOf[None]()})
		}, errs: []string{`decision "deny" is declared twice`, `precedence names "deny" twice`}},

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
