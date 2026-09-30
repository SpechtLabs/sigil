package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
)

// alert builds one webhook alert of team, severity and env that started
// firing firingFor before the test clock's now. An empty team, severity or
// env leaves the label out.
func alert(name, team, severity, env string, firingFor time.Duration) map[string]any {
	labels := map[string]string{"alertname": name}
	for key, value := range map[string]string{"team": team, "severity": severity, "env": env} {
		if value != "" {
			labels[key] = value
		}
	}
	return map[string]any{
		"status":       "firing",
		"labels":       labels,
		"annotations":  map[string]string{"summary": name + " is firing"},
		"startsAt":     now.Add(-firingFor).Format(time.RFC3339),
		"endsAt":       "0001-01-01T00:00:00Z",
		"generatorURL": "http://prometheus/graph",
		"fingerprint":  "fp-" + strings.ToLower(name),
	}
}

// resolved marks a webhook alert as resolved.
func resolved(a map[string]any) map[string]any {
	return withStatus(a, "resolved")
}

// withStatus sets a webhook alert's status.
func withStatus(a map[string]any, status string) map[string]any {
	a["status"] = status
	return a
}

// webhook renders an Alertmanager webhook, version 4, carrying alerts.
func webhook(alerts ...map[string]any) string {
	if alerts == nil {
		alerts = []map[string]any{}
	}
	out, _ := json.Marshal(map[string]any{
		"version":           "4",
		"groupKey":          `{}:{alertname="test"}`,
		"truncatedAlerts":   0,
		"status":            "firing",
		"receiver":          "alertrouter",
		"groupLabels":       map[string]string{},
		"commonLabels":      map[string]string{},
		"commonAnnotations": map[string]string{},
		"externalURL":       "http://alertmanager:9093",
		"alerts":            alerts,
	})
	return string(out)
}

