package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// checkoutWith is checkout's shipped policy with extra appended, so a test
// can make an evaluation fail in a way the shipped policies never do.
func checkoutWith(t *testing.T, extra string) map[string]string {
	t.Helper()
	return map[string]string{"checkout/alerts.sigil": readEmbedded(t, "checkout/alerts.sigil") + "\n" + extra + "\n"}
}

func TestRoute(t *testing.T) {
	tests := []struct {
		name        string
		team        string
		body        string
		wantDec     string
		wantReason  string
		wantTarget  string
		wantChannel string
		// wantWinner is the policy of the winning candidate, empty when no
		// rule fired and the trace is empty.
		wantWinner string
	}{
		{
			name: "a critical production alert pages the on-call", team: "checkout",
			body:    routeRequest("critical", "production", "1m"),
			wantDec: "page", wantReason: "critical_alert", wantTarget: "checkout-primary", wantWinner: "platform.paging",
		},
		{
			name: "a warning that fires past checkout's page_after pages", team: "checkout",
			body:    routeRequest("warning", "production", "12m"),
			wantDec: "page", wantReason: "sustained", wantTarget: "checkout-primary", wantWinner: "platform.paging",
		},
		{
			name: "payments pages sooner", team: "payments",
			body:    routeRequest("warning", "production", "6m"),
			wantDec: "page", wantReason: "sustained", wantTarget: "payments-primary", wantWinner: "platform.paging",
		},
		{
			name: "a fresh warning is posted to the team's channel", team: "checkout",
			body:    routeRequest("warning", "production", "2m"),
			wantDec: "notify", wantReason: "routine", wantChannel: "#checkout-alerts", wantWinner: "platform.routing",
		},
		{
			name: "staging is dropped", team: "checkout",
			body:    routeRequest("critical", "staging", "1h"),
			wantDec: "drop", wantReason: "not_production", wantWinner: "platform.routing",
		},
		{
			name: "a muted warning is dropped", team: "checkout",
			body:    routeRequestWith("CheckoutCanaryLatency", "warning", "2m", map[string]string{"env": "production"}),
			wantDec: "drop", wantReason: "muted", wantWinner: "platform.routing",
		},
		{
			name: "muting never silences a critical page", team: "checkout",
			body:    routeRequestWith("CheckoutCanaryLatency", "critical", "2m", map[string]string{"env": "production"}),
			wantDec: "page", wantReason: "critical_alert", wantTarget: "checkout-primary", wantWinner: "platform.paging",
		},
		{
			name: "the team's own rule routes an info alert", team: "checkout",
			body:    routeRequestWith("CheckoutPaymentsRetry", "info", "0s", map[string]string{"env": "production", "component": "payments"}),
			wantDec: "notify", wantReason: "routine", wantChannel: "#checkout-payments", wantWinner: "checkout.alerts",
		},
		{
			name: "an info alert no rule routes goes to the default channel", team: "checkout",
			body:    routeRequestWith("CheckoutPodRestarted", "info", "0s", map[string]string{"env": "production"}),
			wantDec: "notify", wantReason: "unrouted", wantChannel: "#alerts",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, loaded)
			rec := do(env.srv.Handler(), http.MethodPost, "/api/v1/teams/"+tt.team+"/route", tt.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
			}
			var resp server.RouteResponse
			decode(t, rec, &resp)
			if resp.Team != tt.team || resp.Policy != tt.team+".alerts" {
				t.Errorf("team, policy = %s, %s", resp.Team, resp.Policy)
			}
			if resp.Decision != tt.wantDec || resp.Reason != tt.wantReason || resp.Target != tt.wantTarget || resp.Channel != tt.wantChannel {
				t.Errorf("routed %s(%s) target %q channel %q, want %s(%s) target %q channel %q",
					resp.Decision, resp.Reason, resp.Target, resp.Channel, tt.wantDec, tt.wantReason, tt.wantTarget, tt.wantChannel)
			}
			if resp.Error != nil {
				t.Errorf("error = %+v on a decision", resp.Error)
			}
			switch {
			case tt.wantWinner == "" && len(resp.Trace) != 0:
				t.Errorf("trace = %+v, want none when the default decided", resp.Trace)
			case tt.wantWinner != "" && (len(resp.Trace) == 0 || !resp.Trace[0].Winner || resp.Trace[0].Policy != tt.wantWinner):
				t.Errorf("trace = %+v, want %s's winner first", resp.Trace, tt.wantWinner)
			case tt.wantWinner != "" && tt.wantWinner != "checkout.alerts" && !strings.Contains(resp.Trace[0].Location, "→"):
				t.Errorf("winner location %q, want the call chain into the platform", resp.Trace[0].Location)
			}

			notes := env.notes.list()
			want := dispatch.Notification{
				Team: tt.team, AlertName: notes[0].AlertName, Decision: tt.wantDec, Reason: tt.wantReason,
				Target: tt.wantTarget, Channel: tt.wantChannel,
			}
			if len(notes) != 1 || notes[0] != want {
				t.Errorf("notifications = %+v, want %+v", notes, want)
			}
		})
	}
}

