package routing_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/pkg/policy"
	"github.com/spechtlabs/sigil/pkg/policytest"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// TestKindFileIsCurrent fails when policies/alert_routing.sigil is stale.
// Regenerate it with `go generate ./cmd/sigilc`.
func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, routing.Kind, "../../policies/alert_routing.sigil")
}

// TestSeveritiesMatchKind fails when [routing.Severities] and the kind's
// enum Severity drift apart, which would make alertrouter refuse a severity
// a policy can read, or pass one through that fails the evaluation.
func TestSeveritiesMatchKind(t *testing.T) {
	enum := routing.Kind.Contract().Model.Enum("Severity")
	if enum == nil {
		t.Fatal("routing.Kind declares no enum Severity")
	}
	got := make([]string, len(routing.Severities))
	for i, s := range routing.Severities {
		got[i] = string(s)
	}
	if !slices.Equal(got, enum.Values) {
		t.Errorf("routing.Severities = %v, the kind declares %v", got, enum.Values)
	}
}

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		in     string
		want   routing.Severity
		wantOK bool
	}{
		{in: "critical", want: routing.Critical, wantOK: true},
		{in: "warning", want: routing.Warning, wantOK: true},
		{in: "info", want: routing.Info, wantOK: true},
		{in: ""},
		{in: "Critical"},
		{in: "urgent"},
		{in: " warning"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := routing.ParseSeverity(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ParseSeverity(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestPolicies runs every *_test.yaml under policies/teams against the team
// bundle, loaded the way the service loads it: the platform's documents are
// the trusted source of the required platform.paging.
func TestPolicies(t *testing.T) {
	policytest.Run(t, routing.Kind, os.DirFS("../../policies/teams"),
		policy.Require("platform.paging", policy.From(os.DirFS("../../policies/platform"))))
}

// TestRequirePaging checks the Require the service loads every team with:
// a team bundle that doesn't invoke platform.paging unconditionally, or that
// brings its own, doesn't load.
func TestRequirePaging(t *testing.T) {
	const header = "policy checkout.alerts: AlertRouting@1\n\n"

	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "paging invoked at the top level",
			src:  header + "use platform.paging\n\npaging()\n",
		},
		{
			name: "page_after within the platform's bounds",
			src:  header + "use platform.paging\n\npaging(page_after: 1h)\n",
		},
		{
			name:    "page_after above the platform's max",
			src:     header + "use platform.paging\n\npaging(page_after: 2h)\n",
			wantErr: "page_after: 2h is above the maximum 1h",
		},
		{
			name:    "page_after below the platform's min",
			src:     header + "use platform.paging\n\npaging(page_after: 1m)\n",
			wantErr: "page_after: 1m is below the minimum 5m",
		},
		{
			name:    "no paging",
			src:     header + "when alert.severity == info {\n  notify(reason: routine)\n}\n",
			wantErr: "checkout.alerts doesn't invoke platform.paging",
		},
		{
			name:    "paging under a when",
			src:     header + "use platform.paging\n\nwhen alert.labels[\"team\"] == \"checkout\" {\n  paging()\n}\n",
			wantErr: "platform.paging must be invoked unconditionally",
		},
		{
			name:    "a team's own platform.paging",
			src:     header + "use platform.paging\n\npaging()\n---\npolicy platform.paging: AlertRouting@1\n",
			wantErr: "policy platform.paging is defined twice",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := policy.MapFS(map[string]string{"checkout/alerts.sigil": tt.src})
			_, err := routing.Kind.Load(bundle, "checkout.alerts",
				policy.Require("platform.paging", policy.From(os.DirFS("../../policies/platform"))))

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load() succeeded, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestDefaultChannel fails when [routing.DefaultChannel] and the default of
// NotifyData's channel drift apart: the router would post an unowned alert
// somewhere other than where the kind's default decision posts.
func TestDefaultChannel(t *testing.T) {
	p, err := routing.Kind.Compile("policy empty.alerts: AlertRouting@1\n", "empty.alerts")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Eval(t.Context(), routing.Input{})
	if err != nil {
		t.Fatal(err)
	}

	if !routing.Unrouted.Is(res) {
		t.Fatalf("a policy without rules decides %s(%s), want the default notify(unrouted)", res.Decision, res.Reason)
	}
	note, ok := routing.Notify.Match(res)
	if !ok {
		t.Fatal("Notify.Match doesn't match the default decision")
	}
	if note.Channel != routing.DefaultChannel {
		t.Errorf("the default decision posts to %q, DefaultChannel is %q", note.Channel, routing.DefaultChannel)
	}
}
