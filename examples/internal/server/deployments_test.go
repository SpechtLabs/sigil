package server_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/spechtlabs/sigil/examples/internal/server"
)

// assertedCheckout is checkout's policy with an input assert on the service
// name, so a test can make the deploy stage fail after the access stage
// passed.
const assertedCheckout = `policy checkout.production: DeployApproval@1

use deploy.guardrails
use deploy.production

assert("named_actor", actor.name != "")
assert("named_service", service.name != "")

guardrails()

production(approvers: ["checkout-leads"], tiers: ["standard"])
`

func TestDeployments(t *testing.T) {
	tests := []struct {
		check      func(t *testing.T, resp server.DecisionResponse)
		edit       func(r, actor map[string]any)
		env        envOptions
		name       string
		team       string
		wantStatus int
		wantDec    string
		wantReason string
		wantRoles  []string
	}{
		{
			name:       "a team member owning a pci service goes to review",
			team:       "payments",
			wantStatus: http.StatusAccepted,
			wantDec:    "review",
			wantReason: "service_owner",
			wantRoles:  []string{"reader team_member", "deployer team_member 8h"},
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{"approvers":["payments-leads","security-leads"]}`)
				if len(resp.Trace) == 0 || !resp.Trace[0].Winner {
					t.Fatalf("trace = %+v, want the winner first", resp.Trace)
				}
				w := resp.Trace[0]
				if w.Policy != "deploy.production" || !strings.Contains(w.Location, "→") || len(w.Conditions) == 0 {
					t.Errorf("winner = %+v, want deploy.production reached through an invocation, with conditions", w)
				}
				if g := resp.Access.Grants[1]; g.Policy != "access.main" || !strings.Contains(g.Location, "main.sigil:") {
					t.Errorf("grant = %+v, want its policy and location", g)
				}
			},
		},
		{
			name: "the on-call sre gets an approval with a bake",
			team: "payments",
			edit: func(_, actor map[string]any) {
				actor["groups"] = []string{"payments-sre"}
			},
			wantStatus: http.StatusOK,
			wantDec:    "approve",
			wantReason: "payments_sre",
			wantRoles:  []string{"deployer oncall 2h"},
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{"bake":"15m"}`)
			},
		},
		{
			name: "a release manager approves a critical service",
			team: "payments",
			edit: func(r, actor map[string]any) {
				actor["groups"] = []string{"payments", "platform"}
				r["service"].(map[string]any)["tier"] = "critical"
			},
			wantStatus: http.StatusOK,
			wantDec:    "approve",
			wantReason: "release_manager",
			wantRoles:  []string{"reader team_member", "deployer team_member 8h", "release_manager platform_member 4h"},
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{"bake":"1h"}`)
			},
		},
		{
			name: "a short soak is denied",
			team: "payments",
			edit: func(r, _ map[string]any) {
				r["release"] = map[string]any{"soak": "2h", "hotfix": false}
			},
			wantStatus: http.StatusForbidden,
			wantDec:    "deny",
			wantReason: "soak_too_short",
			wantRoles:  []string{"reader team_member", "deployer team_member 8h"},
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Payload, `{}`)
			},
		},
		{
			name: "an actor outside the team gets no role and isn't eligible",
			team: "payments",
			edit: func(_, actor map[string]any) {
				actor["groups"] = []string{"marketing"}
			},
			wantStatus: http.StatusForbidden,
			wantDec:    "deny",
			wantReason: "not_eligible",
			wantRoles:  []string{},
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertJSON(t, resp.Access, `{"policy":"access.main","grants":[]}`)
			},
		},
		{
			name: "an unnamed actor fails the access guardrails before the deploy policy runs",
			team: "checkout",
			edit: func(_, actor map[string]any) {
				actor["name"] = ""
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantDec:    "deny",
			wantReason: "no_rule_matched",
			wantRoles:  []string{},
			check: func(t *testing.T, resp server.DecisionResponse) {
				if len(resp.Asserts) != 1 || resp.Asserts[0].Reason != "named_actor" || resp.Asserts[0].Policy != "access.guardrails" {
					t.Errorf("asserts = %+v, want access.guardrails' named_actor", resp.Asserts)
				}
				if resp.Error == nil || !strings.Contains(resp.Error.Message, "access.main") || len(resp.Error.Advice) == 0 {
					t.Errorf("error = %+v, want a message naming access.main, with advice", resp.Error)
				}
				assertJSON(t, resp.Trace, `[]`)
				assertJSON(t, resp.Payload, `{}`)
			},
		},
		{
			name: "a compliance member of the team trips separation of duties",
			team: "payments",
			edit: func(_, actor map[string]any) {
				actor["groups"] = []string{"payments", "compliance"}
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantDec:    "deny",
			wantReason: "no_rule_matched",
			wantRoles:  []string{},
			check: func(t *testing.T, resp server.DecisionResponse) {
				if len(resp.Asserts) != 1 || resp.Asserts[0].Reason != "sod_auditor_deployer" {
					t.Errorf("asserts = %+v, want sod_auditor_deployer", resp.Asserts)
				}
			},
		},
		{
			name: "break-glass and platform together are a conflict",
			team: "payments",
			edit: func(_, actor map[string]any) {
				actor["groups"] = []string{"platform", "break-glass"}
			},
			wantStatus: http.StatusConflict,
			wantDec:    "deny",
			wantReason: "no_rule_matched",
			wantRoles:  []string{},
			check: func(t *testing.T, resp server.DecisionResponse) {
				assertConflict(t, resp.Conflict, resp.Error)
			},
		},
		{
			name: "a failed assert in the deploy policy answers with the fallback and the grants",
			team: "checkout",
			env:  envOptions{deployLoaded: true, accessLoaded: true, teamOverrides: map[string]string{"checkout/production.sigil": assertedCheckout}},
			edit: func(r, actor map[string]any) {
				actor["groups"] = []string{"checkout"}
				r["service"].(map[string]any)["name"] = ""
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantDec:    "deny",
			wantReason: "no_rule_matched",
			wantRoles:  []string{"reader team_member", "deployer team_member 8h"},
			check: func(t *testing.T, resp server.DecisionResponse) {
				if len(resp.Asserts) != 1 || resp.Asserts[0].Reason != "named_service" || resp.Asserts[0].Policy != "checkout.production" {
					t.Errorf("asserts = %+v, want checkout.production's named_service", resp.Asserts)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := tt.env
			if !env.deployLoaded {
				env = loaded
			}
			h := newEnv(t, env).srv.Handler()
			rec := do(h, http.MethodPost, "/api/v1/teams/"+tt.team+"/deployments", deployRequest(tt.edit))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var resp server.DecisionResponse
			decode(t, rec, &resp)
			if resp.Team != tt.team || resp.Policy != tt.team+".production" {
				t.Errorf("team, policy = %s, %s", resp.Team, resp.Policy)
			}
			if resp.Decision != tt.wantDec || resp.Reason != tt.wantReason {
				t.Errorf("decision = %s(%s), want %s(%s)", resp.Decision, resp.Reason, tt.wantDec, tt.wantReason)
			}
			if resp.Access == nil || resp.Access.Policy != "access.main" {
				t.Fatalf("access = %+v, want the access stage", resp.Access)
			}
			if got := grantStrings(resp.Access.Grants); !slices.Equal(got, tt.wantRoles) {
				t.Errorf("grants = %v, want %v", got, tt.wantRoles)
			}
			tt.check(t, resp)
		})
	}
}

func TestDeploymentsReject(t *testing.T) {
	tests := []struct {
		name       string
		team       string
		edit       func(r, actor map[string]any)
		body       string
		wantStatus int
		wantMsg    string
	}{
		{name: "unknown team", team: "billing", wantStatus: http.StatusNotFound, wantMsg: `team "billing" isn't served`},
		{name: "malformed json", team: "payments", body: `{"release":`, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "empty body", team: "payments", body: " ", wantStatus: http.StatusBadRequest, wantMsg: "empty"},
		{name: "roles are no longer accepted", team: "payments", edit: func(_, actor map[string]any) { actor["roles"] = []string{"deployer"} }, wantStatus: http.StatusBadRequest, wantMsg: "roles"},
		{name: "teams are no longer accepted", team: "payments", edit: func(_, actor map[string]any) { actor["teams"] = []string{"payments"} }, wantStatus: http.StatusBadRequest, wantMsg: "teams"},
		{name: "unknown field tier at the top level", team: "payments", edit: func(r, _ map[string]any) { r["tier"] = "standard" }, wantStatus: http.StatusBadRequest, wantMsg: "tier"},
		{name: "numeric duration", team: "payments", edit: func(r, _ map[string]any) { r["release"] = map[string]any{"soak": 21600} }, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "unparsable duration", team: "payments", edit: func(r, _ map[string]any) { r["release"] = map[string]any{"soak": "a while"} }, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "negative soak", team: "payments", edit: func(r, _ map[string]any) { r["release"] = map[string]any{"soak": "-1h"} }, wantStatus: http.StatusBadRequest, wantMsg: "release.soak is negative"},
		{name: "two json values", team: "payments", body: deployRequest(nil) + deployRequest(nil), wantStatus: http.StatusBadRequest, wantMsg: "more than one JSON value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			if body == "" {
				body = deployRequest(tt.edit)
			}
			rec := do(newEnv(t, loaded).srv.Handler(), http.MethodPost, "/api/v1/teams/"+tt.team+"/deployments", body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var env server.ErrorEnvelope
			decode(t, rec, &env)
			if !strings.Contains(errorText(env.Error), tt.wantMsg) {
				t.Errorf("body %s doesn't mention %q", rec.Body, tt.wantMsg)
			}
			if env.Error != nil && len(env.Error.Advice) == 0 {
				t.Errorf("error %+v has no advice", env.Error)
			}
		})
	}
}