func TestRouteRejects(t *testing.T) {
	tests := []struct {
		name       string
		team       string
		body       string
		wantStatus int
		wantMsg    string
	}{
		{name: "unknown team", team: "billing", body: routeRequest("critical", "production", "1m"), wantStatus: http.StatusNotFound, wantMsg: `team "billing" isn't in the team directory`},
		{name: "malformed json", team: "checkout", body: `{"alert":`, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "empty body", team: "checkout", body: " ", wantStatus: http.StatusBadRequest, wantMsg: "empty"},
		{name: "unknown field", team: "checkout", body: `{"alert":{"name":"A","severity":"critical","firing_for":"1m","team":"checkout"}}`, wantStatus: http.StatusBadRequest, wantMsg: "team"},
		{name: "numeric duration", team: "checkout", body: `{"alert":{"name":"A","severity":"critical","firing_for":60}}`, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "unparsable duration", team: "checkout", body: routeRequest("critical", "production", "a while"), wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "two json values", team: "checkout", body: routeRequest("critical", "production", "1m") + routeRequest("critical", "production", "1m"), wantStatus: http.StatusBadRequest, wantMsg: "more than one JSON value"},
		{name: "a body over the cap", team: "checkout", body: `{"alert":{"name":"` + strings.Repeat("x", 1<<20) + `"}}`, wantStatus: http.StatusRequestEntityTooLarge, wantMsg: "larger than 1048576 bytes"},
		{name: "no name", team: "checkout", body: routeRequestWith("", "critical", "1m", nil), wantStatus: http.StatusUnprocessableEntity, wantMsg: "alert.name is empty"},
		{name: "unknown severity", team: "checkout", body: routeRequest("page-me", "production", "1m"), wantStatus: http.StatusUnprocessableEntity, wantMsg: `alert.severity "page-me" isn't a severity`},
		{name: "severity in the wrong case", team: "checkout", body: routeRequest("Critical", "production", "1m"), wantStatus: http.StatusUnprocessableEntity, wantMsg: `alert.severity "Critical" isn't a severity`},
		{name: "negative firing time", team: "checkout", body: routeRequest("warning", "production", "-5m"), wantStatus: http.StatusUnprocessableEntity, wantMsg: "alert.firing_for is negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, loaded)
			rec := do(env.srv.Handler(), http.MethodPost, "/api/v1/teams/"+tt.team+"/route", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %.300s", rec.Code, tt.wantStatus, rec.Body)
			}
			var body server.ErrorEnvelope
			decode(t, rec, &body)
			if !strings.Contains(errorText(body.Error), tt.wantMsg) {
				t.Errorf("body %.300s doesn't mention %q", rec.Body, tt.wantMsg)
			}
			if body.Error == nil || len(body.Error.Advice) == 0 {
				t.Errorf("error %+v has no advice", body.Error)
			}
			if n := env.notes.list(); len(n) != 0 {
				t.Errorf("a refused alert was dispatched: %+v", n)
			}
		})
	}
}

