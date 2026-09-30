package config_test

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/config"
)

func TestReadyzURL(t *testing.T) {
	tests := []struct {
		addr, want string
		wantErr    bool
	}{
		{addr: ":8080", want: "http://127.0.0.1:8080/readyz"},
		{addr: "0.0.0.0:9090", want: "http://127.0.0.1:9090/readyz"},
		{addr: "[::]:8080", want: "http://127.0.0.1:8080/readyz"},
		{addr: "127.0.0.1:18080", want: "http://127.0.0.1:18080/readyz"},
		{addr: "[::1]:8080", want: "http://[::1]:8080/readyz"},
		{addr: "8080", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := config.ReadyzURL(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ReadyzURL(%q) = %q, want an error", tt.addr, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadyzURL(%q): %v", tt.addr, err)
			}
			if got != tt.want {
				t.Errorf("ReadyzURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestHealthcheckCommand(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		useEnv  bool
		wantErr string
	}{
		{name: "ready", status: http.StatusOK},
		{name: "ready, address from the environment", status: http.StatusOK, useEnv: true},
		{name: "not ready", status: http.StatusServiceUnavailable, wantErr: "isn't ready: HTTP 503"},
		{name: "nothing listening", wantErr: "didn't answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := closedPort(t)
			if tt.status != 0 {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/readyz" {
						http.NotFound(w, r)
						return
					}
					w.WriteHeader(tt.status)
				}))
				t.Cleanup(srv.Close)
				addr = srv.Listener.Addr().String()
			}

			args := []string{"healthcheck"}
			if tt.useEnv {
				t.Setenv("ALERTROUTER_ADDR", addr)
			} else {
				args = append(args, "--addr", addr)
			}

			root := config.NewRootCommand("test", nil)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)
			err := root.Execute()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("healthcheck: %v", err)
				}
				if out.Len() != 0 {
					t.Errorf("a passing healthcheck printed %q", out.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("healthcheck error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// closedPort returns an address on which nothing listens.
func closedPort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
