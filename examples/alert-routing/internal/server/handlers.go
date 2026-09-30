package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// StatusClientClosedRequest is the status of a request whose client went
// away before the answer: 499, nginx's "client closed request". No client
// reads it. It's for the access log and the request metrics, where it keeps
// a client that gave up apart from both a client error and a server
// failure, so it burns no error budget.
const StatusClientClosedRequest = 499

// The strictness of a request body's decoding: the route endpoint's body is
// alertrouter's own format, where an unknown field is a typo worth
// reporting, while the webhook's is Alertmanager's, which may grow fields
// alertrouter doesn't read.
const (
	strictFields  = true
	lenientFields = false
)

// failure is a failed policy evaluation, classified: the kind of error for
// the metrics, the HTTP status that says whose fault it is, the error with
// advice for the response, and the asserts or conflicting candidates that
// explain it. kind is empty for an error that isn't an evaluation error to
// count: a request the client canceled, which answers 499 without a body,
// and an error classify doesn't know.
type failure struct {
	herr     humane.Error
	kind     string
	status   int
	asserts  []AssertResult
	conflict *ConflictResult
	// stack is the host function's stack when it panicked, for the log
	// only: it says nothing to the client and is too long for a message.
	stack []byte
}

// listPolicies handles GET /api/v1/policies: the loaded bundle, in
// deploygate's shape with one kind.
func (s *Server) listPolicies(c *gin.Context) {
	snap, ok := s.store.Snapshot()
	if !ok {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	c.JSON(http.StatusOK, PoliciesResponse{Kinds: []KindPolicies{newKindPolicies(snap)}})
}

// reload handles POST /api/v1/policies/reload. A bundle that doesn't load is
// a 500 with the compiler's diagnostics, and the previous bundle keeps
// serving.
func (s *Server) reload(c *gin.Context) {
	if err := s.store.Load(c.Request.Context()); err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}
	s.listPolicies(c)
}

// listTeams handles GET /api/v1/teams: the team directory the alerts' team
// labels are looked up in.
func (s *Server) listTeams(c *gin.Context) {
	c.JSON(http.StatusOK, TeamsResponse{Teams: s.directory.Teams()})
}

// healthz handles GET /healthz: the process is up and serving HTTP.
func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, StatusResponse{Status: "ok"})
}

// readyz handles GET /readyz: ready once the bundle is loaded, so a pod whose
// first load failed never receives alerts.
func (s *Server) readyz(c *gin.Context) {
	snap, ok := s.store.Snapshot()
	if !ok {
		c.JSON(http.StatusServiceUnavailable, StatusResponse{Status: "not ready"})
		return
	}
	loadedAt := snap.LoadedAt.UTC()
	c.JSON(http.StatusOK, StatusResponse{Status: "ready", LoadedAt: &loadedAt})
}

// classify names the kind of evaluation error for the metrics, picks the
// status that says whose fault it is, and wraps the error with advice. Both
// endpoints answer through it, so a failure has the same meaning wherever it
// happens:
//
//   - A failed input assert is the caller's: the policy declared the input
//     invalid before any rule ran. 422.
//   - A failed outcome assert, a conflict and a runtime error, a host
//     function's panic included, are the policy's or the host's: the input
//     was acceptable, and what the policy made of it wasn't. 500, so the
//     failure counts against alertrouter's error budget instead of reading
//     as a client mistake.
//   - An evaluation that ran past the evaluation timeout is alertrouter's:
//     the alert may be fine, and the service didn't decide it in time. 503,
//     a server that can't handle the request now, rather than 504, which
//     says an upstream server didn't answer: alertrouter evaluates in
//     process and has no upstream.
//   - A request the client canceled is no one's failure: the client left
//     before the answer. [StatusClientClosedRequest], and kind is empty, so
//     it isn't counted as an evaluation error and nothing is written.
//   - Anything else isn't the policy's at all. 500, and kind is empty.
//
// The assert phase comes from [policy.AssertionError.Phase], not from the
// trace, which an outcome assert that failed with nothing fired leaves as
// empty as a failed input assert does. The policy's errors are matched
// before the context's, so a host function's own timeout, which a
// [policy.RuntimeError] unwraps to, stays a runtime error.
func classify(policyName string, err error) failure {
	const fallback = "the alert was routed with the fallback decision, notify(reason: unrouted), which the decision fields hold"
	var (
		assertErr   *policy.AssertionError
		runtimeErr  *policy.RuntimeError
		conflictErr *policy.ConflictError
	)
	switch {
	case errors.As(err, &assertErr):
		return assertFailure(policyName, err, assertResults(assertErr), assertErr.Phase, fallback)
	case errors.As(err, &runtimeErr):
		return runtimeFailure(policyName, err, runtimeErr.Message+" at "+runtimeErr.Position.String(), fallback)
	case errors.As(err, &conflictErr):
		return failure{
			kind:   telemetry.ErrorKindConflict,
			status: http.StatusInternalServerError,
			herr: humane.Wrap(err,
				fmt.Sprintf("%s produced decisions that can't stand together: %s", policyName, conflictErr.Message),
				fallback,
				"a conflict is a defect in the policy, not in the alert, such as a team rule that pages someone else than the platform's; tell the policy's owners",
			),
			conflict: conflictResult(conflictErr),
		}
	case errors.Is(err, context.Canceled):
		return failure{
			status: StatusClientClosedRequest,
			herr: humane.Wrap(err, "the client closed the request while "+policyName+" was evaluated",
				"nothing to fix on either side; the client left before the answer"),
		}
	case errors.Is(err, context.DeadlineExceeded):
		return failure{
			kind:   telemetry.ErrorKindTimeout,
			status: http.StatusServiceUnavailable,
			herr: humane.Wrap(err,
				fmt.Sprintf("%s wasn't decided within alertrouter's evaluation timeout", policyName),
				fallback,
				"if this alert keeps timing out, the policy is slow for its input: tell alertrouter's operators",
			),
		}
	default:
		return failure{status: http.StatusInternalServerError, herr: humane.Wrap(err, "evaluating "+policyName+" failed",
			fallback, "if it keeps failing, check the alertrouter logs")}
	}
}

