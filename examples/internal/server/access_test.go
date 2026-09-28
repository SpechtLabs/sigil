package server_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/examples/internal/server"
)

// TestGrants covers every rule of access.main through the endpoint, and the
// ways an evaluation can fail.
func TestGrants(t *testing.T) {
	tests := []struct {
		check      func(t *testing.T, resp server.AccessResponse)
		name       string
		body       string
		wantStatus int
		wantGrants []string
	}{
		{name: "team member", body: accessRequest("ada", "", "payments", "production", "payments"), wantStatus: http.StatusOK, wantGrants: []string{"reader team_member", "deployer team_member 8h"}},
		{name: "anyone in staging", body: accessRequest("ada", "", "payments", "staging", "marketing"), wantStatus: http.StatusOK, wantGrants: []string{"reader everyone_in_staging"}},
		{name: "on-call sre", body: accessRequest("sam", "", "payments", "production", "payments-sre"), wantStatus: http.StatusOK, wantGrants: []string{"deployer oncall 2h"}},
		{name: "another team's sre", body: accessRequest("sam", "", "payments", "production", "checkout-sre"), wantStatus: http.StatusForbidden, wantGrants: []string{}},
		{name: "platform member", body: accessRequest("pat", "", "payments", "production", "platform"), wantStatus: http.StatusOK, wantGrants: []string{"release_manager platform_member 4h"}},
		{name: "admin clearance", body: accessRequest("root", "admin", "payments", "production"), wantStatus: http.StatusOK, wantGrants: []string{"admin clearance 1h"}},
		{name: "an admin in platform isn't a release manager too", body: accessRequest("root", "admin", "payments", "production", "platform"), wantStatus: http.StatusOK, wantGrants: []string{"admin clearance 1h"}},
		{name: "break glass", body: accessRequest("bea", "", "payments", "production", "break-glass"), wantStatus: http.StatusOK, wantGrants: []string{"admin break_glass 15m"}},
		{name: "compliance", body: accessRequest("cai", "", "payments", "production", "compliance"), wantStatus: http.StatusOK, wantGrants: []string{"auditor compliance_member"}},
		{
			name: "nothing granted", body: accessRequest("ada", "", "payments", "production", "marketing"), wantStatus: http.StatusForbidden, wantGrants: []string{},
			check: func(t *testing.T, resp server.AccessResponse) {
				if resp.Error != nil {
					t.Errorf("error = %+v, an empty outcome isn't an error", resp.Error)
				}
			},
		},
		{
			name: "break glass and platform conflict", body: accessRequest("bea", "", "payments", "production", "platform", "break-glass"), wantStatus: http.StatusConflict, wantGrants: []string{},
			check: func(t *testing.T, resp server.AccessResponse) {
				assertConflict(t, resp.Conflict, resp.Error)
			},
		},
		{
			name: "separation of duties", body: accessRequest("cai", "", "payments", "production", "payments", "compliance"), wantStatus: http.StatusUnprocessableEntity, wantGrants: []string{},
			check: func(t *testing.T, resp server.AccessResponse) {
				if len(resp.Asserts) != 1 || resp.Asserts[0].Reason != "sod_auditor_deployer" || !strings.Contains(resp.Asserts[0].Location, "access/guardrails.sigil") {
					t.Errorf("asserts = %+v, want sod_auditor_deployer in access/guardrails.sigil", resp.Asserts)
				}
				if len(resp.Trace) != 3 {
					t.Errorf("trace = %+v, want the three candidates the assert read", resp.Trace)
				}
			},
		},
		{
			name: "unnamed actor", body: accessRequest("", "", "payments", "production", "payments"), wantStatus: http.StatusUnprocessableEntity, wantGrants: []string{},
			check: func(t *testing.T, resp server.AccessResponse) {
				if len(resp.Asserts) != 1 || resp.Asserts[0].Reason != "named_actor" {
					t.Errorf("asserts = %+v, want named_actor", resp.Asserts)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newEnv(t, loaded).srv.Handler(), http.MethodPost, "/api/v1/access/grants", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var resp server.AccessResponse
			decode(t, rec, &resp)
			if resp.Policy != "access.main" || resp.Team != "payments" || resp.Environment == "" {
				t.Errorf("policy, team, environment = %s, %s, %s", resp.Policy, resp.Team, resp.Environment)
			}
			if got := grantStrings(resp.Grants); !slices.Equal(got, tt.wantGrants) {
				t.Errorf("grants = %v, want %v", got, tt.wantGrants)
			}
			if !strings.Contains(rec.Body.String(), `"grants":[`) || !strings.Contains(rec.Body.String(), `"trace":[`) {
				t.Errorf("body %s, want grants and trace as arrays, never null", rec.Body)
			}
			if tt.check != nil {
				tt.check(t, resp)
			}
		})
	}
}

func TestGrantsReject(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{name: "no team", body: accessRequest("ada", "", "", "production", "payments"), wantMsg: "names no team"},
		{name: "unknown field", body: `{"actor":{"name":"ada","groups":[],"clearance":"","roles":["admin"]},"team":"payments","environment":"production"}`, wantMsg: "roles"},
		{name: "malformed json", body: `{"actor":`, wantMsg: "isn't a valid request"},
		{name: "empty body", body: " ", wantMsg: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newEnv(t, loaded).srv.Handler(), http.MethodPost, "/api/v1/access/grants", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
			}
			var env server.ErrorEnvelope
			decode(t, rec, &env)
			if !strings.Contains(errorText(env.Error), tt.wantMsg) || len(env.Error.Advice) == 0 {
				t.Errorf("error %+v, want one mentioning %q, with advice", env.Error, tt.wantMsg)
			}
		})
	}
}

func TestGrantMetrics(t *testing.T) {
	env := newEnv(t, loaded)
	h := env.srv.Handler()
	do(h, http.MethodPost, "/api/v1/access/grants", accessRequest("sam", "", "payments", "production", "payments-sre"))
	do(h, http.MethodPost, "/api/v1/access/grants", accessRequest("cai", "", "payments", "production", "payments", "compliance"))

	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	for _, want := range []string{
		`deploygate_access_grants_total{reason="oncall",role="deployer",team="payments"} 1`,
		`deploygate_evaluation_errors_total{kind="assertion",stage="access",team="payments"} 1`,
		`deploygate_access_evaluation_duration_seconds_count{team="payments"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
	// A failed evaluation grants nothing, so the candidates the assert
	// rejected aren't counted as grants.
	if strings.Contains(body, `role="auditor"`) {
		t.Error("a rejected evaluation counted its candidates as grants")
	}
}
