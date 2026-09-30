// Package output renders API responses and preserves failed answers as
// errors.
//
// [Print] writes a response in one of the views, a summary of the route,
// the webhook's results, the policies, the teams or the status, or as
// indented JSON with --json, and only then returns an error for a status
// outside 2xx. The error wraps a [ResponseError], whose exit code tells a
// script that a policy failed and the alert fell back to the kind's default
// from any other failure. The views decode the response into the server's
// own types, so the CLI and the service can't disagree on the wire format.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
)

// Response views used by the command packages, one per kind of response.
// Metrics prints a successful body as it is.
const (
	Route    = "route"
	Webhook  = "webhook"
	Policies = "policies"
	Teams    = "teams"
	Status   = "status"
	Metrics  = "metrics"
)

// ResponseError follows a response already printed to stdout. It must
// remain nonzero in scripts even with --json.
type ResponseError struct {
	status   int
	fellBack bool
}

// Print renders the full response before returning an error for an HTTP
// failure. JSON output stays machine-readable on every status. view is one
// of the view constants, and explain adds the trace to the Route view. The
// error for a status outside 2xx wraps a [*ResponseError].
func Print(out io.Writer, status int, data []byte, view string, asJSON, explain bool) humane.Error {
	rendered, herr := format(status, data, view, asJSON, explain)
	if herr != nil {
		return herr
	}
	if herr := Write(out, rendered); herr != nil {
		return herr
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		err := &ResponseError{status: status, fellBack: fellBack(status, data)}
		return humane.Wrap(err, err.Error(), "read the response for the decision or diagnostics")
	}
	return nil
}

// Error implements the error interface. It names the HTTP status.
func (e *ResponseError) Error() string {
	return fmt.Sprintf("alertrouter answered HTTP %d %s", e.status, http.StatusText(e.status))
}

// ExitCode returns 2 when the policy's evaluation failed and the alert fell
// back to the kind's default: 422 when the alert failed an input assert,
// 500 when the policy failed, 503 when it ran out of time. Every other HTTP
// failure returns 1. See [fellBack].
func (e *ResponseError) ExitCode() int {
	if e.fellBack {
		return 2
	}
	return 1
}

// Write writes data to out and returns an error with advice when the write
// fails.
func Write(out io.Writer, data []byte) humane.Error {
	if _, err := out.Write(data); err != nil {
		return humane.Wrap(err, "cannot write the response", "check the output destination")
	}
	return nil
}

// fellBack reports whether a response routed the alert with the fallback
// decision: a 422, 500 or 503 whose body names the policy that failed,
// which a failed evaluation's body does and an error without a decision,
// such as a severity the kind doesn't declare or a bundle that isn't loaded
// yet, doesn't.
func fellBack(status int, data []byte) bool {
	if status != http.StatusUnprocessableEntity && status != http.StatusInternalServerError && status != http.StatusServiceUnavailable {
		return false
	}
	var body struct {
		Policy string `json:"policy"`
	}
	return json.Unmarshal(data, &body) == nil && body.Policy != ""
}

func format(status int, data []byte, view string, asJSON, explain bool) ([]byte, humane.Error) {
	switch {
	case view == Metrics && status == http.StatusOK:
		return data, nil
	case asJSON:
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err != nil {
			return nil, humane.Wrap(err, fmt.Sprintf("alertrouter returned HTTP %d with an invalid JSON response", status), "check --url points to alertrouter")
		}
		return append(bytes.TrimSpace(pretty.Bytes()), '\n'), nil
	default:
		return renderResponse(status, data, view, explain)
	}
}

func renderResponse(status int, data []byte, view string, explain bool) ([]byte, humane.Error) {
	var envelope server.ErrorEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, humane.Wrap(err, fmt.Sprintf("alertrouter returned HTTP %d with an invalid JSON response", status), "check --url points to alertrouter")
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "HTTP %d %s\n", status, http.StatusText(status))
	var err error
	switch view {
	case Route:
		err = renderRoute(&out, data, explain)
	case Webhook:
		err = renderWebhook(&out, data)
	case Policies:
		err = renderPolicies(&out, data)
	case Teams:
		err = renderTeams(&out, data)
	case Status:
		var response server.StatusResponse
		err = json.Unmarshal(data, &response)
		if response.Status != "" {
			fmt.Fprintf(&out, "alertrouter: %s\n", response.Status)
		}
	}
	if err != nil {
		return nil, humane.Wrap(err, "cannot decode the alertrouter response", "check that the CLI and service are built from the same checkout")
	}
	for cause := envelope.Error; cause != nil; cause = cause.Cause {
		fmt.Fprintf(&out, "\n%s\n", cause.Message)
		for _, advice := range cause.Advice {
			fmt.Fprintf(&out, "  %s\n", advice)
		}
	}
	return out.Bytes(), nil
}