// assertFailure classifies a failed assert by its phase: a failed input
// assert is the caller's, 422, and a failed outcome assert the policy's,
// 500. See classify.
func assertFailure(policyName string, err error, asserts []AssertResult, phase policy.AssertPhase, fallback string) failure {
	reasons := make([]string, 0, len(asserts))
	for _, a := range asserts {
		reasons = append(reasons, a.Reason)
	}
	list := strings.Join(reasons, ", ")

	if phase == policy.InputAsserts {
		return failure{
			kind:   telemetry.ErrorKindAssertion,
			status: http.StatusUnprocessableEntity,
			herr: humane.Wrap(err,
				fmt.Sprintf("the alert fails %s's asserts: %s", policyName, list),
				fallback,
				"fix the alert so the asserts listed in asserts hold",
			),
			asserts: asserts,
		}
	}
	return failure{
		kind:   telemetry.ErrorKindAssertion,
		status: http.StatusInternalServerError,
		herr: humane.Wrap(err,
			fmt.Sprintf("the outcome of %s fails its asserts: %s", policyName, list),
			fallback,
			"an outcome assert checks what the policy decided, which the alert can't change; tell the policy's owners",
		),
		asserts: asserts,
	}
}

// runtimeFailure classifies a runtime error, 500. A host function that
// panicked is a bug in alertrouter rather than in the policy, so its advice
// says so, and its stack goes to the log. what is the runtime error's
// message and position. See classify.
func runtimeFailure(policyName string, err error, what, fallback string) failure {
	advice := "a runtime error usually means the policy doesn't guard against this alert; tell the policy's owners"
	var stack []byte
	if panicErr, ok := errors.AsType[*policy.HostPanicError](err); ok {
		advice = "a host function panicked, which is a bug in alertrouter, not in the policy; report it with the alert"
		stack = panicErr.Stack
	}
	return failure{
		kind:   telemetry.ErrorKindRuntime,
		status: http.StatusInternalServerError,
		herr: humane.Wrap(err,
			fmt.Sprintf("%s can't be evaluated against this alert: %s", policyName, what),
			fallback, advice,
		),
		stack: stack,
	}
}

// decodeJSON reads the body into a T: trailing data and numeric durations
// are errors, and with strict so are unknown fields, so a typo in a field
// name is reported instead of silently routing a zero value. advice says
// what a valid body looks like. A body over the size cap is an error whose
// [bodyStatus] is 413.
func decodeJSON[T any](c *gin.Context, strict bool, advice string) (*T, humane.Error) {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	if strict {
		dec.DisallowUnknownFields()
	}

	var v T
	if err := dec.Decode(&v); err != nil {
		return nil, badRequest(err, advice)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, humane.New("the request body holds more than one JSON value",
			"send exactly one JSON object per call")
	}
	return &v, nil
}

