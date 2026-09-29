package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

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

production(approvers: ["checkout-leads"], tiers: [standard])
`

// checkoutWith is checkout's policy with extra appended, so a test can make
// the deploy stage fail in a way the shipped policies never do.
func checkoutWith(extra string) map[string]string {
	return map[string]string{"checkout/production.sigil": `policy checkout.production: DeployApproval@1

use deploy.guardrails
use deploy.production

assert("named_actor", actor.name != "")

guardrails()

production(approvers: ["checkout-leads"], tiers: [standard])

` + extra}
}

// checkoutOwner edits the default request into checkout's owner shipping a
// release that soaked for a day, which checkout's policy sends to review.
func checkoutOwner(r, actor map[string]any) {
	actor["groups"] = []string{"checkout"}
	r["service"].(map[string]any)["owners"] = []string{"checkout"}
	r["release"] = map[string]any{"soak": "24h", "hotfix": false}
}

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
			wantStatus: http.StatusInternalServerError,
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
			wantStatus: http.StatusInternalServerError,
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

// TestDeployStageFailures breaks checkout's policy in each way a deploy
// evaluation can fail after the access stage passed. A failed input assert
// is the caller's and answers 422; everything else is the policy's and
// answers 500. Every answer carries the fallback and the grants. The
// request is checkout's owner shipping a hotfix, which checkout's policy
// sends to review, unless edit changes it.
func TestDeployStageFailures(t *testing.T) {
	tests := []struct {
		name        string
		extra       string // appended to checkout's policy
		edit        func(r, actor map[string]any)
		wantStatus  int
		wantKind    string
		wantMessage string
		wantAssert  string
		wantTrace   bool // whether the trace holds the candidates the failure saw
		wantSides   int  // candidates in the conflict block
	}{
		{
			name:        "a failed input assert is the caller's",
			extra:       `assert("no_hotfix", not release.hotfix)`,
			wantStatus:  http.StatusUnprocessableEntity,
			wantKind:    "assertion",
			wantMessage: "the request fails checkout.production's asserts: no_hotfix",
			wantAssert:  "no_hotfix",
		},
		{
			name:        "a failed outcome assert is the policy's",
			extra:       `assert("no_reviews", review not in outcome)`,
			wantStatus:  http.StatusInternalServerError,
			wantKind:    "assertion",
			wantMessage: "the outcome of checkout.production fails its asserts: no_reviews",
			wantAssert:  "no_reviews",
			wantTrace:   true,
		},
		{
			// A critical service is outside checkout's tiers and the owner
			// isn't a release manager, so no rule fires and the outcome is
			// the default. The trace is as empty as after a failed input
			// assert, and the answer is still the policy's 500.
			name:  "an outcome assert that fails with nothing fired is the policy's",
			extra: `assert("decided_by_a_rule", deny.no_rule_matched not in outcome)`,
			edit: func(r, _ map[string]any) {
				r["service"].(map[string]any)["tier"] = "critical"
			},
			wantStatus:  http.StatusInternalServerError,
			wantKind:    "assertion",
			wantMessage: "the outcome of checkout.production fails its asserts: decided_by_a_rule",
			wantAssert:  "decided_by_a_rule",
		},
		{
			name:        "a runtime error is the policy's",
			extra:       "when actor.regions[9] == \"eu\" {\n  deny(reason: not_eligible)\n}",
			wantStatus:  http.StatusInternalServerError,
			wantKind:    "runtime",
			wantMessage: "checkout.production can't be evaluated against this request",
		},
		{
			name:        "two reviews with different approvers are the policy's conflict",
			extra:       "when true {\n  review(reason: service_owner, approvers: [\"security-leads\"])\n}",
			wantStatus:  http.StatusInternalServerError,
			wantKind:    "conflict",
			wantMessage: "checkout.production produced decisions that can't stand together",
			wantTrace:   true,
			wantSides:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{deployLoaded: true, accessLoaded: true, teamOverrides: checkoutWith(tt.extra + "\n")})
			h := env.srv.Handler()
			rec := do(h, http.MethodPost, "/api/v1/teams/checkout/deployments", deployRequest(func(r, actor map[string]any) {
				checkoutOwner(r, actor)
				r["release"].(map[string]any)["hotfix"] = true
				if tt.edit != nil {
					tt.edit(r, actor)
				}
			}))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var resp server.DecisionResponse
			decode(t, rec, &resp)

			if resp.Decision != "deny" || resp.Reason != "no_rule_matched" {
				t.Errorf("decision = %s(reason: %s), want the fallback deny(reason: no_rule_matched)", resp.Decision, resp.Reason)
			}
			if got := grantStrings(resp.Access.Grants); !slices.Equal(got, []string{"reader team_member", "deployer team_member 8h"}) {
				t.Errorf("grants = %v, want the team member's", got)
			}
			if resp.Error == nil || !strings.Contains(resp.Error.Message, tt.wantMessage) {
				t.Errorf("error = %+v, want a message containing %q", resp.Error, tt.wantMessage)
			}
			if tt.wantAssert != "" && (len(resp.Asserts) != 1 || resp.Asserts[0].Reason != tt.wantAssert) {
				t.Errorf("asserts = %+v, want %s", resp.Asserts, tt.wantAssert)
			}
			if (len(resp.Trace) > 0) != tt.wantTrace {
				t.Errorf("trace = %+v, want candidates %v", resp.Trace, tt.wantTrace)
			}
			if resp.Conflict != nil && len(resp.Conflict.Candidates) != tt.wantSides || resp.Conflict == nil && tt.wantSides > 0 {
				t.Errorf("conflict = %+v, want %d sides", resp.Conflict, tt.wantSides)
			}

			body := do(h, http.MethodGet, "/metrics", "").Body.String()
			if want := `deploygate_evaluation_errors_total{kind="` + tt.wantKind + `",stage="deploy",team="checkout"} 1`; !strings.Contains(body, want) {
				t.Errorf("/metrics doesn't contain %s", want)
			}
		})
	}
}

// TestCanceledRequest cancels requests before and during the evaluation.
// The client is gone either way, so it answers 499 with no body, counts no
// evaluation error and no decision, and marks no span as failed: a client
// that gives up says nothing about the policy or the service.
func TestCanceledRequest(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		// during cancels the request shortly after the deploy stage
		// starts, while a policy that's slow for the input runs, instead
		// of before the request is sent.
		during bool
	}{
		{name: "deployment canceled before the access stage", path: "/api/v1/teams/payments/deployments", body: deployRequest(nil)},
		{name: "access grants canceled before the evaluation", path: "/api/v1/access/grants", body: accessRequest("ada", "", "payments", "production", "payments")},
		{name: "deployment canceled during the deploy stage", path: "/api/v1/teams/payments/deployments", body: deployRequest(slowToDecide), during: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := loaded
			if tt.during {
				opts.onSpanStart = func(name string) {
					if name == "deploygate.evaluate" {
						time.AfterFunc(10*time.Millisecond, cancel)
					}
				}
			} else {
				cancel()
			}
			env := newEnv(t, opts)
			h := env.srv.Handler()
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != server.StatusClientClosedRequest {
				t.Fatalf("status = %d, want 499; body %s", rec.Code, rec.Body)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("body %s, want none: no one is left to read it", rec.Body)
			}
			metrics := do(h, http.MethodGet, "/metrics", "").Body.String()
			for _, unwanted := range []string{"deploygate_evaluation_errors_total{", "deploygate_decisions_total{"} {
				if strings.Contains(metrics, unwanted) {
					t.Errorf("a canceled request counted in %s", unwanted)
				}
			}
			if want := `deploygate_requests_total{code="499",method="POST"`; !strings.Contains(metrics, want) {
				t.Errorf("/metrics doesn't contain %s", want)
			}
			spans := env.spans.GetSpans()
			for _, s := range spans {
				if s.Status.Code == codes.Error {
					t.Errorf("span %s is marked failed: %v", s.Name, s.Status)
				}
			}
			if evalSpan, ok := findSpan(spans, "deploygate.evaluate"); tt.during && (!ok || countEvents(evalSpan, "exception") != 1) {
				t.Errorf("deploygate.evaluate = %+v, present %v; want the deploy stage stopped by the cancellation", evalSpan.Events, ok)
			}
		})
	}
}

// TestEvaluationDeadline runs each stage out of time. The deploy stage gets
// a policy that's slow for its input and a short evaluation timeout; the
// access stage gets a request whose deadline passed before it arrived,
// which the evaluation timeout can only shorten. Either way the service
// didn't decide in time: 503 with the fallback, counted as a timeout of the
// stage that ran out.
func TestEvaluationDeadline(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		body      string
		expired   bool // whether the request's own deadline has passed
		wantStage string
		wantTeam  string
	}{
		{name: "the deploy stage", path: "/api/v1/teams/payments/deployments", body: deployRequest(slowToDecide), wantStage: "deploy", wantTeam: "payments"},
		{name: "the access stage of a deployment", path: "/api/v1/teams/checkout/deployments", body: deployRequest(nil), expired: true, wantStage: "access", wantTeam: "checkout"},
		{name: "the access stage of a grants request", path: "/api/v1/access/grants", body: accessRequest("ada", "", "payments", "production", "payments"), expired: true, wantStage: "access", wantTeam: "payments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{deployLoaded: true, accessLoaded: true, evaluationTimeout: 50 * time.Millisecond})
			h := env.srv.Handler()
			ctx := context.Background()
			if tt.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			start := time.Now()
			h.ServeHTTP(rec, req)
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("the request took %v, far past the 50ms evaluation timeout", took)
			}

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body %s", rec.Code, rec.Body)
			}
			var resp struct {
				server.DecisionResponse
				Grants []server.GrantResult `json:"grants"`
			}
			decode(t, rec, &resp)
			if resp.Error == nil || !strings.Contains(resp.Error.Message, "wasn't decided within deploygate's evaluation timeout") {
				t.Errorf("error = %+v, want the timeout explained", resp.Error)
			}
			if len(resp.Trace) != 0 || len(resp.Grants) != 0 {
				t.Errorf("trace, grants = %+v, %+v; want none from an evaluation that ran out of time", resp.Trace, resp.Grants)
			}
			if strings.Contains(tt.path, "deployments") && (resp.Decision != "deny" || resp.Reason != "no_rule_matched") {
				t.Errorf("decision = %s(reason: %s), want the fallback deny(reason: no_rule_matched)", resp.Decision, resp.Reason)
			}

			metrics := do(h, http.MethodGet, "/metrics", "").Body.String()
			if want := `deploygate_evaluation_errors_total{kind="timeout",stage="` + tt.wantStage + `",team="` + tt.wantTeam + `"} 1`; !strings.Contains(metrics, want) {
				t.Errorf("/metrics doesn't contain %s", want)
			}
			if strings.Contains(metrics, "deploygate_decisions_total{") {
				t.Error("an evaluation that ran out of time counted its fallback as a decision")
			}
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
		{name: "undeclared tier", team: "payments", edit: func(r, _ map[string]any) { r["service"].(map[string]any)["tier"] = "critcal" }, wantStatus: http.StatusBadRequest, wantMsg: `service.tier "critcal" isn't a tier`},
		{name: "missing tier", team: "payments", edit: func(r, _ map[string]any) { delete(r["service"].(map[string]any), "tier") }, wantStatus: http.StatusBadRequest, wantMsg: `service.tier "" isn't a tier`},
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
		`deploygate_policy_last_reload_timestamp_seconds{kind="DeployApproval"} `,
		`deploygate_policy_last_reload_timestamp_seconds{kind="AccessGrant"} `,
		`deploygate_policy_last_reload_successful{kind="DeployApproval"} 1`,
		`deploygate_policy_last_reload_successful{kind="AccessGrant"} 1`,
		`deploygate_requests_total{code="202",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
		`deploygate_requests_total{code="500",method="POST",url="/api/v1/teams/:team/deployments"} 1`,
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

// TestFailedDeployEvaluationMetrics checks that a deploy evaluation that
// fails counts as an evaluation error of the deploy stage and not as the
// fallback decision it answers with, so a real deny(reason: no_rule_matched) and a
// failure stay apart.
func TestFailedDeployEvaluationMetrics(t *testing.T) {
	env := newEnv(t, envOptions{deployLoaded: true, accessLoaded: true, teamOverrides: map[string]string{"checkout/production.sigil": assertedCheckout}})
	h := env.srv.Handler()
	failing := deployRequest(func(r, actor map[string]any) {
		actor["groups"] = []string{"checkout"}
		r["service"].(map[string]any)["name"] = ""
	})
	if rec := do(h, http.MethodPost, "/api/v1/teams/checkout/deployments", failing); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rec.Code, rec.Body)
	}

	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	for _, want := range []string{
		`deploygate_evaluation_errors_total{kind="assertion",stage="deploy",team="checkout"} 1`,
		`deploygate_evaluation_duration_seconds_count{team="checkout"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
	if strings.Contains(body, "deploygate_decisions_total{") {
		t.Error("a failed deploy evaluation counted its fallback as a decision")
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
		// wantServerError is whether the HTTP server span is an error,
		// which otelgin sets for a 5xx only: a policy's failure is one, the
		// caller's failed input assert isn't.
		wantServerError bool
	}{
		{name: "team member", wantGrants: 2, wantRoles: []string{"deployer"}, wantDeploy: true},
		{name: "admin", edit: func(_, actor map[string]any) { actor["clearance"] = "admin" }, wantGrants: 3, wantRoles: []string{"deployer", "release_manager"}, wantDeploy: true},
		{name: "outsider", edit: func(_, actor map[string]any) { actor["groups"] = []string{"marketing"} }, wantRoles: []string{}, wantDeploy: true},
		{name: "conflict", edit: func(_, actor map[string]any) { actor["groups"] = []string{"platform", "break-glass"} }, wantError: true, wantServerError: true},
		{name: "unnamed actor", edit: func(_, actor map[string]any) { actor["name"] = "" }, wantError: true},
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
			serverSpan, ok := findSpan(spans, "POST /api/v1/teams/:team/deployments")
			if !ok {
				t.Fatalf("no HTTP server span among %d spans", len(spans))
			}
			if (serverSpan.Status.Code.String() == "Error") != tt.wantServerError {
				t.Errorf("server span status = %v, want error %v", serverSpan.Status, tt.wantServerError)
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

// assertConflict checks a conflict's answer names both sides.
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

// slowToDecide makes the deploy policy's region check take far longer than
// any test waits. `cleared` checks that every region the service lists is
// one the actor holds; the service lists "z" 50,000 times, and the actor
// holds 49,999 other regions before "z", so each lookup walks the whole
// list: over a billion comparisons, from a body well under the 1 MiB cap.
func slowToDecide(r, actor map[string]any) {
	const n = 50_000
	r["service"].(map[string]any)["labels"].(map[string]string)["regions"] = strings.TrimSuffix(strings.Repeat("z,", n), ",")
	regions := make([]string, n)
	for i := range n - 1 {
		regions[i] = "y"
	}
	regions[n-1] = "z"
	actor["regions"] = regions
}
