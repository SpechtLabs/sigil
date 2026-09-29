package policytest_test

import (
	"os"
	"testing"
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
