package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
)

// TestClassify covers every error Eval returns, built by hand, so each
// status is pinned to the error's type and fields rather than to a policy
// that happens to produce it. The handler tests check the same statuses
// end to end.
func TestClassify(t *testing.T) {
	failed := []policy.AssertFailure{{Reason: "named_actor", Policy: "access.guardrails"}}
	panicked := &policy.RuntimeError{
		Message: "host function split panicked: boom",
		Err:     &policy.HostPanicError{Func: "split", Value: "boom", Stack: []byte("goroutine 1 [running]:")},
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
			wantText:   "the request fails access.main's asserts: named_actor",
			wantAssert: true,
		},
		{
			name:       "a failed outcome assert is the policy's",
			err:        &policy.AssertionError{Failures: failed, Phase: policy.OutcomeAsserts},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindAssertion,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "the outcome of access.main fails its asserts: named_actor",
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
			wantText:   "the outcome of access.main fails its asserts",
			wantAssert: true,
		},
		{
			name:       "a runtime error is the policy's",
			err:        &policy.RuntimeError{Message: "index 9 out of range for a list of 2"},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindRuntime,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "the policy doesn't guard against this input",
		},
		{
			name:       "a host function's panic is deploygate's bug, with the stack for the log",
			err:        panicked,
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindRuntime,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "a host function panicked, which is a bug in deploygate",
			wantStack:  true,
		},
		{
			name:       "a host function's own timeout stays a runtime error",
			err:        &policy.RuntimeError{Message: "host function lookup failed", Err: fmt.Errorf("registry: %w", context.DeadlineExceeded)},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindRuntime,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "can't be evaluated against this request",
		},
		{
			name:       "a conflict is the policy's",
			err:        &policy.ConflictError{Message: "admin and release_manager are exclusive"},
			wantStatus: http.StatusInternalServerError,
			wantKind:   telemetry.ErrorKindConflict,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "can't stand together",
		},
		{
			name:       "the evaluation timeout is deploygate's",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusServiceUnavailable,
			wantKind:   telemetry.ErrorKindTimeout,
			wantLevel:  zapcore.ErrorLevel,
			wantText:   "wasn't decided within deploygate's evaluation timeout",
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
			wantText:   "evaluating access.main failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := classify("access.main", tt.err, "no role is granted")
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
		{name: "failed", err: &policy.ConflictError{Message: "two roles"}, want: codes.Error},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

			_, span := tp.Tracer("test").Start(context.Background(), "deploygate.evaluate")
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

// TestAnswerUncounted checks the two failures a handler answers without a
// decision or a count, and that it leaves every other one to its caller.
func TestAnswerUncounted(t *testing.T) {
	tests := []struct {
		name       string
		f          failure
		wantDone   bool
		wantStatus int
		wantBody   string
	}{
		{name: "a canceled request gets 499 and no body", f: classify("access.main", context.Canceled, ""), wantDone: true, wantStatus: StatusClientClosedRequest},
		{name: "an unknown error gets 500 and the error model", f: classify("access.main", errors.New("disk on fire"), ""), wantDone: true, wantStatus: http.StatusInternalServerError, wantBody: `"message":"evaluating access.main failed`},
		{name: "a counted failure is the caller's to answer", f: classify("access.main", context.DeadlineExceeded, "no role is granted"), wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/access/grants", nil)

			done := answerUncounted(c, tt.f, zap.String("stage", "access"))
			c.Writer.WriteHeaderNow()
			if done != tt.wantDone || rec.Code != tt.wantStatus {
				t.Errorf("answered, status = %v, %d; want %v, %d", done, rec.Code, tt.wantDone, tt.wantStatus)
			}
			if tt.wantBody == "" && rec.Body.Len() != 0 || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body %q, want %q", rec.Body, tt.wantBody)
			}
		})
	}
}

// TestEvalWithinNoPolicy checks that a missing policy is an error rather
// than a nil dereference; the handlers rule it out before they get here.
func TestEvalWithinNoPolicy(t *testing.T) {
	res, err := evalWithin[deploy.Input](context.Background(), time.Second, nil, deploy.Input{})
	if res != nil || err == nil {
		t.Errorf("evalWithin(nil) = %v, %v; want no result and an error", res, err)
	}
}

// logged reports whether f's log line carries a field called key.
func logged(f failure, key string) bool {
	for _, field := range f.logFields() {
		if field.Key == key {
			return true
		}
	}
	return false
}