// TestRouteFailures breaks checkout's policy in each way an evaluation can
// fail. A failed input assert is the caller's and answers 422; everything
// else is the policy's and answers 500. Every answer carries the fallback,
// which is dispatched, so the alert isn't lost, and counts as an evaluation
// error, not as a decision.
func TestRouteFailures(t *testing.T) {
	tests := []struct {
		name        string
		extra       string // appended to checkout's policy
		body        string
		wantStatus  int
		wantKind    string
		wantMessage string
		wantAssert  string
		wantSides   int // candidates in the conflict block
	}{
		{
			name:        "a failed input assert is the caller's",
			extra:       `assert("has_component", alert.labels["component"] != "")`,
			body:        routeRequest("warning", "production", "2m"),
			wantStatus:  http.StatusUnprocessableEntity,
			wantKind:    "assertion",
			wantMessage: "the alert fails checkout.alerts's asserts: has_component",
			wantAssert:  "has_component",
		},
		{
			name:        "a failed outcome assert is the policy's",
			extra:       `assert("no_pages", page not in outcome)`,
			body:        routeRequest("critical", "production", "2m"),
			wantStatus:  http.StatusInternalServerError,
			wantKind:    "assertion",
			wantMessage: "the outcome of checkout.alerts fails its asserts: no_pages",
			wantAssert:  "no_pages",
		},
		{
			name:        "a team page that ties with the platform's is a conflict",
			extra:       "when alert.severity == critical {\n  page(reason: critical_alert, target: \"checkout-secondary\")\n}",
			body:        routeRequest("critical", "production", "2m"),
			wantStatus:  http.StatusInternalServerError,
			wantKind:    "conflict",
			wantMessage: "checkout.alerts produced decisions that can't stand together",
			wantSides:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, envOptions{loaded: true, teamOverrides: checkoutWith(t, tt.extra)})
			h := env.srv.Handler()
			rec := do(h, http.MethodPost, "/api/v1/teams/checkout/route", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			var resp server.RouteResponse
			decode(t, rec, &resp)

			if resp.Decision != "notify" || resp.Reason != "unrouted" || resp.Channel != "#alerts" {
				t.Errorf("decision = %s(%s) to %q, want the fallback notify(unrouted) to #alerts", resp.Decision, resp.Reason, resp.Channel)
			}
			if resp.Error == nil || !strings.Contains(resp.Error.Message, tt.wantMessage) || len(resp.Error.Advice) == 0 {
				t.Errorf("error = %+v, want a message containing %q, with advice", resp.Error, tt.wantMessage)
			}
			if tt.wantAssert != "" && (len(resp.Asserts) != 1 || resp.Asserts[0].Reason != tt.wantAssert) {
				t.Errorf("asserts = %+v, want %s", resp.Asserts, tt.wantAssert)
			}
			if resp.Conflict != nil && len(resp.Conflict.Candidates) != tt.wantSides || resp.Conflict == nil && tt.wantSides > 0 {
				t.Errorf("conflict = %+v, want %d sides", resp.Conflict, tt.wantSides)
			}
			if notes := env.notes.list(); len(notes) != 1 || notes[0].Decision != "notify" || notes[0].Channel != "#alerts" {
				t.Errorf("notifications = %+v, want the fallback dispatched", notes)
			}

			body := do(h, http.MethodGet, "/metrics", "").Body.String()
			for _, want := range []string{
				`alertrouter_evaluation_errors_total{kind="` + tt.wantKind + `",team="checkout"} 1`,
				`alertrouter_alerts_routed_total{outcome="failed",team="checkout"} 1`,
				`alertrouter_notifications_total{decision="notify",destination="#alerts"} 1`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("/metrics doesn't contain %s", want)
				}
			}
			if strings.Contains(body, "alertrouter_decisions_total{") {
				t.Error("a failed evaluation counted its fallback as a decision")
			}
		})
	}
}

// TestRouteDeadline runs an evaluation out of time with a request whose
// deadline passed before it arrived, which the evaluation timeout can only
// shorten. The service didn't decide in time: 503 with the fallback, which
// is dispatched, counted as a timeout.
func TestRouteDeadline(t *testing.T) {
	env := newEnv(t, envOptions{loaded: true, evaluationTimeout: 50 * time.Millisecond})
	h := env.srv.Handler()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/teams/checkout/route", strings.NewReader(routeRequest("critical", "production", "1m")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rec.Code, rec.Body)
	}
	var resp server.RouteResponse
	decode(t, rec, &resp)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "wasn't decided within alertrouter's evaluation timeout") {
		t.Errorf("error = %+v, want the timeout explained", resp.Error)
	}
	if resp.Decision != "notify" || resp.Reason != "unrouted" || len(resp.Trace) != 0 {
		t.Errorf("answer = %+v, want the fallback without a trace", resp)
	}
	if notes := env.notes.list(); len(notes) != 1 || notes[0].Reason != "unrouted" {
		t.Errorf("notifications = %+v, want the fallback dispatched", notes)
	}
	metrics := do(h, http.MethodGet, "/metrics", "").Body.String()
	if want := `alertrouter_evaluation_errors_total{kind="timeout",team="checkout"} 1`; !strings.Contains(metrics, want) {
		t.Errorf("/metrics doesn't contain %s", want)
	}
}

