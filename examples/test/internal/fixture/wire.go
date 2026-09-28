// Package fixture is what the integration and end-to-end suites share: the
// API's wire types as a client sees them, the requests both suites send, the
// decisions they expect, an HTTP client and helpers for reading metrics.
//
// It is an ordinary package rather than a _test.go file because test files
// can't be imported across packages, and the two suites live in two. It sits
// under test/internal so nothing outside the tests can depend on it.
package fixture

import (
	"encoding/json"
	"time"
)

// The request body of POST /api/v1/teams/{team}/deployments. The suites keep
// their own wire types instead of importing the server's: durations travel as
// strings ("6h"), and a black-box test should break when the wire format
// changes, not follow it silently.
type (
	// DeployRequest is one deploy the client asks about.
	DeployRequest struct {
		Release     Release `json:"release"`
		Service     Service `json:"service"`
		Actor       Actor   `json:"actor"`
		Environment string  `json:"environment"`
	}

	// Release is the artifact being shipped. Soak is a duration string.
	Release struct {
		Soak   string `json:"soak"`
		Hotfix bool   `json:"hotfix"`
	}

	// Service is the workload the release belongs to.
	Service struct {
		Labels map[string]string `json:"labels"`
		Name   string            `json:"name"`
		Tier   string            `json:"tier"`
		Owners []string          `json:"owners"`
	}

	// Actor is who asks for the deploy.
	Actor struct {
		Name    string   `json:"name"`
		Teams   []string `json:"teams"`
		Roles   []string `json:"roles"`
		Regions []string `json:"regions"`
	}
)

// The response bodies. Payloads stay raw so specs can compare them with
// MatchJSON, which checks the exact shape including duration strings.
type (
	// DecisionResponse is the body of an evaluation, including the 422 of a
	// failed one.
	DecisionResponse struct {
		Error    *ErrorBody      `json:"error"`
		Team     string          `json:"team"`
		Policy   string          `json:"policy"`
		Decision string          `json:"decision"`
		Reason   string          `json:"reason"`
		Payload  json.RawMessage `json:"payload"`
		Trace    []TraceEntry    `json:"trace"`
		Asserts  []AssertEntry   `json:"asserts"`
	}

	// TraceEntry is one candidate of the trace.
	TraceEntry struct {
		Decision   string          `json:"decision"`
		Reason     string          `json:"reason"`
		Policy     string          `json:"policy"`
		Location   string          `json:"location"`
		Payload    json.RawMessage `json:"payload"`
		Conditions []string        `json:"conditions"`
		Winner     bool            `json:"winner"`
	}

	// AssertEntry is one assert that didn't hold.
	AssertEntry struct {
		Reason   string `json:"reason"`
		Policy   string `json:"policy"`
		Location string `json:"location"`
	}

	// ErrorBody is a humane error as the API renders it.
	ErrorBody struct {
		Cause   *ErrorBody `json:"cause"`
		Message string     `json:"message"`
		Advice  []string   `json:"advice"`
	}

	// ErrorResponse is the body of every error status other than 422.
	ErrorResponse struct {
		Error *ErrorBody `json:"error"`
	}

	// PoliciesResponse is the body of GET /api/v1/policies and of a
	// successful reload.
	PoliciesResponse struct {
		LoadedAt time.Time   `json:"loaded_at"`
		Kind     string      `json:"kind"`
		Source   string      `json:"source"`
		Policies []PolicyRef `json:"policies"`
		Version  int         `json:"version"`
	}

	// PolicyRef is one served team and the policy it evaluates.
	PolicyRef struct {
		Team   string `json:"team"`
		Policy string `json:"policy"`
	}
)

// Winners returns the trace entries marked as the outcome.
func (r DecisionResponse) Winners() []TraceEntry {
	var out []TraceEntry
	for _, c := range r.Trace {
		if c.Winner {
			out = append(out, c)
		}
	}
	return out
}

// Messages returns the message of the error and of every cause below it, so
// a spec can look for a diagnostic wherever in the chain the server put it.
func (e *ErrorBody) Messages() []string {
	var out []string
	for cur := e; cur != nil; cur = cur.Cause {
		out = append(out, cur.Message)
	}
	return out
}
