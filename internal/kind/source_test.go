package kind_test

import (
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/kind"
)

func TestSource(t *testing.T) {
	t.Run("deploy approval", func(t *testing.T) {
		want := `kind DeployApproval version 1

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

fn split(s: string, sep: string) -> list<string>

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

precedence deny > review > approve
default deny("no_rule_matched")
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

decision read(reason: string)
decision write(reason: string)
decision admin(reason: string, ttl: duration = 8h)
decision customer_data_writer(reason: string)
decision development_environment_writer(reason: string)

collect all
`
		if got := access().Source(); got != want {
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
		want := `default approve("open", bake: 15m, detail: ["a"], aaa: 2, zzz: 1)`
		if got := k.Default.Source(k.Decision("approve")); got != want {
			t.Errorf("Source() = %q, want %q", got, want)
		}
		if got := k.Default.Source(nil); got != `default approve("open", aaa: 2, bake: 15m, detail: ["a"], zzz: 1)` {
			t.Errorf("Source(nil) = %q", got)
		}
	})

	t.Run("empty kind", func(t *testing.T) {
		k := &kind.Kind{Name: "Empty", Version: 2, Collect: true}
		if got, want := k.Source(), "kind Empty version 2\n\ncollect all\n"; got != want {
			t.Errorf("Source() = %q, want %q", got, want)
		}
	})
}