// TestRouteCanceled cancels a request before it arrives. The client is gone:
// 499 with no body, nothing dispatched, no evaluation error or decision
// counted, and no span marked failed.
func TestRouteCanceled(t *testing.T) {
	env := newEnv(t, loaded)
	h := env.srv.Handler()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/teams/checkout/route", strings.NewReader(routeRequest("critical", "production", "1m")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != server.StatusClientClosedRequest {
		t.Fatalf("status = %d, want 499; body %s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body %s, want none: no one is left to read it", rec.Body)
	}
	if n := env.notes.list(); len(n) != 0 {
		t.Errorf("a canceled request dispatched %+v", n)
	}
	metrics := do(h, http.MethodGet, "/metrics", "").Body.String()
	for _, unwanted := range []string{"alertrouter_evaluation_errors_total{", "alertrouter_decisions_total{", "alertrouter_alerts_routed_total{"} {
		if strings.Contains(metrics, unwanted) {
			t.Errorf("a canceled request counted in %s", unwanted)
		}
	}
	for _, s := range env.spans.GetSpans() {
		if s.Status.Code == codes.Error {
			t.Errorf("span %s is marked failed: %v", s.Name, s.Status)
		}
	}
}

// TestRouteNotifierFailure checks that a notifier's error doesn't change the
// decision: the answer is still the policy's, with the dispatch error
// attached, and nothing is counted as dispatched.
func TestRouteNotifierFailure(t *testing.T) {
	env := newEnv(t, envOptions{loaded: true, notifyErr: humane.New("pager unreachable", "check the pager's status page")})
	h := env.srv.Handler()
	rec := do(h, http.MethodPost, "/api/v1/teams/checkout/route", routeRequest("critical", "production", "1m"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	var resp server.RouteResponse
	decode(t, rec, &resp)
	if resp.Decision != "page" || resp.Target != "checkout-primary" {
		t.Errorf("decision = %+v, want the policy's page", resp)
	}
	if resp.Error == nil || !strings.Contains(errorText(resp.Error), "dispatching the page to checkout-primary failed") {
		t.Errorf("error = %+v, want the dispatch failure", resp.Error)
	}
	metrics := do(h, http.MethodGet, "/metrics", "").Body.String()
	if strings.Contains(metrics, "alertrouter_notifications_total{") {
		t.Error("a notification that failed counted as dispatched")
	}
	if want := `alertrouter_alerts_routed_total{outcome="routed",team="checkout"} 1`; !strings.Contains(metrics, want) {
		t.Errorf("/metrics doesn't contain %s", want)
	}
}

func TestRouteMetrics(t *testing.T) {
	env := newEnv(t, loaded)
	h := env.srv.Handler()
	do(h, http.MethodPost, "/api/v1/teams/checkout/route", routeRequest("critical", "production", "1m"))
	do(h, http.MethodPost, "/api/v1/teams/checkout/route", routeRequest("warning", "staging", "1m"))
	do(h, http.MethodPost, "/api/v1/teams/payments/route", routeRequest("warning", "production", "1m"))

	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	for _, want := range []string{
		`alertrouter_alerts_received_total{status="firing"} 3`,
		`alertrouter_alerts_routed_total{outcome="routed",team="checkout"} 2`,
		`alertrouter_alerts_routed_total{outcome="routed",team="payments"} 1`,
		`alertrouter_decisions_total{decision="page",policy="checkout.alerts",reason="critical_alert",team="checkout"} 1`,
		`alertrouter_decisions_total{decision="drop",policy="checkout.alerts",reason="not_production",team="checkout"} 1`,
		`alertrouter_decisions_total{decision="notify",policy="payments.alerts",reason="routine",team="payments"} 1`,
		`alertrouter_evaluation_duration_seconds_count{team="checkout"} 2`,
		`alertrouter_notifications_total{decision="page",destination="checkout-primary"} 1`,
		`alertrouter_notifications_total{decision="drop",destination="-"} 1`,
		`alertrouter_notifications_total{decision="notify",destination="#payments-alerts"} 1`,
		`alertrouter_policy_reloads_total{result="success"} 1`,
		`alertrouter_policy_reloads_total{result="failure"} 0`,
		`alertrouter_policy_last_reload_successful 1`,
		`alertrouter_policy_last_reload_timestamp_seconds `,
		`alertrouter_policy_loaded_info{fingerprint="`,
		`alertrouter_requests_total{code="200",method="POST",route="/api/v1/teams/:team/route"} 3`,
		`go_goroutines`,
		`process_cpu_seconds_total`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics doesn't contain %s", want)
		}
	}
}

// TestRouteSpan checks the alertrouter.route span of each alert: under the
// request's span, with the alert, the team and the decision, one event per
// candidate, and an error status only when the evaluation failed.
func TestRouteSpan(t *testing.T) {
	tests := []struct {
		name        string
		env         envOptions
		body        string
		wantDec     string
		wantOutcome string
		wantEvents  bool
		wantError   bool
	}{
		{name: "a page", env: loaded, body: routeRequest("critical", "production", "1m"), wantDec: "page", wantOutcome: "routed", wantEvents: true},
		{name: "the default", env: loaded, body: routeRequestWith("CheckoutPodRestarted", "info", "0s", map[string]string{"env": "production"}), wantDec: "notify", wantOutcome: "routed"},
		{
			name:    "a conflict",
			body:    routeRequest("critical", "production", "1m"),
			env:     envOptions{loaded: true, teamOverrides: checkoutWith(t, "when true {\n  page(reason: critical_alert, target: \"someone-else\")\n}")},
			wantDec: "notify", wantOutcome: "failed", wantEvents: true, wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, tt.env)
			env.spans.Reset()
			do(env.srv.Handler(), http.MethodPost, "/api/v1/teams/checkout/route", tt.body)
			spans := env.spans.GetSpans()

			route := findSpans(spans, "alertrouter.route")
			if len(route) != 1 {
				t.Fatalf("alertrouter.route spans = %d, want 1", len(route))
			}
			attrs := attributes(route[0].Attributes)
			for key, want := range map[attribute.Key]string{
				"alertrouter.team":    "checkout",
				"alertrouter.outcome": tt.wantOutcome,
				"sigil.policy":        "checkout.alerts",
				"sigil.decision":      tt.wantDec,
			} {
				if got := attrs[key].AsString(); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			for _, key := range []attribute.Key{"alert.name", "alert.severity", "sigil.reason"} {
				if attrs[key].AsString() == "" {
					t.Errorf("%s is missing", key)
				}
			}
			if got := countEvents(route[0], "sigil.candidate"); (got > 0) != tt.wantEvents || int64(got) != attrs["sigil.candidates"].AsInt64() {
				t.Errorf("candidate events = %d, sigil.candidates = %d, want some %v", got, attrs["sigil.candidates"].AsInt64(), tt.wantEvents)
			}
			if (route[0].Status.Code == codes.Error) != tt.wantError {
				t.Errorf("status = %v, want error %v", route[0].Status, tt.wantError)
			}
			request := findSpans(spans, "POST /api/v1/teams/:team/route")
			if len(request) != 1 || route[0].Parent.SpanID() != request[0].SpanContext.SpanID() {
				t.Error("the route span isn't a child of the request's span")
			}
		})
	}
}