func TestDeploymentMetrics(t *testing.T) {
	env := newEnv(t, loaded)
	h := env.srv.Handler()
	do(h, http.MethodPost, "/api/v1/teams/payments/deployments", deployRequest(nil))
	do(h, http.MethodPost, "/api/v1/teams/payments/deployments", deployRequest(func(_, actor map[string]any) {
		actor["groups"] = []string{"platform", "break-glass"}
	}))
	do(h, http.MethodPost, "/api/v1/teams/checkout/deployments", deployRequest(func(_, actor map[string]any) {
		actor["name"] = ""
	}))

	rec := do(h, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`deploygate_decisions_total{decision="review",policy="payments.production",reason="service_owner",team="payments"} 1`,
		`deploygate_access_grants_total{reason="team_member",role="deployer",team="payments"} 1`,
		`deploygate_access_grants_total{reason="team_member",role="reader",team="payments"} 1`,
		`deploygate_access_evaluation_duration_seconds_count{team="payments"} 2`,
		`deploygate_evaluation_errors_total{kind="conflict",stage="access",team="payments"} 1`,
		`deploygate_evaluation_errors_total{kind="assertion",stage="access",team="checkout"} 1`,
		`deploygate_evaluation_duration_seconds_count{team="payments"} 1`,
		`deploygate_policy_reloads_total{kind="DeployApproval",result="success"} 1`,
		`deploygate_policy_reloads_total{kind="AccessGrant",result="success"} 1`,
		`deploygate_policy_reloads_total{kind="AccessGrant",result="failure"} 0`,
		`deploygate_policy_loaded_info{kind="DeployApproval",policy="payments.production",source="embedded",team="payments"} 1`,
		`deploygate_policy_loaded_info{kind="AccessGrant",policy="access.main",source="embedded",team=""} 1`,
		`deploygate_policy_last_reload_timestamp_seconds `,
		`deploygate_requests_total{code="202",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
		`deploygate_requests_total{code="409",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
		`go_goroutines`,
		`process_cpu_seconds_total`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
	// The deploy policy didn't run for the failed access stages, so they
	// decided nothing.
	if strings.Contains(body, `deploygate_decisions_total{decision="deny"`) {
		t.Error("a failed access stage counted a deploy decision")
	}
}

// TestDeploymentSpans checks the two stage spans: siblings under the request
// span, access first, with the grants on the access span and the derived
// roles on the evaluation span.
func TestDeploymentSpans(t *testing.T) {
	tests := []struct {
		name       string
		edit       func(r, actor map[string]any)
		wantGrants int64
		wantRoles  []string
		wantDeploy bool
		wantError  bool
	}{
		{name: "team member", wantGrants: 2, wantRoles: []string{"deployer"}, wantDeploy: true},
		{name: "admin", edit: func(_, actor map[string]any) { actor["clearance"] = "admin" }, wantGrants: 3, wantRoles: []string{"deployer", "release_manager"}, wantDeploy: true},
		{name: "outsider", edit: func(_, actor map[string]any) { actor["groups"] = []string{"marketing"} }, wantRoles: []string{}, wantDeploy: true},
		{name: "conflict", edit: func(_, actor map[string]any) { actor["groups"] = []string{"platform", "break-glass"} }, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, loaded)
			env.spans.Reset()
			do(env.srv.Handler(), http.MethodPost, "/api/v1/teams/payments/deployments", deployRequest(tt.edit))
			spans := env.spans.GetSpans()

			accessSpan, ok := findSpan(spans, "deploygate.access")
			if !ok {
				t.Fatalf("no deploygate.access span among %d spans", len(spans))
			}
			attrs := attributes(accessSpan.Attributes)
			for key, want := range map[attribute.Key]string{
				"sigil.kind":        "AccessGrant",
				"sigil.policy":      "access.main",
				"sigil.team":        "payments",
				"sigil.environment": "production",
			} {
				if got := attrs[key].AsString(); got != want {
					t.Errorf("access %s = %q, want %q", key, got, want)
				}
			}
			if got := attrs["sigil.grants"].AsInt64(); got != tt.wantGrants {
				t.Errorf("sigil.grants = %d, want %d", got, tt.wantGrants)
			}
			if got := countEvents(accessSpan, "sigil.grant"); int64(got) != tt.wantGrants {
				t.Errorf("sigil.grant events = %d, want %d", got, tt.wantGrants)
			}
			if (accessSpan.Status.Code.String() == "Error") != tt.wantError {
				t.Errorf("access span status = %v, want error %v", accessSpan.Status, tt.wantError)
			}

			evalSpan, ok := findSpan(spans, "deploygate.evaluate")
			if ok != tt.wantDeploy {
				t.Fatalf("deploygate.evaluate present = %v, want %v", ok, tt.wantDeploy)
			}
			if !ok {
				return
			}
			if got := attributes(evalSpan.Attributes)["sigil.roles"].AsStringSlice(); !slices.Equal(got, tt.wantRoles) {
				t.Errorf("sigil.roles = %v, want %v", got, tt.wantRoles)
			}
			if got := attributes(evalSpan.Attributes)["sigil.decision"].AsString(); got == "" {
				t.Error("sigil.decision is missing")
			}
			if evalSpan.Parent.SpanID() != accessSpan.Parent.SpanID() {
				t.Error("the stage spans aren't siblings under the request span")
			}
			if !accessSpan.EndTime.Before(evalSpan.StartTime) && !accessSpan.EndTime.Equal(evalSpan.StartTime) {
				t.Error("the access stage didn't run before the deploy stage")
			}
		})
	}
}

// grantStrings renders grants as "role reason [ttl]" for comparison.
func grantStrings(grants []server.GrantResult) []string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		s := g.Role + " " + g.Reason
		if g.TTL != nil {
			s += " " + g.TTL.String()
		}
		out = append(out, s)
	}
	return out
}

// assertConflict checks a 409's conflict names both sides.
func assertConflict(t *testing.T, conflict *server.ConflictResult, herr *server.ErrorResponse) {
	t.Helper()
	if conflict == nil || len(conflict.Candidates) != 2 {
		t.Fatalf("conflict = %+v, want two candidates", conflict)
	}
	sides := []string{conflict.Candidates[0].Decision, conflict.Candidates[1].Decision}
	slices.Sort(sides)
	if !slices.Equal(sides, []string{"admin", "release_manager"}) {
		t.Errorf("conflict sides = %v, want admin and release_manager", sides)
	}
	if herr == nil || !strings.Contains(herr.Message, "can't stand together") {
		t.Errorf("error = %+v, want the conflict explained", herr)
	}
}

func attributes(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range kvs {
		out[kv.Key] = kv.Value
	}
	return out
}
