package dispatch_test

import (
	"context"
	"errors"
	"testing"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spechtlabs/go-otel-utils/otelzap"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
)

func TestDestination(t *testing.T) {
	tests := []struct {
		name string
		n    dispatch.Notification
		want string
	}{
		{name: "a page goes to its target", n: dispatch.Notification{Decision: "page", Target: "checkout-primary"}, want: "checkout-primary"},
		{name: "a notification goes to its channel", n: dispatch.Notification{Decision: "notify", Channel: "#checkout-alerts"}, want: "#checkout-alerts"},
		{name: "a drop goes nowhere", n: dispatch.Notification{Decision: "drop"}, want: dispatch.NoDestination},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.n.Destination(); got != tt.want {
				t.Errorf("Destination() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLogNotifier checks the one line each notification writes: its fields,
// and the trace and span ids when the context carries a span, so a log line
// in Loki leads to the evaluation's trace in Tempo.
func TestLogNotifier(t *testing.T) {
	tests := []struct {
		name      string
		n         dispatch.Notification
		traced    bool
		wantDest  string
		wantTrace bool
	}{
		{
			name:     "a page",
			n:        dispatch.Notification{Team: "checkout", AlertName: "CheckoutErrorRate", Fingerprint: "a1", Decision: "page", Reason: "critical_alert", Target: "checkout-primary"},
			traced:   true,
			wantDest: "checkout-primary", wantTrace: true,
		},
		{
			name:     "a drop without a span",
			n:        dispatch.Notification{Team: "payments", AlertName: "LedgerLag", Decision: "drop", Reason: "not_production"},
			wantDest: dispatch.NoDestination,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := observeLogs(t)
			ctx := context.Background()
			if tt.traced {
				tp := sdktrace.NewTracerProvider()
				t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
				var span trace.Span
				ctx, span = tp.Tracer("test").Start(ctx, "alertrouter.route")
				defer span.End()
			}

			var notifier dispatch.Notifier = dispatch.NewLogNotifier()
			if err := notifier.Notify(ctx, tt.n); err != nil {
				t.Fatalf("Notify: %v", err)
			}

			entries := logs.FilterMessage("notification dispatched").All()
			if len(entries) != 1 {
				t.Fatalf("got %d notification lines, want 1: %+v", len(entries), logs.All())
			}
			e := entries[0]
			if e.Level != zapcore.InfoLevel {
				t.Errorf("level = %v, want info", e.Level)
			}
			fields := e.ContextMap()
			for key, want := range map[string]string{
				"team": tt.n.Team, "alertname": tt.n.AlertName, "fingerprint": tt.n.Fingerprint,
				"decision": tt.n.Decision, "reason": tt.n.Reason, "destination": tt.wantDest,
			} {
				if fields[key] != want {
					t.Errorf("%s = %v, want %q", key, fields[key], want)
				}
			}
			if _, ok := fields["trace_id"]; ok != tt.wantTrace {
				t.Errorf("trace_id present = %v, want %v", ok, tt.wantTrace)
			}
		})
	}
}

func TestNotifierFunc(t *testing.T) {
	boom := humane.New("pager down", "check the pager")
	var got dispatch.Notification
	f := dispatch.NotifierFunc(func(_ context.Context, n dispatch.Notification) humane.Error {
		got = n
		return boom
	})
	n := dispatch.Notification{AlertName: "CheckoutErrorRate"}
	if err := f.Notify(context.Background(), n); !errors.Is(err, boom) {
		t.Errorf("Notify error = %v, want %v", err, boom)
	}
	if got != n {
		t.Errorf("the function got %+v, want %+v", got, n)
	}
}

// observeLogs installs a logger that records every line as the process
// logger, and puts the previous one back when the test ends.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	undo := otelzap.ReplaceGlobals(otelzap.New(zap.New(core)))
	t.Cleanup(undo)
	return logs
}

// TestLogNotifierRecordsDestination checks that the destination lands on the
// alert's span, so a trace says where the alert went.
func TestLogNotifierRecordsDestination(t *testing.T) {
	tests := []struct {
		name string
		n    dispatch.Notification
		want string
	}{
		{name: "a page", n: dispatch.Notification{Decision: "page", Target: "payments-primary"}, want: "payments-primary"},
		{name: "a drop", n: dispatch.Notification{Decision: "drop"}, want: dispatch.NoDestination},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observeLogs(t)
			spans := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

			ctx, span := tp.Tracer("test").Start(context.Background(), "alertrouter.route")
			if err := dispatch.NewLogNotifier().Notify(ctx, tt.n); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			span.End()

			for _, kv := range spans.GetSpans()[0].Attributes {
				if kv.Key == "alertrouter.destination" {
					if got := kv.Value.AsString(); got != tt.want {
						t.Errorf("alertrouter.destination = %q, want %q", got, tt.want)
					}
					return
				}
			}
			t.Error("the span has no alertrouter.destination")
		})
	}
}