// TestRouteStoreDirectoryMismatch builds a store that serves fewer teams than
// the directory names, a wiring mistake, and checks that such a team's alert
// still goes out with the fallback, reported as failed with a 500 that says
// how to fix the wiring, and counts no evaluation error, since no policy ran.
func TestRouteStoreDirectoryMismatch(t *testing.T) {
	st := store.NewRouting(store.WithTeams("checkout"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	metrics := telemetry.NewMetrics()
	notes := &notifications{}
	srv, herr := server.New(
		server.WithStore(st),
		server.WithDirectory(teams.Default()),
		server.WithMetrics(metrics),
		server.WithNotifier(dispatch.NotifierFunc(func(ctx context.Context, n dispatch.Notification) humane.Error {
			notes.record(ctx, n)
			return nil
		})),
	)
	if herr != nil {
		t.Fatal(herr)
	}
	h := srv.Handler()

	rec := do(h, http.MethodPost, "/api/v1/teams/payments/route", routeRequest("critical", "production", "1m"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body %s", rec.Code, rec.Body)
	}
	var resp server.RouteResponse
	decode(t, rec, &resp)
	if resp.Decision != "notify" || resp.Channel != "#alerts" || resp.Policy != "payments.alerts" {
		t.Errorf("answer = %+v, want the fallback for payments.alerts", resp)
	}
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "the loaded bundle has no policy for it") {
		t.Errorf("error = %+v, want the mismatch explained", resp.Error)
	}
	if n := notes.list(); len(n) != 1 || n[0].Channel != "#alerts" {
		t.Errorf("notifications = %+v, want the fallback dispatched", n)
	}
	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	if strings.Contains(body, "alertrouter_evaluation_errors_total{") {
		t.Error("a mismatch counted as an evaluation error")
	}
	if want := `alertrouter_alerts_routed_total{outcome="failed",team="payments"} 1`; !strings.Contains(body, want) {
		t.Errorf("/metrics doesn't contain %s", want)
	}
}
