package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/config"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/policies"
)

// TestStart checks what the process refuses to start with: a team
// directory that doesn't load, and a team in it without a policy that
// loads, so a broken rollout never becomes ready.
func TestStart(t *testing.T) {
	tests := []struct {
		name      string
		teamsFile string // written to a file when set
		policies  map[string]string
		wantErr   string
		wantTeams []string
	}{
		{name: "the embedded directory and policies", wantTeams: []string{"checkout", "payments"}},
		{
			name:      "a directory of one team",
			teamsFile: "teams:\n  - name: payments\n    oncall: payments-primary\n    channel: \"#payments-alerts\"\n",
			wantTeams: []string{"payments"},
		},
		{
			name:      "a directory that doesn't load",
			teamsFile: "teams:\n  - name: checkout\n    pager: checkout-primary\n",
			wantErr:   "won't start without a team directory that loads",
		},
		{
			name:      "a team without a policy",
			teamsFile: "teams:\n  - name: billing\n    oncall: billing-primary\n    channel: \"#billing\"\n",
			wantErr:   "billing.alerts failed to compile",
		},
		{
			name:     "a team policy without the platform's paging",
			policies: map[string]string{"checkout/alerts.sigil": "policy checkout.alerts: AlertRouting@1\n\nuse platform.routing\n\nrouting()\n"},
			wantErr:  "won't start without team policies that load",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Addr:              "127.0.0.1:0",
				ShutdownTimeout:   config.DefaultShutdownTimeout,
				EvaluationTimeout: config.DefaultEvaluationTimeout,
				LogFormat:         "json",
			}
			if tt.teamsFile != "" {
				cfg.TeamsFile = filepath.Join(t.TempDir(), "teams.yaml")
				writeFile(t, cfg.TeamsFile, tt.teamsFile)
			}
			if tt.policies != nil {
				cfg.PoliciesDir = teamsDir(t, tt.policies)
			}

			svc, err := start(context.Background(), cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(chain(err), tt.wantErr) {
					t.Fatalf("start error = %v, want one containing %q", err, tt.wantErr)
				}
				if len(err.Advice()) == 0 {
					t.Error("start error has no advice")
				}
				return
			}
			if err != nil {
				t.Fatalf("start: %s", chain(err))
			}
			snap, ok := svc.store.Snapshot()
			if !ok {
				t.Fatal("start returned without a loaded bundle")
			}
			if got := strings.Join(snap.TeamNames(), ","); got != strings.Join(tt.wantTeams, ",") {
				t.Errorf("teams = %s, want %s", got, strings.Join(tt.wantTeams, ","))
			}
			if want := sourceName(cfg.PoliciesDir); snap.Source != want {
				t.Errorf("source = %q, want %q", snap.Source, want)
			}
			if svc.server == nil {
				t.Error("start returned no server")
			}
		})
	}
}

func TestSourceName(t *testing.T) {
	tests := []struct{ dir, want string }{
		{dir: "", want: store.SourceEmbedded},
		{dir: "/etc/alertrouter/policies", want: "/etc/alertrouter/policies"},
	}
	for _, tt := range tests {
		if got := sourceName(tt.dir); got != tt.want {
			t.Errorf("sourceName(%q) = %q, want %q", tt.dir, got, tt.want)
		}
	}
}

// teamsDir copies the embedded team policies into a temporary directory and
// applies overrides.
func teamsDir(t *testing.T, overrides map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(policies.Teams, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(name) != ".sigil" {
			return err
		}
		data, err := fs.ReadFile(policies.Teams, name)
		if err != nil {
			return err
		}
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("copying the embedded teams: %v", err)
	}
	for name, content := range overrides {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// chain joins the messages of err and everything it wraps, since a humane
// error's message leaves out its cause.
func chain(err error) string {
	var parts []string
	for ; err != nil; err = errors.Unwrap(err) {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, ": ")
}