// bodyStatus is the status of a body decodeJSON refused: 413 for one over
// the size cap, 400 for everything else.
func bodyStatus(herr humane.Error) int {
	if _, ok := errors.AsType[*http.MaxBytesError](herr); ok {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// badRequest explains why a body didn't decode.
func badRequest(err error, advice string) humane.Error {
	if errors.Is(err, io.EOF) {
		return humane.New("the request body is empty", advice)
	}

	// A duration error is a humane error already, with its own advice.
	if _, ok := errors.AsType[humane.Error](err); ok {
		return humane.Wrap(err, "the request body isn't a valid request", advice)
	}

	if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return humane.Wrap(err, fmt.Sprintf("the request body is larger than %d bytes", maxErr.Limit), advice,
			"split the alerts over several webhooks, or lower max_alerts on the Alertmanager receiver")
	}

	return humane.Wrap(err, "the request body isn't a valid request: "+err.Error(), advice,
		"field names are case-sensitive")
}

// newKindPolicies renders the snapshot.
func newKindPolicies(snap *store.Snapshot[routing.Input]) KindPolicies {
	if snap == nil {
		return KindPolicies{Policies: []PolicyResponse{}}
	}
	out := KindPolicies{
		Kind:        snap.Kind,
		Version:     snap.KindVersion,
		LoadedAt:    snap.LoadedAt.UTC(),
		Source:      snap.Source,
		Fingerprint: snap.Fingerprint,
		Policies:    make([]PolicyResponse, 0, len(snap.Roots)),
	}
	for _, r := range snap.Roots {
		out.Policies = append(out.Policies, PolicyResponse{Team: r.Team, Policy: r.Policy})
	}
	return out
}

// errNotLoaded is the answer while the bundle hasn't loaded yet.
func errNotLoaded() humane.Error {
	return humane.New("no policy bundle is loaded yet",
		"wait until GET /readyz reports ready",
		"if it never does, the alertrouter logs name the policy that fails to compile")
}

// logLevel is the level a classified evaluation failure is logged at: info
// for a request the client canceled, which needs no one's attention, a
// warning when it's the caller's, a 4xx, and an error when it's the
// policy's or alertrouter's, a 5xx, the same line writeError draws.
func (f failure) logLevel() zapcore.Level {
	switch {
	case f.status == StatusClientClosedRequest:
		return zapcore.InfoLevel
	case f.status >= http.StatusInternalServerError:
		return zapcore.ErrorLevel
	}
	return zapcore.WarnLevel
}

// logFields are the fields a failed evaluation adds to its alert's log line:
// the failure, with the host function's stack when it panicked. The HTTP
// status is http_status, since the line's status is the alert's outcome.
func (f failure) logFields() []zap.Field {
	fields := []zap.Field{
		zap.String("error_kind", f.kind),
		zap.Int("http_status", f.status),
		zap.Error(f.herr),
	}
	if f.stack != nil {
		fields = append(fields, zap.ByteString("stack", f.stack))
	}
	return fields
}

// evalWithin evaluates p against in with at most timeout to decide. Eval
// stops at the deadline and returns context.DeadlineExceeded with the
// fallback, so an alert that makes the policy slow can't hold a request, or
// a CPU, for longer. The request's own context still ends it sooner when
// the client leaves. A nil p, which the callers rule out first, returns no
// result and an error.
func evalWithin[In any](ctx context.Context, timeout time.Duration, p *policy.Policy[In], in In) (*policy.Result, error) {
	if p == nil {
		return nil, humane.New("there is no policy to evaluate", "this is a bug in alertrouter; please report it")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return p.Eval(ctx, in)
}

// failSpan marks a route span as failed. A request the client canceled is
// recorded on the span but leaves its status unset: the evaluation didn't
// fail, it was stopped, and OpenTelemetry's gRPC conventions don't mark a
// server call the client canceled as an error either, so an error-rate query
// over the spans counts only real failures.
func failSpan(span trace.Span, err error, description string) {
	span.RecordError(err)
	if errors.Is(err, context.Canceled) {
		return
	}
	span.SetStatus(codes.Error, description)
}

// writeError answers with a humane error as JSON. Server-side failures are
// logged too, since the client can't fix them.
func writeError(c *gin.Context, status int, herr humane.Error) {
	if status >= http.StatusInternalServerError {
		ctx := c.Request.Context()
		telemetry.FromContext(ctx).ErrorContext(ctx, "request failed",
			zap.Int("status", status),
			zap.String("path", c.Request.URL.Path),
			zap.Error(herr),
			zap.Strings("advice", herr.Advice()),
		)
	}
	c.JSON(status, ErrorEnvelope{Error: NewErrorResponse(herr)})
}