// TestWebhook routes one batch with an alert of every status and checks each
// result, in the webhook's order, and that every firing alert was
// dispatched.
func TestWebhook(t *testing.T) {
	env := newEnv(t, loaded)
	h := env.srv.Handler()
	rec := do(h, http.MethodPost, "/api/v1/alerts", webhook(
		alert("CheckoutErrorRate", "checkout", "critical", "production", 2*time.Minute),
		alert("PaymentsLatencyHigh", "payments", "warning", "production", 6*time.Minute),
		alert("CheckoutLatencyHigh", "checkout", "warning", "staging", time.Hour),
		resolved(alert("CheckoutDiskFull", "checkout", "critical", "production", time.Hour)),
		alert("BillingDown", "billing", "critical", "production", time.Minute),
		alert("NobodysAlert", "", "critical", "production", time.Minute),
		alert("CheckoutNoSeverity", "checkout", "", "production", time.Minute),
		alert("CheckoutLoud", "checkout", "SEV1", "production", time.Minute),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	var resp server.WebhookResponse
	decode(t, rec, &resp)
	if resp.Received != 8 || resp.Routed != 3 || len(resp.Results) != 8 {
		t.Fatalf("received %d, routed %d, results %d; want 8, 3, 8", resp.Received, resp.Routed, len(resp.Results))
	}

	want := []struct {
		status, team, policy, decision, reason, target, channel, errText string
	}{
		{status: "routed", team: "checkout", policy: "checkout.alerts", decision: "page", reason: "critical_alert", target: "checkout-primary"},
		{status: "routed", team: "payments", policy: "payments.alerts", decision: "page", reason: "sustained", target: "payments-primary"},
		{status: "routed", team: "checkout", policy: "checkout.alerts", decision: "drop", reason: "not_production"},
		{status: "resolved"},
		{status: "unowned", decision: "notify", reason: "unrouted", channel: "#alerts", errText: `team "billing" isn't in the team directory`},
		{status: "unowned", decision: "notify", reason: "unrouted", channel: "#alerts", errText: "the alert has no team label"},
		{status: "invalid", team: "checkout", policy: "checkout.alerts", decision: "notify", reason: "unrouted", channel: "#alerts", errText: "no severity label"},
		{status: "invalid", team: "checkout", policy: "checkout.alerts", decision: "notify", reason: "unrouted", channel: "#alerts", errText: `"SEV1"`},
	}
	for i, w := range want {
		got := resp.Results[i]
		if got.Status != w.status || got.Fingerprint == "" || got.AlertName == "" {
			t.Errorf("result %d = %+v, want status %s with its fingerprint and alertname", i, got, w.status)
			continue
		}
		if !strings.Contains(got.Error, w.errText) || (w.errText == "") != (got.Error == "") {
			t.Errorf("result %d error = %q, want %q", i, got.Error, w.errText)
		}
		if w.status == "resolved" {
			if got.RouteResponse != nil {
				t.Errorf("result %d of a resolved alert carries a decision: %+v", i, got.RouteResponse)
			}
			continue
		}
		r := got.RouteResponse
		if r == nil || r.Team != w.team || r.Policy != w.policy || r.Decision != w.decision || r.Reason != w.reason || r.Target != w.target || r.Channel != w.channel {
			t.Errorf("result %d = %+v, want %+v", i, r, w)
		}
	}

	notes := env.notes.list()
	if len(notes) != 7 {
		t.Fatalf("notifications = %d, want one per firing alert, 7", len(notes))
	}
	if notes[0].Fingerprint != "fp-checkouterrorrate" || notes[0].AlertName != "CheckoutErrorRate" || notes[0].Target != "checkout-primary" {
		t.Errorf("first notification = %+v", notes[0])
	}

	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	for _, m := range []string{
		`alertrouter_alerts_received_total{status="firing"} 7`,
		`alertrouter_alerts_received_total{status="resolved"} 1`,
		`alertrouter_alerts_routed_total{outcome="routed",team="checkout"} 2`,
		`alertrouter_alerts_routed_total{outcome="routed",team="payments"} 1`,
		`alertrouter_alerts_routed_total{outcome="unowned",team="-"} 2`,
		`alertrouter_alerts_routed_total{outcome="invalid",team="checkout"} 2`,
		`alertrouter_webhook_batch_size_count 1`,
		`alertrouter_webhook_batch_size_sum 8`,
		`alertrouter_notifications_total{decision="notify",destination="#alerts"} 4`,
		`alertrouter_requests_total{code="200",method="POST",route="/api/v1/alerts"} 1`,
	} {
		if !strings.Contains(body, m) {
			t.Errorf("/metrics doesn't contain %s", m)
		}
	}
	if strings.Contains(body, "billing") {
		t.Error("an unowned alert's team label became a label value")
	}

	// One route span per firing alert, none for the resolved one.
	if got := len(findSpans(env.spans.GetSpans(), "alertrouter.route")); got != 7 {
		t.Errorf("alertrouter.route spans = %d, want 7", got)
	}
}

func TestWebhookRejects(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantMsg    string
	}{
		{name: "malformed json", body: `{"version":`, wantStatus: http.StatusBadRequest, wantMsg: "isn't a valid request"},
		{name: "empty body", body: "", wantStatus: http.StatusBadRequest, wantMsg: "the request body is empty"},
		{name: "wrong version", body: strings.Replace(webhook(), `"version":"4"`, `"version":"3"`, 1), wantStatus: http.StatusBadRequest, wantMsg: `version "3"`},
		{name: "unknown alert status", body: webhook(withStatus(alert("A", "checkout", "critical", "production", time.Minute), "pending")), wantStatus: http.StatusBadRequest, wantMsg: `"pending"`},
		{name: "too many alerts", body: webhook(manyAlerts(1001)...), wantStatus: http.StatusBadRequest, wantMsg: "more than the 1000"},
		{name: "a body over the cap", body: `{"version":"4","receiver":"` + strings.Repeat("x", 1<<20) + `"}`, wantStatus: http.StatusRequestEntityTooLarge, wantMsg: "larger than 1048576 bytes"},
		{name: "two json values", body: webhook() + webhook(), wantStatus: http.StatusBadRequest, wantMsg: "more than one JSON value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t, loaded)
			rec := do(env.srv.Handler(), http.MethodPost, "/api/v1/alerts", tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %.300s", rec.Code, tt.wantStatus, rec.Body)
			}
			var body server.ErrorEnvelope
			decode(t, rec, &body)
			if !strings.Contains(errorText(body.Error), tt.wantMsg) {
				t.Errorf("body %.300s doesn't mention %q", rec.Body, tt.wantMsg)
			}
			if n := env.notes.list(); len(n) != 0 {
				t.Errorf("a refused webhook dispatched %d notifications", len(n))
			}
		})
	}
}

