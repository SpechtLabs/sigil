// Package fixture is what the integration and end-to-end suites share: the
// API's wire types as a client sees them, the alerts both suites send, the
// routes they expect, an HTTP client and helpers for reading metrics.
//
// It is an ordinary package rather than a _test.go file because test files
// can't be imported across packages, and the two suites live in two. It sits
// under test/internal so nothing outside the tests can depend on it.
//
// [FiringAlert] builds single-alert requests, adjusted by [Mutator] values
// such as [FiringFor], and [NewWebhook] builds Alertmanager batches from
// [WebhookAlert] values. [RouteCases], [WebhookCases] and the bad-request
// tables say what the service must answer, [ManifestCases] reads the same
// expectations k6 checks from requests/cases.json, and the Expect helpers,
// such as [ExpectRoute], check an answer against them. [Families] reads
// metrics from a registry's Gather or from the text /metrics serves, so both
// suites assert on the same series.
package fixture

import (
	"encoding/json"
	"errors"
	"time"
)

// The request bodies. The suites keep their own wire types instead of
// importing the server's: durations travel as strings ("12m"), and a
// black-box test should break when the wire format changes, not follow it
// silently.
type (
	// RouteRequest is the body of POST /api/v1/teams/{team}/route.
	RouteRequest struct {
		Alert Alert `json:"alert"`
	}

	// Alert is one alert as the single-alert endpoint takes it. FiringFor
	// is a duration string.
	Alert struct {
		Labels    map[string]string `json:"labels"`
		Name      string            `json:"name"`
		Severity  string            `json:"severity"`
		FiringFor string            `json:"firing_for"`
	}

	// Webhook is the body of POST /api/v1/alerts: Alertmanager's webhook
	// payload, version 4, with the field names Alertmanager sends.
	Webhook struct {
		GroupLabels       map[string]string `json:"groupLabels"`
		CommonLabels      map[string]string `json:"commonLabels"`
		CommonAnnotations map[string]string `json:"commonAnnotations"`
		Version           string            `json:"version"`
		GroupKey          string            `json:"groupKey"`
		Status            string            `json:"status"`
		Receiver          string            `json:"receiver"`
		ExternalURL       string            `json:"externalURL"`
		Alerts            []WebhookAlert    `json:"alerts"`
		TruncatedAlerts   int               `json:"truncatedAlerts"`
	}

	// WebhookAlert is one alert of a webhook batch. The router reads the
	// alertname, severity and team labels, and how long ago StartsAt was.
	WebhookAlert struct {
		StartsAt     time.Time         `json:"startsAt"`
		EndsAt       time.Time         `json:"endsAt"`
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		Status       string            `json:"status"`
		GeneratorURL string            `json:"generatorURL"`
		Fingerprint  string            `json:"fingerprint"`
	}
)

// The response bodies.
type (
	// RouteResponse is the body of a single-alert route, including the 503
	// of an evaluation that failed or ran out of time, which carries the
	// fallback decision.
	RouteResponse struct {
		Error    *ErrorBody   `json:"error"`
		Conflict *Conflict    `json:"conflict"`
		Team     string       `json:"team"`
		Policy   string       `json:"policy"`
		Decision string       `json:"decision"`
		Reason   string       `json:"reason"`
		Target   string       `json:"target"`
		Channel  string       `json:"channel"`
		Trace    []TraceEntry `json:"trace"`
	}

	// WebhookResponse is the body of a processed webhook: how many alerts
	// arrived, how many a policy routed, and what became of each.
	WebhookResponse struct {
		Results  []AlertResult `json:"results"`
		Received int           `json:"received"`
		Routed   int           `json:"routed"`
	}

	// AlertResult is what became of one alert of a batch: its status and,
	// for every firing alert, the route it took. Error is a plain message,
	// set for an unowned, invalid or failed alert, and Conflict explains a
	// failed one that fired two routes the kind can't choose between.
	AlertResult struct {
		Conflict    *Conflict    `json:"conflict"`
		Error       string       `json:"error"`
		Fingerprint string       `json:"fingerprint"`
		AlertName   string       `json:"alertname"`
		Status      string       `json:"status"`
		Team        string       `json:"team"`
		Policy      string       `json:"policy"`
		Decision    string       `json:"decision"`
		Reason      string       `json:"reason"`
		Target      string       `json:"target"`
		Channel     string       `json:"channel"`
		Trace       []TraceEntry `json:"trace"`
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
	// successful reload: one entry per kind the service serves, which for
	// alertrouter is AlertRouting alone. It has deploygate's shape, so a
	// client reads both services the same way.
	PoliciesResponse struct {
		Kinds []KindPolicies `json:"kinds"`
	}

	// KindPolicies is what one kind's bundle holds right now.
	KindPolicies struct {
		LoadedAt    time.Time   `json:"loaded_at"`
		Kind        string      `json:"kind"`
		Source      string      `json:"source"`
		Fingerprint string      `json:"fingerprint"`
		Policies    []PolicyRef `json:"policies"`
		Version     int         `json:"version"`
	}

	// PolicyRef is one served root policy and the team it serves.
	PolicyRef struct {
		Team   string `json:"team"`
		Policy string `json:"policy"`
	}

	// TeamsResponse is the body of GET /api/v1/teams: the team directory.
	TeamsResponse struct {
		Teams []Team `json:"teams"`
	}

	// Team is one entry of the team directory.
	Team struct {
		Name    string `json:"name"`
		Oncall  string `json:"oncall"`
		Channel string `json:"channel"`
	}
)

// Winners returns the trace entries marked as the outcome.
func (r RouteResponse) Winners() []TraceEntry {
	return winners(r.Trace)
}

// Route returns the part of the result a single-alert route answers with,
// so one expectation checks both. A result's error is a plain
// message, which [ExpectResult] checks on its own.
func (r AlertResult) Route() RouteResponse {
	return RouteResponse{
		Conflict: r.Conflict,
		Team:     r.Team,
		Policy:   r.Policy,
		Decision: r.Decision,
		Reason:   r.Reason,
		Target:   r.Target,
		Channel:  r.Channel,
		Trace:    r.Trace,
	}
}

// Routing returns the AlertRouting entry, and false when the service
// doesn't list it.
func (r PoliciesResponse) Routing() (KindPolicies, bool) {
	for _, k := range r.Kinds {
		if k.Kind == KindRouting {
			return k, true
		}
	}
	return KindPolicies{}, false
}

// Result returns the result for the alert with fingerprint, and false when
// the response has none.
func (r WebhookResponse) Result(fingerprint string) (AlertResult, bool) {
	for _, res := range r.Results {
		if res.Fingerprint == fingerprint {
			return res, true
		}
	}
	return AlertResult{}, false
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

// ErrorMessages returns the message of err and of every error it wraps,
// the Go-side counterpart of [ErrorBody.Messages], for a spec that calls the
// store directly.
func ErrorMessages(err error) []string {
	var out []string
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		out = append(out, cur.Error())
	}
	return out
}

func winners(trace []TraceEntry) []TraceEntry {
	var out []TraceEntry
	for _, c := range trace {
		if c.Winner {
			out = append(out, c)
		}
	}
	return out
}
