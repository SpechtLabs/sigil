package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

// failure is a failed policy evaluation, classified: the kind of error for
// the metrics, the HTTP status that says whose fault it is, the error with
// advice for the response, and the asserts or conflicting candidates that
// explain it. kind is empty for an error that isn't the policy's, such as a
// canceled request, which answers 500 without a decision.
type failure struct {
	herr     humane.Error
	kind     string
	status   int
	asserts  []AssertResult
	conflict *ConflictResult
}

// listPolicies handles GET /api/v1/policies: every kind's loaded bundle.
func (s *Server) listPolicies(c *gin.Context) {
	deploySnap, ok := s.deploy.Snapshot()
	accessSnap, accessOK := s.access.Snapshot()
	if !ok || !accessOK {
		writeError(c, http.StatusServiceUnavailable, errNotLoaded())
		return
	}
	c.JSON(http.StatusOK, PoliciesResponse{Kinds: []KindPolicies{
		newKindPolicies(deploySnap),
		newKindPolicies(accessSnap),
	}})
}

// reload handles POST /api/v1/policies/reload: it reloads both bundles. A
// bundle that doesn't load is a 500 with the compiler's diagnostics, and the
// previous bundle of that kind keeps serving; the other kind still reloads.
// When both fail, the response carries the team policies' error and the log
// both.
func (s *Server) reload(c *gin.Context) {
	ctx := c.Request.Context()
	deployErr := s.deploy.Load(ctx)
	accessErr := s.access.Load(ctx)
	switch {
	case deployErr != nil:
		writeError(c, http.StatusInternalServerError, deployErr)
	case accessErr != nil:
		writeError(c, http.StatusInternalServerError, accessErr)
	default:
		s.listPolicies(c)
	}
}

// healthz handles GET /healthz: the process is up and serving HTTP.
func (s *Server) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, StatusResponse{Status: "ok"})
}

// readyz handles GET /readyz: ready once both bundles are loaded, so a pod
// whose first load failed never receives traffic. loaded_at is the latest
// successful load of either.
func (s *Server) readyz(c *gin.Context) {
	deploySnap, ok := s.deploy.Snapshot()
	accessSnap, accessOK := s.access.Snapshot()
	if !ok || !accessOK {
		c.JSON(http.StatusServiceUnavailable, StatusResponse{Status: "not ready"})
		return
	}
	loadedAt := deploySnap.LoadedAt
	if accessSnap.LoadedAt.After(loadedAt) {
		loadedAt = accessSnap.LoadedAt
	}
	loadedAt = loadedAt.UTC()
	c.JSON(http.StatusOK, StatusResponse{Status: "ready", LoadedAt: &loadedAt})
}

// classify names the kind of evaluation error for the metrics, picks the
// status that says whose fault it is, and wraps the error with advice.
// Both stages and both endpoints answer through it, so a failure has the
// same status wherever it happens:
//
//   - A failed input assert is the caller's: the policy declared the input
//     invalid before any rule ran. 422.
//   - A failed outcome assert, a conflict and a runtime error are the
//     policy's or the host's: the input was acceptable, and what the policy
//     made of it wasn't. 500, so the failure counts against deploygate's
//     error budget instead of reading as a client mistake.
//   - Anything else, such as a canceled request, isn't the policy's at all.
//     500, and kind is empty, so no decision is rendered.
//
// fired says whether any rule produced a candidate before the evaluation
// failed. The library doesn't say which assert phase failed, but no rule
// runs once an input assert fails, so a failed assert with candidates can
// only be an outcome assert. An outcome assert that fails with nothing
// fired can't be told apart from an input assert, and answers 422 too.
// fallback says what the response means for the client, which differs
// between the stages and endpoints.
func classify(policyName string, err error, fired bool, fallback string) failure {
	var (
		assertErr   *policy.AssertionError
		runtimeErr  *policy.RuntimeError
		conflictErr *policy.ConflictError
	)
	switch {
	case errors.As(err, &assertErr):
		return assertFailure(policyName, err, assertErr, fired, fallback)
	case errors.As(err, &runtimeErr):
		return failure{
			kind:   telemetry.ErrorKindRuntime,
			status: http.StatusInternalServerError,
			herr: humane.Wrap(err,
				fmt.Sprintf("%s can't be evaluated against this request: %s at %s", policyName, runtimeErr.Message, runtimeErr.Position),
				fallback,
				"a runtime error usually means the policy doesn't guard against this input; tell the policy's owners",
			),
		}
	case errors.As(err, &conflictErr):
		return failure{
			kind:   telemetry.ErrorKindConflict,
			status: http.StatusInternalServerError,
			herr: humane.Wrap(err,
				fmt.Sprintf("%s produced decisions that can't stand together: %s", policyName, conflictErr.Message),
				fallback,
				"a conflict is a defect in the policy, not in the request; tell the policy's owners",
			),
			conflict: conflictResult(conflictErr),
		}
	default:
		return failure{status: http.StatusInternalServerError, herr: humane.Wrap(err, "evaluating "+policyName+" failed",
			"retry the request; if it keeps failing, check the deploygate logs")}
	}
}

