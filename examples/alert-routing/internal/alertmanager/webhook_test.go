package alertmanager_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/alertmanager"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// payload is a webhook as Alertmanager sends it, trimmed to one alert.
const payload = `{
  "version": "4",
  "groupKey": "{}/{team=\"checkout\"}:{alertname=\"CheckoutErrorRate\"}",
  "truncatedAlerts": 2,
  "status": "firing",
  "receiver": "alertrouter",
  "groupLabels": {"alertname": "CheckoutErrorRate"},
  "commonLabels": {"alertname": "CheckoutErrorRate", "team": "checkout"},
  "commonAnnotations": {"summary": "errors"},
  "externalURL": "http://alertmanager:9093",
  "alerts": [{
    "status": "firing",
    "labels": {"alertname": "CheckoutErrorRate", "severity": "critical", "team": "checkout"},
    "annotations": {"summary": "errors"},
    "startsAt": "2026-09-30T12:00:00Z",
    "endsAt": "0001-01-01T00:00:00Z",
    "generatorURL": "http://prometheus:9090/graph",
    "fingerprint": "3f6c2a9d81b04e57"
  }]
}`

// now is the time the tests receive their alerts at.
var now = time.Date(2026, 9, 30, 12, 10, 0, 0, time.UTC)

// TestWebhookJSON pins the JSON tags to the names Alertmanager sends: a
// misspelled tag would silently decode to a zero value.
func TestWebhookJSON(t *testing.T) {
	var w alertmanager.Webhook
	if err := json.Unmarshal([]byte(payload), &w); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "version", got: w.Version, want: "4"},
		{name: "groupKey", got: w.GroupKey, want: `{}/{team="checkout"}:{alertname="CheckoutErrorRate"}`},
		{name: "truncatedAlerts", got: w.TruncatedAlerts, want: 2},
		{name: "status", got: w.Status, want: "firing"},
		{name: "receiver", got: w.Receiver, want: "alertrouter"},
		{name: "groupLabels", got: w.GroupLabels["alertname"], want: "CheckoutErrorRate"},
		{name: "commonLabels", got: w.CommonLabels["team"], want: "checkout"},
		{name: "commonAnnotations", got: w.CommonAnnotations["summary"], want: "errors"},
		{name: "externalURL", got: w.ExternalURL, want: "http://alertmanager:9093"},
		{name: "alerts", got: len(w.Alerts), want: 1},
		{name: "alert status", got: w.Alerts[0].Status, want: "firing"},
		{name: "alert labels", got: w.Alerts[0].Labels["severity"], want: "critical"},
		{name: "alert annotations", got: w.Alerts[0].Annotations["summary"], want: "errors"},
		{name: "alert startsAt", got: w.Alerts[0].StartsAt, want: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)},
		{name: "alert endsAt", got: w.Alerts[0].EndsAt.IsZero(), want: true},
		{name: "alert generatorURL", got: w.Alerts[0].GeneratorURL, want: "http://prometheus:9090/graph"},
		{name: "alert fingerprint", got: w.Alerts[0].Fingerprint, want: "3f6c2a9d81b04e57"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	alerts := func(n int, status string) []alertmanager.Alert {
		as := make([]alertmanager.Alert, n)
		for i := range as {
			as[i].Status = status
		}
		return as
	}

	tests := []struct {
		name    string
		webhook alertmanager.Webhook
		wantErr string
	}{
		{name: "firing and resolved alerts", webhook: alertmanager.Webhook{Version: "4", Alerts: append(alerts(1, "firing"), alerts(1, "resolved")...)}},
		{name: "no alerts", webhook: alertmanager.Webhook{Version: "4"}},
		{name: "exactly MaxAlerts", webhook: alertmanager.Webhook{Version: "4", Alerts: alerts(alertmanager.MaxAlerts, "firing")}},
		{name: "no version", webhook: alertmanager.Webhook{}, wantErr: `the webhook has version "", not "4"`},
		{name: "version 3", webhook: alertmanager.Webhook{Version: "3"}, wantErr: `version "3"`},
		{
			name:    "one alert more than MaxAlerts",
			webhook: alertmanager.Webhook{Version: "4", Alerts: alerts(alertmanager.MaxAlerts+1, "firing")},
			wantErr: "the webhook carries 1001 alerts, more than the 1000",
		},
		{
			name:    "an alert without a status",
			webhook: alertmanager.Webhook{Version: "4", Alerts: append(alerts(1, "firing"), alerts(1, "")...)},
			wantErr: `alert 1 has the status ""`,
		},
		{
			name:    "an alert with an unknown status",
			webhook: alertmanager.Webhook{Version: "4", Alerts: alerts(1, "pending")},
			wantErr: `alert 0 has the status "pending"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.webhook.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %s, want nil", err.Display())
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
			if len(err.Advice()) == 0 {
				t.Error("the error has no advice")
			}
		})
	}
}

func TestFiring(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: alertmanager.StatusFiring, want: true},
		{status: alertmanager.StatusResolved},
		{status: ""},
		{status: "Firing"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := (alertmanager.Alert{Status: tt.status}).Firing(); got != tt.want {
				t.Errorf("Alert{Status: %q}.Firing() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestConvert(t *testing.T) {
	labels := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	critical := labels("alertname", "CheckoutErrorRate", "severity", "critical", "team", "checkout", "env", "production")

	tests := []struct {
		alert   alertmanager.Alert
		name    string
		wantErr string
		want    routing.Alert
	}{
		{
			name:  "every label reaches the policy",
			alert: alertmanager.Alert{Labels: critical, StartsAt: now.Add(-10 * time.Minute)},
			want:  routing.Alert{Name: "CheckoutErrorRate", Severity: routing.Critical, Labels: critical, FiringFor: 10 * time.Minute},
		},
		{
			name:  "a warning",
			alert: alertmanager.Alert{Labels: labels("alertname", "A", "severity", "warning"), StartsAt: now.Add(-time.Hour)},
			want:  routing.Alert{Name: "A", Severity: routing.Warning, Labels: labels("alertname", "A", "severity", "warning"), FiringFor: time.Hour},
		},
		{
			name:  "an alert that starts now",
			alert: alertmanager.Alert{Labels: labels("alertname", "A", "severity", "info"), StartsAt: now},
			want:  routing.Alert{Name: "A", Severity: routing.Info, Labels: labels("alertname", "A", "severity", "info")},
		},
		{
			// The sender's clock runs ahead of the router's.
			name:  "an alert that starts in the future fires for zero",
			alert: alertmanager.Alert{Labels: labels("alertname", "A", "severity", "info"), StartsAt: now.Add(time.Minute)},
			want:  routing.Alert{Name: "A", Severity: routing.Info, Labels: labels("alertname", "A", "severity", "info")},
		},
		{
			name:    "no alertname",
			alert:   alertmanager.Alert{Labels: labels("severity", "critical"), StartsAt: now},
			wantErr: "the alert has no alertname label",
		},
		{
			name:    "no labels at all",
			alert:   alertmanager.Alert{StartsAt: now},
			wantErr: "the alert has no alertname label",
		},
		{
			name:    "no severity",
			alert:   alertmanager.Alert{Labels: labels("alertname", "A"), StartsAt: now},
			wantErr: "alert A has no severity label",
		},
		{
			name:    "an empty severity",
			alert:   alertmanager.Alert{Labels: labels("alertname", "A", "severity", ""), StartsAt: now},
			wantErr: `alert A has the severity ""`,
		},
		{
			name:    "an unknown severity",
			alert:   alertmanager.Alert{Labels: labels("alertname", "A", "severity", "urgent"), StartsAt: now},
			wantErr: `alert A has the severity "urgent", which the AlertRouting kind doesn't declare`,
		},
		{
			name:    "an upper-case severity",
			alert:   alertmanager.Alert{Labels: labels("alertname", "A", "severity", "Critical"), StartsAt: now},
			wantErr: `alert A has the severity "Critical"`,
		},
		{
			name:    "no startsAt",
			alert:   alertmanager.Alert{Labels: labels("alertname", "A", "severity", "warning")},
			wantErr: "alert A has no startsAt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := alertmanager.Convert(tt.alert, now)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Convert() = %+v, want an error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("Convert() error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				if len(err.Advice()) == 0 {
					t.Error("the error has no advice")
				}
				return
			}

			if err != nil {
				t.Fatalf("Convert() error = %s", err.Display())
			}
			if got.Name != tt.want.Name || got.Severity != tt.want.Severity || got.FiringFor != tt.want.FiringFor || !maps.Equal(got.Labels, tt.want.Labels) {
				t.Errorf("Convert() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestConvertCopiesLabels guards the copy: the alert the policy reads must
// not share its labels with the webhook the handler still holds.
func TestConvertCopiesLabels(t *testing.T) {
	a := alertmanager.Alert{Labels: map[string]string{"alertname": "A", "severity": "info"}, StartsAt: now}
	got, err := alertmanager.Convert(a, now)
	if err != nil {
		t.Fatal(err.Display())
	}

	a.Labels["env"] = "production"
	if _, ok := got.Labels["env"]; ok {
		t.Error("a label added to the webhook's alert reached the converted alert")
	}
}

// TestSeverityAdvice checks that the advice lists the kind's severities, so
// it stays right when the kind gains one.
func TestSeverityAdvice(t *testing.T) {
	_, err := alertmanager.Convert(alertmanager.Alert{Labels: map[string]string{"alertname": "A"}, StartsAt: now}, now)
	if err == nil {
		t.Fatal("Convert() succeeded without a severity")
	}
	if advice := strings.Join(err.Advice(), " "); !strings.Contains(advice, "critical, warning or info") {
		t.Errorf("advice = %q, want it to list critical, warning or info", advice)
	}
}