// TestWebhookLenient checks that the webhook accepts fields alertrouter
// doesn't read, since Alertmanager's payload may grow, and an empty batch.
func TestWebhookLenient(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "an unknown top-level field", body: strings.Replace(webhook(alert("A", "checkout", "info", "production", 0)), `"version":"4"`, `"version":"4","future":true`, 1), want: 1},
		{name: "an empty batch", body: webhook(), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newEnv(t, loaded).srv.Handler(), http.MethodPost, "/api/v1/alerts", tt.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
			}
			var resp server.WebhookResponse
			decode(t, rec, &resp)
			if resp.Received != tt.want || len(resp.Results) != tt.want {
				t.Errorf("received %d with %d results, want %d", resp.Received, len(resp.Results), tt.want)
			}
		})
	}
}

// TestWebhookFailedAlert checks that an alert whose evaluation fails is
// routed to the fallback and reported as failed, while the rest of the batch
// is routed by its policy, and the webhook still answers 200 so Alertmanager
// doesn't retry the batch.
func TestWebhookFailedAlert(t *testing.T) {
	env := newEnv(t, envOptions{loaded: true, teamOverrides: checkoutWith(t,
		"when alert.severity == critical {\n  page(reason: critical_alert, target: \"checkout-secondary\")\n}")})
	h := env.srv.Handler()
	rec := do(h, http.MethodPost, "/api/v1/alerts", webhook(
		alert("CheckoutErrorRate", "checkout", "critical", "production", time.Minute),
		alert("PaymentsErrorRate", "payments", "critical", "production", time.Minute),
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	var resp server.WebhookResponse
	decode(t, rec, &resp)
	if resp.Routed != 1 {
		t.Errorf("routed = %d, want 1", resp.Routed)
	}
	failed := resp.Results[0]
	if failed.Status != "failed" || !strings.Contains(failed.Error, "can't stand together") {
		t.Errorf("first result = %+v, want a failed conflict", failed)
	}
	if failed.RouteResponse == nil || failed.Decision != "notify" || failed.Channel != "#alerts" || failed.Conflict == nil {
		t.Errorf("first result = %+v, want the fallback with the conflict", failed.RouteResponse)
	}
	if ok := resp.Results[1]; ok.Status != "routed" || ok.Target != "payments-primary" {
		t.Errorf("second result = %+v, want payments' page", ok)
	}
	if notes := env.notes.list(); len(notes) != 2 {
		t.Errorf("notifications = %+v, want both alerts dispatched", notes)
	}
	body := do(h, http.MethodGet, "/metrics", "").Body.String()
	if want := `alertrouter_alerts_routed_total{outcome="failed",team="checkout"} 1`; !strings.Contains(body, want) {
		t.Errorf("/metrics doesn't contain %s", want)
	}
}

// TestWebhookFiringTime checks that an alert's firing time is measured from
// its startsAt to the server clock's now: checkout pages a warning after 10
// minutes, and a startsAt in the future counts as zero.
func TestWebhookFiringTime(t *testing.T) {
	tests := []struct {
		name      string
		firingFor time.Duration
		want      string
	}{
		{name: "just short of page_after", firingFor: 9 * time.Minute, want: "notify"},
		{name: "at page_after", firingFor: 10 * time.Minute, want: "page"},
		{name: "a clock ahead of ours", firingFor: -time.Hour, want: "notify"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newEnv(t, loaded).srv.Handler(), http.MethodPost, "/api/v1/alerts",
				webhook(alert("CheckoutLatencyHigh", "checkout", "warning", "production", tt.firingFor)))
			var resp server.WebhookResponse
			decode(t, rec, &resp)
			if got := resp.Results[0].Decision; got != tt.want {
				t.Errorf("decision = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestWebhookCanceled cancels a webhook before it arrives: 499 and no body,
// and nothing dispatched.
func TestWebhookCanceled(t *testing.T) {
	env := newEnv(t, loaded)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/alerts",
		strings.NewReader(webhook(alert("CheckoutErrorRate", "checkout", "critical", "production", time.Minute))))
	rec := httptest.NewRecorder()
	env.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != server.StatusClientClosedRequest || rec.Body.Len() != 0 {
		t.Errorf("status = %d with body %q, want 499 and none", rec.Code, rec.Body)
	}
	if n := env.notes.list(); len(n) != 0 {
		t.Errorf("a canceled webhook dispatched %+v", n)
	}
}

// manyAlerts builds n firing alerts.
func manyAlerts(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = alert("CheckoutErrorRate", "checkout", "critical", "production", time.Minute)
	}
	return out
}

// TestWebhookLogs checks the one "alert routed" line and the one
// alertrouter.route span of each firing alert. Their team, which log
// pipelines and trace queries index, is the owner or "-" for an unowned
// alert, as on the metrics, and the alert's own label is kept apart as
// team_label.
func TestWebhookLogs(t *testing.T) {
	logs := observeLogs(t)
	env := newEnv(t, loaded)
	do(env.srv.Handler(), http.MethodPost, "/api/v1/alerts", webhook(
		alert("CheckoutErrorRate", "checkout", "critical", "production", time.Minute),
		alert("BillingDown", "billing", "critical", "production", time.Minute),
		resolved(alert("CheckoutDiskFull", "checkout", "critical", "production", time.Hour)),
	))

	tests := []struct {
		alertname, team, teamLabel, status string
	}{
		{alertname: "CheckoutErrorRate", team: "checkout", status: "routed"},
		{alertname: "BillingDown", team: "-", teamLabel: "billing", status: "unowned"},
	}
	lines := logs.FilterMessage("alert routed").All()
	if len(lines) != len(tests) {
		t.Fatalf("alert routed lines = %d, want one per firing alert, %d", len(lines), len(tests))
	}
	spans := findSpans(env.spans.GetSpans(), "alertrouter.route")
	if len(spans) != len(tests) {
		t.Fatalf("alertrouter.route spans = %d, want one per firing alert, %d", len(spans), len(tests))
	}
	for i, tt := range tests {
		t.Run(tt.alertname, func(t *testing.T) {
			fields := lines[i].ContextMap()
			label, _ := fields["team_label"].(string)
			if fields["alertname"] != tt.alertname || fields["team"] != tt.team || label != tt.teamLabel || fields["status"] != tt.status {
				t.Errorf("line = %v, want %+v", fields, tt)
			}
			attrs := attributes(spans[i].Attributes)
			if attrs["alertrouter.team"].AsString() != tt.team || attrs["alertrouter.team_label"].AsString() != tt.teamLabel {
				t.Errorf("span team, team_label = %q, %q; want %q, %q",
					attrs["alertrouter.team"].AsString(), attrs["alertrouter.team_label"].AsString(), tt.team, tt.teamLabel)
			}
		})
	}
}

// TestFailedLog checks a failed alert's "alert routed" line from either
// endpoint: at error level, with status the alert's outcome, a string, and
// the HTTP status under its own key, so a query for status="failed" finds
// it wherever the alert came from.
func TestFailedLog(t *testing.T) {
	tests := []struct {
		name, path, body string
	}{
		{name: "webhook", path: "/api/v1/alerts", body: webhook(alert("CheckoutErrorRate", "checkout", "critical", "production", time.Minute))},
		{name: "route endpoint", path: "/api/v1/teams/checkout/route", body: routeRequest("critical", "production", "1m")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := observeLogs(t)
			env := newEnv(t, envOptions{loaded: true, teamOverrides: checkoutWith(t,
				"when alert.severity == critical {\n  page(reason: critical_alert, target: \"checkout-secondary\")\n}")})
			do(env.srv.Handler(), http.MethodPost, tt.path, tt.body)

			lines := logs.FilterMessage("alert routed").All()
			if len(lines) != 1 {
				t.Fatalf("alert routed lines = %d, want 1", len(lines))
			}
			seen := map[string]int{}
			for _, f := range lines[0].Context {
				seen[f.Key]++
			}
			fields := lines[0].ContextMap()
			for key, want := range map[string]any{
				"status": "failed", "http_status": int64(http.StatusInternalServerError),
				"error_kind": "conflict", "destination": "#alerts",
			} {
				if fields[key] != want || seen[key] != 1 {
					t.Errorf("%s = %v (%d times), want %v once", key, fields[key], seen[key], want)
				}
			}
			if lines[0].Level.String() != "error" {
				t.Errorf("level = %v, want error", lines[0].Level)
			}
		})
	}
}