func renderRoute(out *bytes.Buffer, data []byte, explain bool) error {
	var response server.RouteResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return humane.Wrap(err, "cannot decode the response fields", "check that the CLI and service are built from the same checkout")
	}
	if response.Decision == "" {
		return nil
	}
	fmt.Fprintf(out, "%s: %s\nTeam: %s\nPolicy: %s\n", strings.ToUpper(response.Decision), response.Reason, response.Team, response.Policy)
	if response.Target != "" {
		fmt.Fprintf(out, "Target: %s\n", response.Target)
	}
	if response.Channel != "" {
		fmt.Fprintf(out, "Channel: %s\n", response.Channel)
	}
	renderFailures(out, response.Asserts, response.Conflict)
	if explain {
		renderTrace(out, response.Trace)
	}
	return nil
}

func renderWebhook(out *bytes.Buffer, data []byte) error {
	var response server.WebhookResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return humane.Wrap(err, "cannot decode the response fields", "check that the CLI and service are built from the same checkout")
	}
	if response.Results == nil {
		return nil
	}
	fmt.Fprintf(out, "Received %d, routed %d by a team's policy\n\n", response.Received, response.Routed)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STATUS\tALERT\tTEAM\tDECISION\tDESTINATION")
	var failures []string
	for _, result := range response.Results {
		team, decision, destination := "-", "-", "-"
		if r := result.RouteResponse; r != nil {
			team = orDash(r.Team)
			if r.Decision != "" {
				decision = r.Decision + ": " + r.Reason
			}
			destination = orDash(r.Target + r.Channel)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", result.Status, result.AlertName, team, decision, destination)
		if result.Error != "" {
			failures = append(failures, fmt.Sprintf("  %s (%s): %s", result.AlertName, result.Fingerprint, result.Error))
		}
	}
	_ = w.Flush()
	if len(failures) != 0 {
		fmt.Fprintf(out, "\nNot routed by a policy:\n%s\n", strings.Join(failures, "\n"))
	}
	return nil
}

func renderFailures(out *bytes.Buffer, asserts []server.AssertResult, conflict *server.ConflictResult) {
	for _, assertion := range asserts {
		fmt.Fprintf(out, "\nFailed assert: %s\n  %s\n", assertion.Reason, assertion.Location)
		if assertion.Cause != "" {
			fmt.Fprintf(out, "  %s\n", assertion.Cause)
		}
	}
	if conflict != nil {
		fmt.Fprintln(out, "\nConflicting candidates:")
		for _, candidate := range conflict.Candidates {
			fmt.Fprintf(out, "  %s: %s\n    %s\n", candidate.Decision, candidate.Reason, candidate.Location)
		}
	}
}

func renderTrace(out *bytes.Buffer, candidates []server.CandidateResult) {
	fmt.Fprintln(out, "\nTrace:")
	if len(candidates) == 0 {
		fmt.Fprintln(out, "  No candidates.")
	}
	for _, candidate := range candidates {
		label := "candidate"
		if candidate.Winner {
			label = "winner"
		}
		fmt.Fprintf(out, "  [%s] %s: %s\n    Policy: %s\n    %s\n", label, candidate.Decision, candidate.Reason, candidate.Policy, candidate.Location)
		for _, condition := range candidate.Conditions {
			fmt.Fprintf(out, "    when %s\n", condition)
		}
		if len(candidate.Payload) != 0 {
			fmt.Fprintf(out, "    with %s\n", payloadValue(candidate.Payload))
		}
	}
}

func renderPolicies(out *bytes.Buffer, data []byte) error {
	var response server.PoliciesResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return humane.Wrap(err, "cannot decode the response fields", "check that the CLI and service are built from the same checkout")
	}
	for _, kind := range response.Kinds {
		fmt.Fprintf(out, "\n%s@%d\n  Source: %s\n  Fingerprint: %s\n  Loaded: %s\n",
			kind.Kind, kind.Version, kind.Source, kind.Fingerprint, kind.LoadedAt.Format(time.RFC3339))
		for _, policy := range kind.Policies {
			fmt.Fprintf(out, "  %s  team=%s\n", policy.Policy, policy.Team)
		}
	}
	return nil
}

func renderTeams(out *bytes.Buffer, data []byte) error {
	var response server.TeamsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return humane.Wrap(err, "cannot decode the response fields", "check that the CLI and service are built from the same checkout")
	}
	if response.Teams == nil {
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TEAM\tON-CALL\tCHANNEL")
	for _, team := range response.Teams {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", team.Name, team.Oncall, team.Channel)
	}
	return w.Flush()
}

// payloadValue only receives values decoded from JSON, so marshaling cannot fail.
func payloadValue(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
