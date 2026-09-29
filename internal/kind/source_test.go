package kind_test

import (
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

func TestSource(t *testing.T) {
	t.Run("deploy approval", func(t *testing.T) {
		want := `kind DeployApproval version 1

enum Tier: critical | standard | internal

type Release {
  soak: duration
  hotfix: bool
}

type Service {
  name: string
  tier: Tier
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
  reason: not_eligible | soak_too_short | no_rule_matched
}

decision review {
  reason: service_owner | everyone
  approvers: list<string>
}

decision approve {
  reason: release_manager | payments_sre | open
  bake: duration = 1h
}

collect one
precedence deny > review > approve

default deny(reason: no_rule_matched)
`
		if got := deploy().Source(); got != want {
			t.Errorf("Source() =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("access grant", func(t *testing.T) {
		want := `kind AccessGrant version 1

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor

decision read {
  reason: member
}

decision write {
  reason: member
}

decision admin {
  reason: member | everyone
  ttl: duration = 8h
}

decision customer_data_writer {
  reason: member
}

decision development_environment_writer {
  reason: member
}

collect all
`
		if got := access().Source(); got != want {
			t.Errorf("Source() =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("enums in declaration order before the types", func(t *testing.T) {
		tier := &types.Enum{Name: "Tier", Values: []string{"critical", "standard"}}
		region := &types.Enum{Name: "Region", Values: []string{"eu"}}
		k := &kind.Kind{
			Name: "Routing", Version: 1, Collect: kind.CollectAll,
			Enums:  []*types.Enum{tier, region},
			Inputs: []*kind.Input{{Name: "quota", Type: &types.Map{Key: region, Value: &types.List{Elem: tier}}}},
			Decisions: []*kind.Decision{{Name: "route", Reasons: []string{"nearest"}, Fields: []*kind.Field{
				{Name: "tier", Type: &types.Optional{Elem: tier}},
				{Name: "regions", Type: &types.List{Elem: region}, HasDefault: true, Default: []any{constant.EnumValue("eu")}},
			}}},
		}
		want := `kind Routing version 1

enum Tier: critical | standard
enum Region: eu

input quota: map<Region, list<Tier>>

decision route {
  reason: nearest
  tier: ?Tier
  regions: list<Region> = [eu]
}

collect all
`
		if got := k.Source(); got != want {
			t.Errorf("Source() =\n%s\nwant\n%s", got, want)
		}
	})

	t.Run("default with arguments in field order", func(t *testing.T) {
		k := deploy()
		k.Decisions[2].Fields = append(k.Decisions[2].Fields, &kind.Field{Name: "detail", Type: k.Decisions[1].Fields[0].Type})
		k.Default = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{
			"detail": []any{"a"},
			"bake":   15 * time.Minute,
			"zzz":    int64(1), // unknown fields come last, sorted
			"aaa":    int64(2),
		}}
		want := `approve(reason: open, bake: 15m, detail: ["a"], aaa: 2, zzz: 1)`
		if got := k.Default.Call(k.Decision("approve")); got != want {
			t.Errorf("Call() = %q, want %q", got, want)
		}
		if got := k.Default.Call(nil); got != `approve(reason: open, aaa: 2, bake: 15m, detail: ["a"], zzz: 1)` {
			t.Errorf("Call(nil) = %q", got)
		}
	})

	t.Run("conflict outcome after the default", func(t *testing.T) {
		k := deploy()
		k.Conflict = &kind.Default{Decision: "approve", Reason: "open", Args: map[string]any{"bake": 15 * time.Minute}}
		want := "collect one\nprecedence deny > review > approve\n\ndefault deny(reason: no_rule_matched)\nconflict approve(reason: open, bake: 15m)\n"
		if got := k.Source(); !strings.HasSuffix(got, want) {
			t.Errorf("Source() ends in\n%s\nwant it to end in\n%s", got[max(0, len(got)-len(want)):], want)
		}
	})

	t.Run("empty kind", func(t *testing.T) {
		k := &kind.Kind{Name: "Empty", Version: 2, Collect: kind.CollectAll}
		if got, want := k.Source(), "kind Empty version 2\n\ncollect all\n"; got != want {
			t.Errorf("Source() = %q, want %q", got, want)
		}
	})
}
