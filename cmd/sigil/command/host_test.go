package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// The Access kind of the command testdata, as a host defines it in Go,
// with owner implemented: every resource belongs to ada. User keeps its
// name, since a Go struct's name is the Sigil type's name.
type (
	User struct {
		Name    string     `policy:"name"`
		Teams   []string   `policy:"teams"`
		Admin   bool       `policy:"admin"`
		Expires *time.Time `policy:"expires"`
	}
	hostInput struct {
		User     User           `policy:"user"`
		Resource string         `policy:"resource"`
		Age      time.Duration  `policy:"age"`
		Labels   map[string]int `policy:"labels"`
	}
	hostAllow struct {
		TTL    time.Duration `policy:"ttl,default=1h"`
		Scopes []string      `policy:"scopes,default=[]"`
	}
)

var (
	hostDeny   = policy.NewDecision[policy.None]("deny", "banned", "too_old", "no_rule_matched")
	hostAllows = policy.NewDecision[hostAllow]("allow", "admin", "team_member")
	hostAccess = policy.NewKind[hostInput]("Access",
		policy.WithVersion(1),
		policy.WithDecisions(hostDeny, hostAllows),
		policy.WithReasonPrecedence(hostAllows, "admin", "team_member"),
		policy.WithDefault(hostDeny, "no_rule_matched"),
		policy.WithFunc("owner", func(string) string { return "ada" }),
	)
)

// TestHostBinary runs the commands of a binary with the Access kind
// linked in: they need no --kind, call the host's functions, and reject
// a kind file for the same kind that isn't its Schema().
func TestHostBinary(t *testing.T) {
	const (
		access  = "test/testdata/access"
		kindSrc = "test/testdata/access.sigil"
		vault   = `{"user": {"name": "ada"}, "resource": "vault"}`
	)
	stale := filepath.Join(t.TempDir(), "access.sigil")
	if err := os.WriteFile(stale, []byte(strings.Replace(hostAccess.Schema(), "version 1", "version 2", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		args    []string
		stdin   string
		want    string // in the output
		wantErr string
	}{
		{name: "eval calls the host function", args: []string{"eval", "-i", "-", access}, stdin: vault, want: "access.main: allow(team_member)\n  ttl = 15m"},
		{name: "eval with the matching kind file", args: []string{"eval", "-k", kindSrc, "-i", "-", access}, stdin: vault, want: "ttl = 15m"},
		{name: "eval with a stale kind file", args: []string{"eval", "-k", stale, "-i", "-", access}, stdin: vault, wantErr: "doesn't match the kind Access linked into this binary"},
		{name: "a kind file for another kind is loaded on its own", args: []string{"eval", "-k", "eval/testdata/grants.sigil", "-i", "eval/testdata/inputs/grants.json", "eval/testdata/grants"}, want: "grants: 3 decisions"},
		{name: "test passes the case that needs owner", args: []string{"test", "-v", "../../../pkg/policytest/testdata/access"}, want: "--- PASS: ../../../pkg/policytest/testdata/access/main_test.yaml:38: vault owners get fifteen minutes"},
		{name: "check", args: []string{"check", "--config", "check/testdata/config/defaults.yaml", access}},
		{name: "explain", args: []string{"explain", access}, want: "access.main: 6 rules from 1 policy"},
		{name: "export", args: []string{"export"}, want: hostAccess.Schema()},
		{name: "export by name", args: []string{"export", "Access"}, want: hostAccess.Schema()},
		{name: "export as JSON", args: []string{"export", "-o", "json"}, want: "\"kind\": \"Access\",\n  \"version\": 1,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(WithKind(hostAccess))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			switch {
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Execute() = %v, want %q", err, tt.wantErr)
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Execute() = %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("output doesn't contain %q:\n%s", tt.want, out.String())
			}
		})
	}
}
