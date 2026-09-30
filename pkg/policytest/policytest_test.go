package policytest_test

import (
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
	"github.com/spechtlabs/sigil/pkg/policytest"
)

type (
	User struct {
		Name    string     `policy:"name"`
		Teams   []string   `policy:"teams"`
		Admin   bool       `policy:"admin"`
		Expires *time.Time `policy:"expires"`
	}
	Input struct {
		User     User           `policy:"user"`
		Resource string         `policy:"resource"`
		Age      time.Duration  `policy:"age"`
		Labels   map[string]int `policy:"labels"`
	}
	AllowData struct {
		TTL    time.Duration `policy:"ttl,default=1h"`
		Scopes []string      `policy:"scopes,default=[]"`
	}
)

var (
	Deny  = policy.NewDecision[policy.None]("deny", "banned", "too_old", "no_rule_matched")
	Allow = policy.NewDecision[AllowData]("allow", "admin", "team_member")

	// Access is the kind of the `sigil test` testdata, with owner
	// implemented: every resource belongs to ada.
	Access = policy.NewKind[Input]("Access",
		policy.WithVersion(1),
		policy.WithDecisions(Deny, Allow),
		policy.WithReasonPrecedence(Allow.Reason("admin"), Allow.Reason("team_member")),
		policy.WithDefault(Deny.Reason("no_rule_matched")),
		policy.WithFunc("owner", func(string) string { return "ada" }),
	)
)

func TestRun(t *testing.T) {
	policytest.Run(t, Access, os.DirFS("testdata"))
}

func TestSchema(t *testing.T) {
	policytest.Schema(t, Access, "../../cmd/sigil/command/test/testdata/access.sigil")
}

// TestRunStubsWithOptions runs a test file whose stubs replace the real
// owner, with the host's load options: the root's param bound from Go
// and a required policy from a trusted source, which the case stub
// reaches too. A stub's error fails the evaluation, as a case can expect.
func TestRunStubsWithOptions(t *testing.T) {
	platform := fstest.MapFS{"guard.sigil": {Data: []byte(`policy access.guard: Access@1

when owner(resource) == "mallory" {
  deny(reason: banned)
}
`)}}
	team := fstest.MapFS{
		"team.sigil": {Data: []byte(`policy access.team: Access@1

use access.guard

param who: string

guard()

when owner(resource) == who {
  allow(reason: team_member, ttl: 15m)
}
`)},
		"team_test.yaml": {Data: []byte(`policy: access.team
stubs:
  owner: {returns: bob}
cases:
  - name: the file's stub replaces owner
    input: {user: {name: bob}, resource: vault}
    expect: {decision: allow, reason: team_member, payload: {ttl: 15m}}
  - name: a case's stub reaches the trusted guard
    input: {user: {name: bob}, resource: vault}
    stubs:
      owner: {returns: mallory}
    expect: {decision: deny, reason: banned}
  - name: a call entry that doesn't match falls back to returns
    input: {user: {name: ada}, resource: vault}
    stubs:
      owner:
        calls:
          - {args: [nothing], returns: bob}
        returns: ada
    expect: {decision: deny, reason: no_rule_matched}
  - name: a failing owner fails the evaluation
    input: {user: {name: bob}, resource: vault}
    stubs:
      owner: {error: directory unavailable}
    expect: {error: "host function owner failed: directory unavailable"}
`)},
	}
	policytest.Run(t, Access, team,
		policy.Params{"who": "bob"},
		policy.Require("access.guard", policy.From(platform)))
}
