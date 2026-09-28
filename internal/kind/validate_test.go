package kind_test

import (
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(k *kind.Kind)
		base   func() *kind.Kind
		want   []string // messages; empty means valid
		help   string   // of the first message, when it's the point
	}{
		{name: "deploy approval is valid", base: deploy},
		{name: "access grant is valid", base: access},
		{name: "collecting kind with a default is valid", base: access, mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "read", Reason: "everyone"}
		}},

		// Header.
		{name: "kind name is a keyword", mutate: func(k *kind.Kind) { k.Name = "kind" }, want: []string{`invalid kind name "kind"`}},
		{name: "kind name has a dash", mutate: func(k *kind.Kind) { k.Name = "deploy-approval" }, want: []string{`invalid kind name "deploy-approval"`}},
		{name: "version zero", mutate: func(k *kind.Kind) { k.Version = 0 }, want: []string{"invalid kind version 0"}},

		// Types.
		{name: "type name is a keyword", mutate: func(k *kind.Kind) {
			k.Types[0].Name = "type"
			k.Inputs[0].Type = k.Types[0]
		}, want: []string{`invalid type name "type"`}},
		{name: "type shadows a built-in", mutate: func(k *kind.Kind) {
			k.Types[0].Name = "string"
			k.Inputs[0].Type = k.Types[0]
		}, want: []string{`type "string" shadows a built-in type`}, help: "the built-in type names are reserved; pick another name"},
		{name: "type shadows list", mutate: func(k *kind.Kind) {
			k.Types[0].Name = "list"
			k.Inputs[0].Type = k.Types[0]
		}, want: []string{`type "list" shadows a built-in type`}},
		{name: "type declared twice", mutate: func(k *kind.Kind) {
			k.Types = append(k.Types, &types.Struct{Name: "Actor"})
		}, want: []string{`type "Actor" is declared twice`}},
		{name: "field declared twice", mutate: func(k *kind.Kind) {
			k.Types[0].Fields = append(k.Types[0].Fields, &types.Field{Name: "soak", Type: types.Int})
		}, want: []string{`type Release: field "soak" is declared twice`}},
		{name: "field named like a keyword is fine", mutate: func(k *kind.Kind) {
			k.Types[0].Fields = append(k.Types[0].Fields, &types.Field{Name: "kind", Type: types.String})
		}},
		{name: "field name with a dash", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Name = "soak-time"
		}, want: []string{`type Release: invalid field name "soak-time"`}},
		{name: "field of undeclared type", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Struct{Name: "Ticket"}
		}, want: []string{`type Release, field "soak": undeclared type Ticket`}, help: "declare it with `type Ticket { ... }`"},
		{name: "field of nested optional", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Optional{Elem: &types.Optional{Elem: types.String}}
		}, want: []string{`type Release, field "soak": optional types don't nest`}},
		{name: "field of optional list", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Optional{Elem: strList}
		}, want: []string{`type Release, field "soak": a list can't be optional`}, help: "use `list<T>`; an absent list already reads as an empty one"},
		{name: "field of optional map", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Optional{Elem: &types.Map{Key: types.String, Value: types.Int}}
		}, want: []string{`type Release, field "soak": a map can't be optional`}, help: "use `map<K, V>`; an absent map already reads as an empty one"},
		{name: "field of decision type", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.List{Elem: types.Decision}
		}, want: []string{`type Release, field "soak": type can't be decision`}},
		{name: "field with int map key is fine", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Map{Key: types.Int, Value: types.String}
		}},
		{name: "field with timestamp map key is fine", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Map{Key: types.Timestamp, Value: types.String}
		}},
		{name: "field with list map key", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Map{Key: strList, Value: types.String}
		}, want: []string{`type Release, field "soak": map key type can't be list<string>`}, help: "map keys are scalars, as in Go: bool, int, float, string, duration or timestamp"},
		{name: "field with struct map key", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Map{Key: k.Types[1], Value: types.String}
		}, want: []string{`type Release, field "soak": map key type can't be Service`}},
		{name: "field with optional map key", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Map{Key: &types.Optional{Elem: types.String}, Value: types.String}
		}, want: []string{`type Release, field "soak": map key type can't be ?string`}},
		{name: "field with decision map key", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = &types.Map{Key: types.Decision, Value: types.String}
		}, want: []string{`type Release, field "soak": map key type can't be decision`}},
		{name: "field of invalid type was reported by the loader", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = types.Invalid
		}},
		{name: "field of nil type", mutate: func(k *kind.Kind) {
			k.Types[0].Fields[0].Type = nil
		}, want: []string{`type Release, field "soak": invalid type`}},
		{name: "directly recursive type", mutate: func(k *kind.Kind) {
			k.Types[0].Fields = append(k.Types[0].Fields, &types.Field{Name: "previous", Type: &types.Optional{Elem: k.Types[0]}})
		}, want: []string{"type Release is recursive: Release -> Release"}},
		{name: "mutually recursive types", mutate: func(k *kind.Kind) {
			k.Types[1].Fields = append(k.Types[1].Fields, &types.Field{Name: "owner", Type: k.Types[2]})
			k.Types[2].Fields = append(k.Types[2].Fields, &types.Field{Name: "services", Type: &types.List{Elem: k.Types[1]}})
		}, want: []string{
			"type Service is recursive: Service -> Actor -> Service",
			"type Actor is recursive: Actor -> Service -> Actor",
		}},
		{name: "nested struct without a cycle is fine", mutate: func(k *kind.Kind) {
			k.Types[1].Fields = append(k.Types[1].Fields, &types.Field{Name: "owner", Type: k.Types[2]})
			k.Types[0].Fields = append(k.Types[0].Fields, &types.Field{Name: "service", Type: k.Types[1]})
		}},

		// Inputs and functions.
		{name: "input name is a keyword", mutate: func(k *kind.Kind) { k.Inputs[3].Name = "input" }, want: []string{`invalid input name "input"`}},
		{name: "input declared twice", mutate: func(k *kind.Kind) {
			k.Inputs = append(k.Inputs, &kind.Input{Name: "actor", Type: types.String})
		}, want: []string{`input "actor" collides with input "actor"`}},
		{name: "function collides with input", mutate: func(k *kind.Kind) { k.Funcs[0].Name = "environment" },
			want: []string{`function "environment" collides with input "environment"`}, help: "inputs and host functions share one namespace"},
		{name: "input of undeclared type", mutate: func(k *kind.Kind) {
			k.Inputs[0].Type = &types.Struct{Name: "Deploy"}
		}, want: []string{`input "release": undeclared type Deploy`}},
		{name: "function name is a keyword", mutate: func(k *kind.Kind) { k.Funcs[0].Name = "fn" }, want: []string{`invalid function name "fn"`}},
		{name: "function parameter of undeclared type", mutate: func(k *kind.Kind) {
			k.Funcs[0].Params[1] = &types.Struct{Name: "Text"}
		}, want: []string{"function split, parameter 2: undeclared type Text"}},
		{name: "function parameter of decision type", mutate: func(k *kind.Kind) {
			k.Funcs[0].Params[0] = &types.List{Elem: types.Decision}
		}, want: []string{"function split, parameter 1: type can't be decision"}},
		{name: "function returns optional", mutate: func(k *kind.Kind) { k.Funcs[0].Result = &types.Optional{Elem: types.String} },
			want: []string{"function split: the result can't be optional"}},
		{name: "function returns undeclared type", mutate: func(k *kind.Kind) { k.Funcs[0].Result = &types.Struct{Name: "Parts"} },
			want: []string{"function split, result: undeclared type Parts"}},

		// Decisions.
		{name: "no decisions", mutate: func(k *kind.Kind) {
			k.Decisions = nil
			k.Precedence = nil
			k.Default = nil
		}, want: []string{"kind DeployApproval declares no decisions", "kind DeployApproval collects one decision but has no precedence", "kind DeployApproval has no default decision"}},
		{name: "decision name is a keyword", mutate: func(k *kind.Kind) {
			k.Decisions[0].Name = "default"
			k.Precedence[0] = "default"
			k.Default.Decision = "default"
		}, want: []string{`invalid decision name "default"`}},
		{name: "decision declared twice", mutate: func(k *kind.Kind) {
			k.Decisions = append(k.Decisions, &kind.Decision{Name: "deny"})
		}, want: []string{`decision "deny" is declared twice`}},
		{name: "reason as a payload field", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields = append(k.Decisions[1].Fields, &kind.Field{Name: "reason", Type: types.String})
		}, want: []string{`decision review, field "reason": reason is implied and can't be declared as a payload field`}},
		{name: "payload field declared twice", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields = append(k.Decisions[1].Fields, &kind.Field{Name: "approvers", Type: strList})
		}, want: []string{`decision review, field "approvers": declared twice`}},
		{name: "payload field named like a keyword is fine", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields = append(k.Decisions[1].Fields, &kind.Field{Name: "kind", Type: types.String})
		}},
		{name: "payload field with a dot", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields[0].Name = "app.approvers"
		}, want: []string{`decision review, field "app.approvers": invalid field name`}},
		{name: "payload default of the wrong type", mutate: func(k *kind.Kind) {
			k.Decisions[2].Fields[0].Default = int64(1)
		}, want: []string{`decision approve, field "bake": default 1 is not a duration`}, help: "a default is a constant of the field's type"},
		{name: "payload list default", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields[0].Default = []any{"a", "b"}
			k.Decisions[1].Fields[0].HasDefault = true
		}},
		{name: "payload list default with a wrong element", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields[0].Default = []any{"a", int64(1)}
			k.Decisions[1].Fields[0].HasDefault = true
		}, want: []string{`decision review, field "approvers": default ["a", 1] is not a list<string>`}},

		// Resolution.
		{name: "both precedence and collect", mutate: func(k *kind.Kind) { k.Collect = kind.CollectAll },
			want: []string{"kind DeployApproval has both precedence and collect all"}},
		{name: "no collect", mutate: func(k *kind.Kind) { k.Collect = kind.CollectUnset; k.Precedence = nil },
			want: []string{"kind DeployApproval doesn't declare how many decisions it returns"}, help: "declare `collect one` with a `precedence`, or `collect all`"},
		{name: "precedence without collect", mutate: func(k *kind.Kind) { k.Collect = kind.CollectUnset },
			want: []string{"kind DeployApproval has precedence but no collect"}},
		{name: "collect one without precedence", mutate: func(k *kind.Kind) { k.Precedence = nil },
			want: []string{"kind DeployApproval collects one decision but has no precedence"}},
		{name: "precedence misses a decision", mutate: func(k *kind.Kind) { k.Precedence = []string{"deny", "review"} },
			want: []string{`precedence doesn't name decision "approve"`}, help: "list every decision exactly once, highest first"},
		{name: "precedence names a decision twice", mutate: func(k *kind.Kind) { k.Precedence = []string{"deny", "review", "approve", "deny"} },
			want: []string{`precedence names "deny" twice`}},
		{name: "precedence names an unknown decision", mutate: func(k *kind.Kind) { k.Precedence = []string{"deny", "review", "approve", "escalate"} },
			want: []string{`precedence names undeclared decision "escalate"`}},
		{name: "no default with precedence", mutate: func(k *kind.Kind) { k.Default = nil },
			want: []string{"kind DeployApproval has no default decision"}},
		{name: "default names an unknown decision", mutate: func(k *kind.Kind) { k.Default.Decision = "escalate" },
			want: []string{`default names undeclared decision "escalate"`}},
		{name: "default with an empty reason", mutate: func(k *kind.Kind) { k.Default.Reason = "" },
			want: []string{"default deny has an empty reason"}},
		{name: "default with an unknown field", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bak": 15 * time.Minute}}
		}, want: []string{`default: decision approve has no payload field "bak"`}, help: "approve is declared as: decision approve(reason: string, bake: duration = 1h)"},
		{name: "default with a wrong value", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bake": "15m"}}
		}, want: []string{`default: field "bake" value "15m" is not a duration`}},
		{name: "default misses a required field", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "review", Reason: "everyone"}
		}, want: []string{`default: field "approvers" is required and has no value`}, help: "review is declared as: decision review(reason: string, approvers: list<string>)"},
		{name: "default with every field is fine", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "review", Reason: "everyone", Args: map[string]any{"approvers": []any{"leads"}}}
		}},
		{name: "default relying on a field default is fine", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open"}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := tt.base
			if base == nil {
				base = deploy
			}
			k := base()
			if tt.mutate != nil {
				tt.mutate(k)
			}
			errs := k.Validate(nil)
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Msg
			}
			if g, w := strings.Join(got, "\n"), strings.Join(tt.want, "\n"); g != w {
				t.Errorf("Validate() =\n%s\nwant\n%s", g, w)
			}
			if tt.help != "" && (len(errs) == 0 || errs[0].Help != tt.help) {
				var h string
				if len(errs) > 0 {
					h = errs[0].Help
				}
				t.Errorf("help = %q\nwant   %q", h, tt.help)
			}
		})
	}
}
