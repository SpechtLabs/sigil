package config_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/config"
)

func TestServeConfig(t *testing.T) {
	defaults := config.Config{
		Addr:              ":8080",
		ReloadInterval:    30 * time.Second,
		ShutdownTimeout:   15 * time.Second,
		EvaluationTimeout: time.Second,
		LogFormat:         "json",
	}

	tests := []struct {
		env     map[string]string
		edit    func(*config.Config)
		name    string
		wantErr string
		args    []string
	}{
		{name: "defaults", edit: func(*config.Config) {}},
		{
			name: "flags",
			args: []string{"--addr", ":9090", "--policies", "/etc/p", "--teams-file", "/etc/teams.yaml", "--reload-interval", "0", "--evaluation-timeout", "250ms", "--debug", "--log-format", "console"},
			edit: func(c *config.Config) {
				c.Addr, c.PoliciesDir, c.TeamsFile = ":9090", "/etc/p", "/etc/teams.yaml"
				c.ReloadInterval, c.Debug, c.LogFormat = 0, true, "console"
				c.EvaluationTimeout = 250 * time.Millisecond
			},
		},
		{
			name: "environment",
			env: map[string]string{
				"ALERTROUTER_ADDR":               ":7070",
				"ALERTROUTER_POLICIES":           "/mnt/policies",
				"ALERTROUTER_TEAMS_FILE":         "/mnt/teams.yaml",
				"ALERTROUTER_RELOAD_INTERVAL":    "5s",
				"ALERTROUTER_SHUTDOWN_TIMEOUT":   "5s",
				"ALERTROUTER_EVALUATION_TIMEOUT": "2s",
				"ALERTROUTER_DEBUG":              "true",
				"ALERTROUTER_LOG_FORMAT":         "console",
			},
			edit: func(c *config.Config) {
				c.Addr, c.PoliciesDir, c.TeamsFile = ":7070", "/mnt/policies", "/mnt/teams.yaml"
				c.ReloadInterval, c.ShutdownTimeout, c.Debug, c.LogFormat = 5*time.Second, 5*time.Second, true, "console"
				c.EvaluationTimeout = 2 * time.Second
			},
		},
		{
			name: "flags beat the environment",
			env:  map[string]string{"ALERTROUTER_ADDR": ":7070"},
			args: []string{"--addr", ":9090"},
			edit: func(c *config.Config) { c.Addr = ":9090" },
		},
		{name: "empty address", args: []string{"--addr", ""}, wantErr: "listen address is empty"},
		{name: "unknown log format", args: []string{"--log-format", "xml"}, wantErr: "unknown log format xml"},
		{name: "negative reload interval", args: []string{"--reload-interval", "-1s"}, wantErr: "negative"},
		{name: "no shutdown budget", args: []string{"--shutdown-timeout", "0"}, wantErr: "shutdown timeout"},
		{name: "no evaluation budget", args: []string{"--evaluation-timeout", "0"}, wantErr: "evaluation timeout 0s isn't positive"},
		{name: "no evaluation budget from the environment", env: map[string]string{"ALERTROUTER_EVALUATION_TIMEOUT": "-1s"}, wantErr: "evaluation timeout -1s isn't positive"},
		{name: "arguments aren't accepted", args: []string{"extra"}, wantErr: "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			var got *config.Config
			root := config.NewRootCommand("test", func(_ context.Context, cfg config.Config) error {
				got = &cfg
				return nil
			})
			root.SetArgs(append([]string{"serve"}, tt.args...))
			root.SetOut(&bytes.Buffer{})
			err := root.Execute()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(errorChain(err), tt.wantErr) {
					t.Fatalf("Execute error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}

			want := defaults
			tt.edit(&want)
			if got == nil {
				t.Fatal("serve didn't run")
			}
			if *got != want {
				t.Errorf("config = %+v, want %+v", *got, want)
			}
		})
	}
}

func TestVersionCommand(t *testing.T) {
	root := config.NewRootCommand("v1.2.3", nil)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out.String(); got != "alertrouter v1.2.3\n" {
		t.Errorf("version printed %q", got)
	}
}

// errorChain joins the messages of err and everything it wraps, since a
// humane error's message leaves out its cause.
func errorChain(err error) string {
	var parts []string
	for ; err != nil; err = unwrap(err) {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, ": ")
}

func unwrap(err error) error {
	u, ok := err.(interface{ Unwrap() error })
	if !ok {
		return nil
	}
	return u.Unwrap()
}
