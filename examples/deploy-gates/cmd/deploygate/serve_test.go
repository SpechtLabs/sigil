package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/config"
)

// TestNewFreeze checks the freeze source start builds from the
// configuration: a fixed list, or an OFREP source that start asks once and
// serve keeps refreshing, which a flag service that isn't up yet doesn't
// stop.
func TestNewFreeze(t *testing.T) {
	flags := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"key":"change-freeze","value":"production","reason":"TARGETING_MATCH"}`)
	}))
	defer flags.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()

	ofrep := func(url string) config.Config {
		return config.Config{FreezeOFREPURL: url, FreezeFlag: "change-freeze", FreezeKnownEnvironments: []string{"production", "staging"}, FreezeRefreshInterval: time.Second, FreezeMaxStaleness: time.Minute}
	}
	tests := []struct {
		name        string
		cfg         config.Config
		wantName    string
		wantOFREP   bool
		wantEnvs    []string
		wantUnknown bool
		wantErr     string
	}{
		{name: "nothing frozen", wantName: "none", wantEnvs: []string{}},
		{name: "a fixed freeze", cfg: config.Config{FreezeEnvironments: []string{"production", "staging"}}, wantName: "static production,staging", wantEnvs: []string{"production", "staging"}},
		{name: "a flag service", cfg: ofrep(flags.URL), wantName: "ofrep " + flags.URL + " flag change-freeze", wantOFREP: true, wantEnvs: []string{"production"}},
		{name: "a flag service that isn't up yet", cfg: ofrep(down.URL), wantName: "ofrep " + down.URL + " flag change-freeze", wantOFREP: true, wantEnvs: []string{}, wantUnknown: true},
		{name: "flag service settings that can't work", cfg: ofrep("flagd:8016"), wantName: "ofrep flagd:8016 flag change-freeze", wantErr: "isn't an absolute http or https URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := freezeName(tt.cfg); got != tt.wantName {
				t.Errorf("freezeName = %q, want %q", got, tt.wantName)
			}

			var svc service
			src, herr := svc.newFreeze(context.Background(), tt.cfg)
			if tt.wantErr != "" {
				if herr == nil || !strings.Contains(herr.Error(), tt.wantErr) {
					t.Fatalf("newFreeze error = %v, want one containing %q", herr, tt.wantErr)
				}
				return
			}
			if herr != nil {
				t.Fatalf("newFreeze: %v", herr)
			}
			if (svc.ofrep != nil) != tt.wantOFREP {
				t.Errorf("kept an OFREP source to refresh: %v, want %v", svc.ofrep != nil, tt.wantOFREP)
			}
			if got := src.Freeze(); !slices.Equal(got.Environments, tt.wantEnvs) || got.Unknown != tt.wantUnknown {
				t.Errorf("Freeze() = %+v, want environments %v, unknown %v", got, tt.wantEnvs, tt.wantUnknown)
			}
		})
	}
}
