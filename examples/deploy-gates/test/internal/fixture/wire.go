// Package fixture is what the integration and end-to-end suites share: the
// API's wire types as a client sees them, the requests both suites send, the
// decisions they expect, an HTTP client and helpers for reading metrics.
//
// It is an ordinary package rather than a _test.go file because test files
// can't be imported across packages, and the two suites live in two. It sits
// under test/internal so nothing outside the tests can depend on it.
//
// [OwnerRequest] builds deployment requests, adjusted by [Mutator] values
// such as [Soak], and [AccessFor] builds access requests. [DecisionCases],
// [AccessCases] and the bad-request tables say what the service must answer,
// and the Expect helpers, such as [ExpectDecision], check an answer against
// them. [Families] reads metrics from a registry's Gather or from the text
// /metrics serves, so both suites assert on the same series.
package fixture

import (
	"encoding/json"
	"time"
)

// The request bodies. The suites keep their own wire types instead of
// importing the server's: durations travel as strings ("6h"), and a
// black-box test should break when the wire format changes, not follow it
// silently.
type (
	// DeployRequest is the body of POST /api/v1/teams/{team}/deployments.
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

	// Actor is who asks for the deploy, as the identity provider describes
	// them. There are no roles: the access policy grants them.
	Actor struct {
		Name      string   `json:"name"`
		Clearance string   `json:"clearance"`
		Groups    []string `json:"groups"`
		Regions   []string `json:"regions"`
	}

	// AccessRequest is the body of POST /api/v1/access/grants.
	AccessRequest struct {
		Actor       AccessActor `json:"actor"`
		Team        string      `json:"team"`
		Environment string      `json:"environment"`
	}

	// AccessActor is the requestor as the access policy reads them. Regions
	// only matter to the deploy policy, so the access endpoint doesn't take
	// them.
	AccessActor struct {
		Name      string   `json:"name"`
		Clearance string   `json:"clearance"`
		Groups    []string `json:"groups"`
	}
)

// The response bodies. Payloads stay raw so specs can compare them with
// MatchJSON, which checks the exact shape including duration strings.
type (
	// DecisionResponse is the body of an evaluation, including the 422, 500
	// and 503 of a failed one.
	DecisionResponse struct {
		Error    *ErrorBody      `json:"error"`
		Access   *AccessBlock    `json:"access"`
		Freeze   *Freeze         `json:"freeze"`
		Conflict *Conflict       `json:"conflict"`
		Team     string          `json:"team"`
		Policy   string          `json:"policy"`
		Decision string          `json:"decision"`
		Reason   string          `json:"reason"`
		Payload  json.RawMessage `json:"payload"`
		Trace    []TraceEntry    `json:"trace"`
		Asserts  []AssertEntry   `json:"asserts"`
	}

	// Freeze is the change freeze the deploy policy read, as deploygate's
	// freeze source answered it.
	Freeze struct {
		Environments []string `json:"environments"`
		Unknown      bool     `json:"unknown"`
	}

	// AccessBlock is the access stage of a deployment: the access policy
	// and the roles it granted, which became the deploy policy's
	// actor.roles.
	AccessBlock struct {
		Policy string  `json:"policy"`
		Grants []Grant `json:"grants"`
	}

	// Grant is one role an access evaluation granted. TTL is empty for a
	// role that doesn't expire.
	Grant struct {
		Role     string `json:"role"`
		Reason   string `json:"reason"`
		TTL      string `json:"ttl"`
		Policy   string `json:"policy"`
		Location string `json:"location"`
	}

	// AccessResponse is the body of POST /api/v1/access/grants, including
	// the 403 of an empty outcome and the 422, 500 and 503 of a failed
	// evaluation.
	AccessResponse struct {
		Error       *ErrorBody    `json:"error"`
		Conflict    *Conflict     `json:"conflict"`
		Policy      string        `json:"policy"`
		Team        string        `json:"team"`
		Environment string        `json:"environment"`
		Grants      []Grant       `json:"grants"`
		Trace       []TraceEntry  `json:"trace"`
		Asserts     []AssertEntry `json:"asserts"`
	}

	// Conflict names the candidates the kind says can't stand together.
	Conflict struct {
		Candidates []TraceEntry `json:"candidates"`
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

	// ErrorResponse is the body of every error status that has no decision
	// to report.
	ErrorResponse struct {
		Error *ErrorBody `json:"error"`
	}

	// PoliciesResponse is the body of GET /api/v1/policies and of a
	// successful reload: one entry per kind the service serves.
	PoliciesResponse struct {
		Kinds []KindPolicies `json:"kinds"`
	}

	// KindPolicies is what one kind's bundle holds right now.
	KindPolicies struct {
		LoadedAt time.Time   `json:"loaded_at"`
		Kind     string      `json:"kind"`
		Source   string      `json:"source"`
		Policies []PolicyRef `json:"policies"`
		Version  int         `json:"version"`
	}

	// PolicyRef is one served policy, and the team it serves when it
	// serves one.
	PolicyRef struct {
		Team   string `json:"team,omitempty"`
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

// Kind returns the entry for the named kind, and false when the service
// doesn't list it.
func (r PoliciesResponse) Kind(name string) (KindPolicies, bool) {
	for _, k := range r.Kinds {
		if k.Kind == name {
			return k, true
		}
	}
	return KindPolicies{}, false
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
