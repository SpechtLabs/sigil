package config_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/config"
)

func TestServeConfig(t *testing.T) {
	defaults := config.Config{
		Addr:              ":8080",
		Teams:             []string{"payments", "checkout"},
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
			args: []string{"--addr", ":9090", "--policies", "/etc/p", "--access-policies", "/etc/a", "--team", "payments", "--reload-interval", "0", "--evaluation-timeout", "250ms", "--debug", "--log-format", "console"},
			edit: func(c *config.Config) {
				c.Addr, c.PoliciesDir, c.AccessPoliciesDir, c.Teams = ":9090", "/etc/p", "/etc/a", []string{"payments"}
				c.ReloadInterval, c.Debug, c.LogFormat = 0, true, "console"
				c.EvaluationTimeout = 250 * time.Millisecond
			},
		},
		{
			name: "environment",
			env: map[string]string{
				"DEPLOYGATE_ADDR":               ":7070",
				"DEPLOYGATE_POLICIES":           "/mnt/policies",
				"DEPLOYGATE_ACCESS_POLICIES":    "/mnt/access",
				"DEPLOYGATE_TEAMS":              "payments, checkout,billing",
				"DEPLOYGATE_RELOAD_INTERVAL":    "1m",
				"DEPLOYGATE_SHUTDOWN_TIMEOUT":   "5s",
				"DEPLOYGATE_EVALUATION_TIMEOUT": "2s",
				"DEPLOYGATE_DEBUG":              "true",
				"DEPLOYGATE_LOG_FORMAT":         "console",
			},
			edit: func(c *config.Config) {
				c.Addr, c.PoliciesDir, c.AccessPoliciesDir = ":7070", "/mnt/policies", "/mnt/access"
				c.Teams = []string{"payments", "checkout", "billing"}
				c.ReloadInterval, c.ShutdownTimeout, c.Debug, c.LogFormat = time.Minute, 5*time.Second, true, "console"
				c.EvaluationTimeout = 2 * time.Second
			},
		},
		{
			name: "flags beat the environment",
			env:  map[string]string{"DEPLOYGATE_ADDR": ":7070"},
			args: []string{"--addr", ":9090"},
			edit: func(c *config.Config) { c.Addr = ":9090" },
		},
		{
			name: "repeated and comma-separated teams, deduplicated",
			args: []string{"--team", "payments,checkout", "--team", "payments"},
			edit: func(c *config.Config) { c.Teams = []string{"payments", "checkout"} },
		},
		{name: "unknown log format", args: []string{"--log-format", "xml"}, wantErr: "unknown log format xml"},
		{name: "negative reload interval", args: []string{"--reload-interval", "-1s"}, wantErr: "negative"},
		{name: "no shutdown budget", args: []string{"--shutdown-timeout", "0"}, wantErr: "shutdown timeout"},
		{name: "no evaluation budget", args: []string{"--evaluation-timeout", "0"}, wantErr: "evaluation timeout 0s isn't positive"},
		{name: "empty team list", env: map[string]string{"DEPLOYGATE_TEAMS": " , "}, wantErr: "no team to serve"},
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
			want.Teams = slices.Clone(defaults.Teams)
			tt.edit(&want)
			if got == nil {
				t.Fatal("serve didn't run")
			}
			if got.Addr != want.Addr || got.PoliciesDir != want.PoliciesDir || got.AccessPoliciesDir != want.AccessPoliciesDir ||
				!slices.Equal(got.Teams, want.Teams) ||
				got.ReloadInterval != want.ReloadInterval || got.ShutdownTimeout != want.ShutdownTimeout ||
				got.EvaluationTimeout != want.EvaluationTimeout ||
				got.Debug != want.Debug || got.LogFormat != want.LogFormat {
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
	if got := out.String(); got != "deploygate v1.2.3\n" {
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
