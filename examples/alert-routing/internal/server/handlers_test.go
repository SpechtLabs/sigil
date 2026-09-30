package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// TestClassify covers every error Eval returns, built by hand, so each
// status is pinned to the error's type and fields rather than to a policy
// that happens to produce it. The handler tests check the same statuses
// end to end.
func TestClassify(t *testing.T) {
	failed := []policy.AssertFailure{{Reason: "has_component", Policy: "checkout.alerts"}}
	panicked := &policy.RuntimeError{
		Message: "host function lookup panicked: boom",
		Err:     &policy.HostPanicError{Func: "lookup", Value: "boom", Stack: []byte("goroutine 1 [running]:")},
	}
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantKind   string
		wantLevel  zapcore.Level
		wantText   string // in the message or the advice
		wantStack  bool
		wantAssert bool
	}{
		{
			name:       "a failed input assert is the caller's",
			err:        &policy.AssertionError{Failures: failed, Phase: policy.InputAsserts},
			wantStatus: http.StatusUnprocessableEntity,
			wantKind:   telemetry.ErrorKindAssertion,
			wantLevel:  zapcore.WarnLevel,
			wantText:   "the alert fails checkout.alerts's asserts: has_component",
			wantAssert: true,
		},
		{
			name:       "a failed outcome assert is the policy's",
			err:        &policy.AssertionError{Failures: failed, Phase: policy.OutcomeAsserts},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindAssertion,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "the outcome of checkout.alerts fails its asserts: has_component",
			wantAssert: true,
		},
		{
			// Eval never returns it, and if it did, the caller isn't
			// blamed for a failure no one can place.
			name:       "an assert without a phase isn't the caller's",
			err:        &policy.AssertionError{Failures: failed},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindAssertion,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "the outcome of checkout.alerts fails its asserts",
			wantAssert: true,
		},
		{
			name:       "a runtime error is the policy's",
			err:        &policy.RuntimeError{Message: "index 3 out of range for a list of 1"},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindRuntime,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "the policy doesn't guard against this alert",
		},
		{
			name:       "a host function's panic is alertrouter's bug, with the stack for the log",
			err:        panicked,
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindRuntime,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "a host function panicked, which is a bug in alertrouter",
			wantStack:  true,
		},
		{
			name:       "a host function's own timeout stays a runtime error",
			err:        &policy.RuntimeError{Message: "host function lookup failed", Err: fmt.Errorf("registry: %w", context.DeadlineExceeded)},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindRuntime,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "can't be evaluated against this alert",
		},
		{
			name:       "a conflict is the policy's",
			err:        &policy.ConflictError{Message: "two pages of critical_alert to different targets"},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindConflict,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "can't stand together",
		},
		{
			name:       "the evaluation timeout is alertrouter's",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusServiceUnavailable,
			wantKind:   telemetry.ErrorKindTimeout,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "wasn't decided within alertrouter's evaluation timeout",
		},
		{
			name:       "a client that left is no one's failure",
			err:        context.Canceled,
			wantStatus: StatusClientClosedRequest,
			wantLevel:  zapcore.InfoLevel,
			wantText:   "the client closed the request",
		},
		{
			name:       "an error Eval doesn't document isn't counted",
			err:        errors.New("something else"),
			wantStatus: http.StatusInternalServerError,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "evaluating checkout.alerts failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := classify("checkout.alerts", tt.err)
			if f.status != tt.wantStatus || f.kind != tt.wantKind {
				t.Errorf("status, kind = %d, %q; want %d, %q", f.status, f.kind, tt.wantStatus, tt.wantKind)
			}
			if got := f.logLevel(); got != tt.wantLevel {
				t.Errorf("log level = %v, want %v", got, tt.wantLevel)
			}
			if text := f.herr.Error() + " " + strings.Join(f.herr.Advice(), " "); !strings.Contains(text, tt.wantText) {
				t.Errorf("error %q doesn't say %q", text, tt.wantText)
			}
			if !errors.Is(f.herr, tt.err) {
				t.Errorf("error %v doesn't wrap %v", f.herr, tt.err)
			}
			if (len(f.asserts) > 0) != tt.wantAssert {
				t.Errorf("asserts = %+v, want some %v", f.asserts, tt.wantAssert)
			}
			if hasStack := logged(f, "stack"); hasStack != tt.wantStack {
				t.Errorf("stack logged = %v, want %v", hasStack, tt.wantStack)
			}
		})
	}
}

// TestRoutedLogLevel pins the level of each alert's "alert routed" line:
// only what needs someone's attention is logged above info.
func TestRoutedLogLevel(t *testing.T) {
	caller := classify("checkout.alerts", &policy.AssertionError{Phase: policy.InputAsserts})
	policyFault := classify("checkout.alerts", &policy.ConflictError{})
	tests := []struct {
		name string
		r    routed
		want zapcore.Level
	}{
		{name: "routed", r: routed{status: StatusRouted}, want: zapcore.InfoLevel},
		{name: "unowned", r: routed{status: StatusUnowned}, want: zapcore.InfoLevel},
		{name: "invalid", r: routed{status: StatusInvalid}, want: zapcore.WarnLevel},
		{name: "failed by the caller", r: routed{status: StatusFailed, failure: &caller}, want: zapcore.WarnLevel},
		{name: "failed by the policy", r: routed{status: StatusFailed, failure: &policyFault}, want: zapcore.ErrorLevel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.r.logLevel(); got != tt.want {
				t.Errorf("logLevel() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFailSpan checks that a canceled evaluation is recorded on its span
// without marking it failed, and that every other failure marks it.
func TestFailSpan(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "canceled", err: context.Canceled, want: codes.Unset},
		{name: "timed out", err: context.DeadlineExceeded, want: codes.Error},
		{name: "failed", err: &policy.ConflictError{Message: "two pages"}, want: codes.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

			_, span := tp.Tracer("test").Start(context.Background(), "alertrouter.route")
			failSpan(span, tt.err, "evaluation failed")
			span.End()

			got := spans.GetSpans()[0]
			if got.Status.Code != tt.want {
				t.Errorf("status = %v, want %v", got.Status.Code, tt.want)
			}
			if len(got.Events) != 1 || got.Events[0].Name != "exception" {
				t.Errorf("events = %+v, want the error recorded", got.Events)
			}
		})
	}
}

// TestEvalWithinNoPolicy checks that a missing policy is an error rather
// than a nil dereference; the handlers rule it out before they get here.
func TestEvalWithinNoPolicy(t *testing.T) {
	res, err := evalWithin[routing.Input](context.Background(), time.Second, nil, routing.Input{})
	if res != nil || err == nil {
		t.Errorf("evalWithin(nil) = %v, %v; want no result and an error", res, err)
	}
}

// TestRenderValue checks that durations anywhere in a payload render as
// Sigil duration strings, so a kind version with a duration payload needs
// no change to the API.
func TestRenderValue(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "a string", in: "#alerts", want: `"#alerts"`},
		{name: "a duration", in: 15 * time.Minute, want: `"15m"`},
		{name: "a list of durations", in: []any{time.Hour, 90 * time.Minute}, want: `["1h","1h30m"]`},
		{name: "a nested map", in: map[string]any{"after": 2 * time.Hour}, want: `{"after":"2h"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustJSON(t, renderValue(tt.in))
			if got != tt.want {
				t.Errorf("renderValue(%v) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

// mustJSON renders v as JSON.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// logged reports whether f's log fields carry a field called key.
func logged(f failure, key string) bool {
	for _, field := range f.logFields() {
		if field.Key == key {
			return true
		}
	}
	return false
}