// assertFailure classifies a failed assert: the caller's input when no rule
// had fired, 422, and the policy's outcome when one had, 500. See classify.
func assertFailure(policyName string, err error, assertErr *policy.AssertionError, fired bool, fallback string) failure {
	asserts := assertResults(assertErr)
	reasons := make([]string, 0, len(asserts))
	for _, a := range asserts {
		reasons = append(reasons, a.Reason)
	}
	list := strings.Join(reasons, ", ")

	if !fired {
		return failure{
			kind:   telemetry.ErrorKindAssertion,
			status: http.StatusUnprocessableEntity,
			herr: humane.Wrap(err,
				fmt.Sprintf("the request fails %s's asserts: %s", policyName, list),
				fallback,
				"fix the request so the asserts listed in asserts hold, then ask again",
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
			"an outcome assert checks what the policy decided, which the request can't change; tell the policy's owners",
		),
		asserts: asserts,
	}
}

// decodeJSON reads the body into a T strictly: unknown fields, trailing data
// and numeric durations are errors, so a typo in a field name is reported
// instead of silently evaluating a zero value. advice says what a valid body
// looks like.
func decodeJSON[T any](c *gin.Context, advice string) (*T, humane.Error) {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	dec.DisallowUnknownFields()

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
		return humane.Wrap(err, fmt.Sprintf("the request body is larger than %d bytes", maxErr.Limit), advice)
	}

	return humane.Wrap(err, "the request body isn't a valid request: "+err.Error(), advice,
		"field names are case-sensitive and unknown fields are rejected")
}

// newKindPolicies renders one kind's snapshot.
func newKindPolicies[In any](snap *store.Snapshot[In]) KindPolicies {
	if snap == nil {
		return KindPolicies{Policies: []PolicyResponse{}}
	}
	out := KindPolicies{
		Kind:     snap.Kind,
		Version:  snap.KindVersion,
		LoadedAt: snap.LoadedAt.UTC(),
		Source:   snap.Source,
		Policies: make([]PolicyResponse, 0, len(snap.Roots)),
	}
	for _, r := range snap.Roots {
		out.Policies = append(out.Policies, PolicyResponse{Team: r.Team, Policy: r.Policy})
	}
	return out
}

// errNotLoaded is the answer while a bundle hasn't loaded yet.
func errNotLoaded() humane.Error {
	return humane.New("no policy bundle is loaded yet",
		"wait until GET /readyz reports ready",
		"if it never does, the deploygate logs name the policy that fails to compile")
}

// logLevel is the level a classified evaluation failure is logged at: a
// warning when it's the caller's, a 4xx, and an error when it's the
// policy's, a 5xx, the same line writeError draws.
func (f failure) logLevel() zapcore.Level {
	if f.status >= http.StatusInternalServerError {
		return zapcore.ErrorLevel
	}
	return zapcore.WarnLevel
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
