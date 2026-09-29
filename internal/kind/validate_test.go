package kind_test

import (
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
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
			k.Default = &kind.Default{Decision: "read", Reason: "member"}
		}},

		// Header.
		{name: "kind name is a keyword", mutate: func(k *kind.Kind) { k.Name = "kind" }, want: []string{`invalid kind name "kind"`}},
		{name: "kind name has a dash", mutate: func(k *kind.Kind) { k.Name = "deploy-approval" }, want: []string{`invalid kind name "deploy-approval"`}},
		{name: "version zero", mutate: func(k *kind.Kind) { k.Version = 0 }, want: []string{"invalid kind version 0"}},
		{name: "accepts the current version", mutate: func(k *kind.Kind) { k.Version, k.Accepts = 3, 3 }},
		{name: "accepts zero", mutate: func(k *kind.Kind) { k.Accepts = 0 }, want: []string{"kind DeployApproval accepts version 0, but versions start at 1"},
			help: "leave `accepts` out to accept every version, or name the oldest version policies may still pin, between 1 and the version"},
		{name: "accepts a negative version", mutate: func(k *kind.Kind) { k.Accepts = -1 }, want: []string{"kind DeployApproval accepts version -1, but versions start at 1"}},
		{name: "accepts above the version", mutate: func(k *kind.Kind) { k.Accepts = 2 }, want: []string{"kind DeployApproval at version 1 can't accept version 2"},
			help: "`accepts` names the oldest version policies may still pin, between 1 and the version"},

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
		}, want: []string{`type Release, field "soak": map key type can't be list<string>`}, help: "map keys are scalars or enums, as in Go: bool, int, float, string, duration, timestamp or an enum"},
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

		// Enums.
		{name: "enum as map key, list element and optional is fine", mutate: func(k *kind.Kind) {
			tier := k.Enums[0]
			k.Types[0].Fields = append(k.Types[0].Fields,
				&types.Field{Name: "quota", Type: &types.Map{Key: tier, Value: &types.List{Elem: tier}}},
				&types.Field{Name: "target", Type: &types.Optional{Elem: tier}},
			)
			k.Funcs = append(k.Funcs, &kind.Func{Name: "tier_of", Params: []types.Type{tier}, Result: tier})
		}},
		{name: "two enums share a value", mutate: func(k *kind.Kind) {
			k.Enums = append(k.Enums, &types.Enum{Name: "Plan", Values: []string{"free", "standard"}})
		}},
		{name: "enum value named like a reason is fine", mutate: func(k *kind.Kind) {
			k.Enums[0].Values = append(k.Enums[0].Values, "open", "everyone")
		}},
		{name: "enum name with a dash", mutate: func(k *kind.Kind) { k.Enums[0].Name = "service-tier"; k.Types[1].Fields[1].Type = k.Enums[0] },
			want: []string{`invalid enum name "service-tier"`}, help: "an enum name is an identifier, like `Tier`"},
		{name: "enum name is a keyword", mutate: func(k *kind.Kind) { k.Enums[0].Name = "enum"; k.Types[1].Fields[1].Type = k.Enums[0] },
			want: []string{`invalid enum name "enum"`}},
		{name: "enum shadows a built-in", mutate: func(k *kind.Kind) { k.Enums[0].Name = "string"; k.Types[1].Fields[1].Type = k.Enums[0] },
			want: []string{`enum "string" shadows a built-in type`}, help: "the built-in type names are reserved; pick another name"},
		{name: "enum declared twice", mutate: func(k *kind.Kind) {
			k.Enums = append(k.Enums, &types.Enum{Name: "Tier", Values: []string{"gold"}})
		}, want: []string{`type "Tier" is declared twice`}, help: "enums and struct types share one namespace; give each type one declaration"},
		{name: "enum named like a struct type", mutate: func(k *kind.Kind) {
			k.Enums = append(k.Enums, &types.Enum{Name: "Actor", Values: []string{"human", "bot"}})
		}, want: []string{`type "Actor" is declared twice`}},
		{name: "enum collides with an input", mutate: func(k *kind.Kind) {
			k.Inputs = append(k.Inputs, &kind.Input{Name: "Tier", Type: types.String})
		}, want: []string{`enum "Tier" collides with input "Tier"`},
			help: "inputs, host functions, decisions, enums and their values share one namespace; rename one of them"},
		{name: "enum collides with a function", mutate: func(k *kind.Kind) {
			k.Funcs = append(k.Funcs, &kind.Func{Name: "Tier", Result: types.Bool})
		}, want: []string{`enum "Tier" collides with function "Tier"`}},
		{name: "enum collides with a decision", mutate: func(k *kind.Kind) {
			k.Decisions = append(k.Decisions, &kind.Decision{Name: "Tier", Reasons: []string{"x"}})
			k.Precedence = append(k.Precedence, "Tier")
		}, want: []string{`enum "Tier" collides with decision "Tier"`}},
		{name: "enum without values", mutate: func(k *kind.Kind) { k.Enums[0].Values = nil },
			want: []string{"enum Tier declares no values"}, help: "list its values, like `enum Tier: critical | standard`"},
		{name: "enum value is a keyword", mutate: func(k *kind.Kind) { k.Enums[0].Values[1] = "when" },
			want: []string{`enum Tier: invalid value "when"`}, help: "a value is a plain identifier, not a keyword"},
		{name: "enum value with a dash", mutate: func(k *kind.Kind) { k.Enums[0].Values[1] = "tier-1" },
			want: []string{`enum Tier: invalid value "tier-1"`}},
		{name: "enum value declared twice", mutate: func(k *kind.Kind) { k.Enums[0].Values = append(k.Enums[0].Values, "critical") },
			want: []string{`enum Tier: value "critical" is declared twice`}, help: "declare each value once"},
		{name: "enum value collides with an input", mutate: func(k *kind.Kind) { k.Enums[0].Values[2] = "environment" },
			want: []string{`enum Tier: value "environment" collides with input "environment"`},
			help: "inputs, host functions, decisions, enums and their values share one namespace; rename one of them"},
		{name: "enum value collides with a function", mutate: func(k *kind.Kind) { k.Enums[0].Values[2] = "split" },
			want: []string{`enum Tier: value "split" collides with function "split"`}},
		{name: "enum value collides with a decision", mutate: func(k *kind.Kind) { k.Enums[0].Values[2] = "deny" },
			want: []string{`enum Tier: value "deny" collides with decision "deny"`}},
		{name: "enum value collides with a struct type", mutate: func(k *kind.Kind) { k.Enums[0].Values[2] = "Release" },
			want: []string{`enum Tier: value "Release" collides with type "Release"`}},
		{name: "enum value collides with an enum", mutate: func(k *kind.Kind) { k.Enums[0].Values[2] = "Tier" },
			want: []string{`enum Tier: value "Tier" collides with type "Tier"`}},
		{name: "field of undeclared enum", mutate: func(k *kind.Kind) {
			k.Types[1].Fields[1].Type = &types.Enum{Name: "Plan", Values: []string{"free"}}
		}, want: []string{`type Service, field "tier": undeclared type Plan`}, help: "declare it with `enum Plan: a | b`"},
		{name: "enum map value", mutate: func(k *kind.Kind) {
			k.Types[1].Fields[3].Type = &types.Map{Key: types.String, Value: k.Enums[0]}
		}, want: []string{`type Service, field "labels": map value type can't be enum Tier`},
			help: "a missing key would read as the zero value, and an enum has none; key the map by the enum instead, or use a list"},
		{name: "enum map value of an input", mutate: func(k *kind.Kind) {
			k.Inputs[3].Type = &types.Map{Key: k.Enums[0], Value: k.Enums[0]}
		}, want: []string{`input "environment": map value type can't be enum Tier`}},
		{name: "payload field of enum type with a default", mutate: func(k *kind.Kind) {
			k.Decisions[2].Fields = append(k.Decisions[2].Fields, &kind.Field{Name: "tier", Type: k.Enums[0], HasDefault: true, Default: constant.EnumValue("standard")})
		}},
		{name: "payload default outside the enum", mutate: func(k *kind.Kind) {
			k.Decisions[2].Fields = append(k.Decisions[2].Fields, &kind.Field{Name: "tier", Type: k.Enums[0], HasDefault: true, Default: constant.EnumValue("gold")})
		}, want: []string{`decision approve, field "tier": default gold is not a Tier`}},
		{name: "payload default of an enum as a string", mutate: func(k *kind.Kind) {
			k.Decisions[2].Fields = append(k.Decisions[2].Fields, &kind.Field{Name: "tier", Type: k.Enums[0], HasDefault: true, Default: "standard"})
		}, want: []string{`decision approve, field "tier": default "standard" is not a Tier`}},

		// Inputs and functions.
		{name: "input name is a keyword", mutate: func(k *kind.Kind) { k.Inputs[3].Name = "input" }, want: []string{`invalid input name "input"`}},
		{name: "input declared twice", mutate: func(k *kind.Kind) {
			k.Inputs = append(k.Inputs, &kind.Input{Name: "actor", Type: types.String})
		}, want: []string{`input "actor" collides with input "actor"`}},
		{name: "function collides with input", mutate: func(k *kind.Kind) { k.Funcs[0].Name = "environment" },
			want: []string{`function "environment" collides with input "environment"`}, help: "inputs, host functions, decisions, enums and their values share one namespace; rename one of them"},
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
			k.Decisions = append(k.Decisions, &kind.Decision{Name: "deny", Reasons: []string{"x"}})
		}, want: []string{`decision "deny" is declared twice`}},
		{name: "decision collides with an input", mutate: func(k *kind.Kind) {
			k.Inputs = append(k.Inputs, &kind.Input{Name: "review", Type: types.String})
		}, want: []string{`decision "review" collides with input "review"`},
			help: "inputs, host functions, decisions, enums and their values share one namespace; rename one of them"},
		{name: "decision collides with a function", mutate: func(k *kind.Kind) {
			k.Funcs = append(k.Funcs, &kind.Func{Name: "approve", Result: types.Bool})
		}, want: []string{`decision "approve" collides with function "approve"`}},
		{name: "lone reason names an enum", mutate: func(k *kind.Kind) { k.Decisions[1].Reasons = []string{"Tier"} },
			want: []string{"decision review: the reason can't name type Tier"}, help: "list the reasons inline, like `reason: critical | standard | internal`"},
		{name: "lone reason names a struct type", mutate: func(k *kind.Kind) { k.Decisions[1].Reasons = []string{"Actor"} },
			want: []string{"decision review: the reason can't name type Actor"}, help: "list the reasons inline, like `reason: a | b`"},
		{name: "lone reason names a built-in type", mutate: func(k *kind.Kind) { k.Decisions[1].Reasons = []string{"string"} },
			want: []string{"decision review: the reason can't name type string"}},
		{name: "a type name among several reasons is a reason", mutate: func(k *kind.Kind) {
			k.Decisions[1].Reasons = []string{"Tier", "string"}
		}},
		{name: "reason as a payload field", mutate: func(k *kind.Kind) {
			k.Decisions[1].Fields = append(k.Decisions[1].Fields, &kind.Field{Name: "reason", Type: types.String})
		}, want: []string{`decision review, field "reason": reason can't be a payload field`}},
		{name: "decision without reasons", mutate: func(k *kind.Kind) { k.Decisions[0].Reasons = nil },
			want: []string{"decision deny declares no reasons", `default: decision deny has no reason "no_rule_matched"`}, help: "declare at least one reason, like `decision deny { reason: no_rule_matched }`"},
		{name: "reason declared twice", mutate: func(k *kind.Kind) { k.Decisions[0].Reasons = append(k.Decisions[0].Reasons, "not_eligible") },
			want: []string{`decision deny: reason "not_eligible" is declared twice`}},
		{name: "reason is a keyword", mutate: func(k *kind.Kind) { k.Decisions[0].Reasons = []string{"when", "no_rule_matched"} },
			want: []string{`decision deny: invalid reason "when"`}},
		{name: "ranked reasons", mutate: func(k *kind.Kind) { k.Decisions[2].Ranked = []string{"release_manager", "payments_sre", "open"} }},
		{name: "ranking names an undeclared reason", mutate: func(k *kind.Kind) {
			k.Decisions[2].Ranked = []string{"release_manager", "lgtm", "payments_sre", "open"}
		},
			want: []string{`precedence approve: names undeclared reason "lgtm"`}},
		{name: "ranking names a reason twice", mutate: func(k *kind.Kind) {
			k.Decisions[2].Ranked = []string{"release_manager", "release_manager", "payments_sre", "open"}
		},
			want: []string{`precedence approve: names "release_manager" twice`}},
		{name: "ranking leaves a reason out", mutate: func(k *kind.Kind) { k.Decisions[2].Ranked = []string{"release_manager"} },
			want: []string{`precedence approve: doesn't name reason "payments_sre"`, `precedence approve: doesn't name reason "open"`}, help: "list every reason exactly once, highest first"},
		{name: "exclusive decisions", mutate: func(k *kind.Kind) { k.Exclusive = [][]kind.Outcome{{{Decision: "review"}, {Decision: "approve"}}} }},
		{name: "exclusive reasons", mutate: func(k *kind.Kind) {
			k.Exclusive = [][]kind.Outcome{{{Decision: "approve", Reason: "release_manager"}, {Decision: "approve", Reason: "payments_sre"}, {Decision: "deny"}}}
		}},
		{name: "exclusive with one outcome", mutate: func(k *kind.Kind) { k.Exclusive = [][]kind.Outcome{{{Decision: "review"}}} },
			want: []string{"exclusive set 1 names fewer than two outcomes"}},
		{name: "exclusive names an undeclared decision", mutate: func(k *kind.Kind) { k.Exclusive = [][]kind.Outcome{{{Decision: "review"}, {Decision: "escalate"}}} },
			want: []string{`exclusive: undeclared decision "escalate"`}},
		{name: "exclusive names an undeclared reason", mutate: func(k *kind.Kind) {
			k.Exclusive = [][]kind.Outcome{{{Decision: "review"}, {Decision: "approve", Reason: "lgtm"}}}
		},
			want: []string{`exclusive: decision approve has no reason "lgtm"`}, help: "approve declares: release_manager, payments_sre, open"},
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
			want: nil},
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
		{name: "default with an undeclared reason", mutate: func(k *kind.Kind) { k.Default.Reason = "nope" },
			want: []string{`default: decision deny has no reason "nope"`}, help: "deny declares: not_eligible, soak_too_short, no_rule_matched"},
		{name: "default with an unknown field", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bak": 15 * time.Minute}}
		}, want: []string{`default: decision approve has no payload field "bak"`}, help: "approve takes reason: release_manager | payments_sre | open, and bake: duration = 1h"},
		{name: "default with a wrong value", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bake": "15m"}}
		}, want: []string{`default: field "bake" value "15m" is not a duration`}},
		{name: "default misses a required field", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "review", Reason: "everyone"}
		}, want: []string{`default: field "approvers" is required and has no value`}, help: "review takes reason: service_owner | everyone, and approvers: list<string>"},
		{name: "default with every field is fine", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "review", Reason: "everyone", Args: map[string]any{"approvers": []any{"leads"}}}
		}},
		{name: "default relying on a field default is fine", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open"}
		}},

		// The conflict outcome follows the default's rules, and only a
		// `collect one` kind may declare one.
		{name: "conflict outcome", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "deny", Reason: "not_eligible"}
		}},
		{name: "conflict outcome with every field", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "review", Reason: "everyone", Args: map[string]any{"approvers": []any{"leads"}}}
		}},
		{name: "conflict outcome on a collecting kind", base: access, mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "read", Reason: "member"}
		}, want: []string{"kind AccessGrant collects all decisions and can't declare a conflict outcome"},
			help: "a collecting kind returns an empty outcome on a conflict, because granting anything on a defect in the policy would fail open; remove `conflict`"},
		{name: "conflict outcome on a collecting kind isn't checked further", base: access, mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "escalate", Reason: "x"}
		}, want: []string{"kind AccessGrant collects all decisions and can't declare a conflict outcome"}},
		{name: "conflict names an unknown decision", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "escalate", Reason: "x"}
		}, want: []string{`conflict names undeclared decision "escalate"`}, help: "the conflict outcome constructs one of the kind's decisions"},
		{name: "conflict with an undeclared reason", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "deny", Reason: "conflicting_rules"}
		}, want: []string{`conflict: decision deny has no reason "conflicting_rules"`}, help: "deny declares: not_eligible, soak_too_short, no_rule_matched"},
		{name: "conflict with an unknown field", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bak": 15 * time.Minute}}
		}, want: []string{`conflict: decision approve has no payload field "bak"`}},
		{name: "conflict with a wrong value", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bake": "15m"}}
		}, want: []string{`conflict: field "bake" value "15m" is not a duration`}, help: "the conflict outcome passes constants of the fields' types"},
		{name: "conflict misses a required field", mutate: func(k *kind.Kind) {
			k.Conflict = &kind.Default{Decision: "review", Reason: "everyone"}
		}, want: []string{`conflict: field "approvers" is required and has no value`}, help: "review takes reason: service_owner | everyone, and approvers: list<string>"},
		{name: "default with a wrong value says what to pass", mutate: func(k *kind.Kind) {
			k.Default = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bake": int64(1)}}
		}, want: []string{`default: field "bake" value 1 is not a duration`}, help: "the default passes constants of the fields' types"},
		{name: "default with an enum argument", mutate: func(k *kind.Kind) {
			k.Decisions[0].Fields = []*kind.Field{{Name: "tier", Type: k.Enums[0]}}
			k.Default.Args = map[string]any{"tier": constant.EnumValue("internal")}
		}},
		{name: "default with an enum argument outside the enum", mutate: func(k *kind.Kind) {
			k.Decisions[0].Fields = []*kind.Field{{Name: "tier", Type: k.Enums[0]}}
			k.Default.Args = map[string]any{"tier": constant.EnumValue("gold")}
		}, want: []string{`default: field "tier" value gold is not a Tier`}},
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
