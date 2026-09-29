// Package output renders API responses and preserves policy refusals as errors.
//
// [Print] writes a response in one of the views, a summary of the decision,
// grants, policies or status, or as indented JSON with --json, and only then
// returns an error for a status outside 2xx. The error wraps a
// [ResponseError], whose exit code tells a script that the policy refused
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
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/internal/server"
)

func renderResponse(status int, data []byte, view string, explain bool) ([]byte, humane.Error) {
	var envelope server.ErrorEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, humane.Wrap(err, fmt.Sprintf("deploygate returned HTTP %d with an invalid JSON response", status), "check --url points to deploygate")
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "HTTP %d %s\n", status, http.StatusText(status))
	var err error
	switch view {
	case Deploy:
		err = renderDeploy(&out, data, explain)
	case Access:
		err = renderAccess(&out, data, explain)
	case "policies":
		err = renderPolicies(&out, data)
	case "status":
		var response server.StatusResponse
		err = json.Unmarshal(data, &response)
		if response.Status != "" {
			fmt.Fprintf(&out, "deploygate: %s\n", response.Status)
		}
	}
	if err != nil {
		return nil, humane.Wrap(err, "cannot decode the deploygate response", "check that the CLI and service are built from the same checkout")
	}
	for cause := envelope.Error; cause != nil; cause = cause.Cause {
		fmt.Fprintf(&out, "\n%s\n", cause.Message)
		for _, advice := range cause.Advice {
			fmt.Fprintf(&out, "  %s\n", advice)
		}
	}
	return out.Bytes(), nil
}

func renderDeploy(out *bytes.Buffer, data []byte, explain bool) error {
	var response server.DecisionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return humane.Wrap(err, "cannot decode the response fields", "check that the CLI and service are built from the same checkout")
	}
	if response.Decision == "" {
		return nil
	}
	fmt.Fprintf(out, "%s: %s\nTeam: %s\nPolicy: %s\n", strings.ToUpper(response.Decision), response.Reason, response.Team, response.Policy)
	if bake, ok := response.Payload["bake"]; ok {
		fmt.Fprintf(out, "Bake: %v\n", bake)
	}
	if approvers, ok := response.Payload["approvers"]; ok {
		fmt.Fprintf(out, "Approvers: %s\n", payloadValue(approvers))
	}
	if response.Access != nil {
		fmt.Fprintf(out, "\nAccess policy: %s\n", response.Access.Policy)
		renderGrants(out, response.Access.Grants)
	}
	renderFailures(out, response.Asserts, response.Conflict)
	if explain {
		renderTrace(out, response.Trace)
	}
	return nil
}

func renderAccess(out *bytes.Buffer, data []byte, explain bool) error {
	var response server.AccessResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return humane.Wrap(err, "cannot decode the response fields", "check that the CLI and service are built from the same checkout")
	}
	if response.Policy == "" {
		return nil
	}
	fmt.Fprintf(out, "Team: %s\nEnvironment: %s\nAccess policy: %s\n", response.Team, response.Environment, response.Policy)
	renderGrants(out, response.Grants)
	renderFailures(out, response.Asserts, response.Conflict)
	if explain {
		renderTrace(out, response.Trace)
	}
	return nil
}

func renderGrants(out *bytes.Buffer, grants []server.GrantResult) {
	if len(grants) == 0 {
		fmt.Fprintln(out, "No roles granted.")
		return
	}
	fmt.Fprintln(out, "Roles:")
	for _, grant := range grants {
		fmt.Fprintf(out, "  %s: %s", grant.Role, grant.Reason)
		if grant.TTL != nil {
			fmt.Fprintf(out, ", expires in %s", grant.TTL.String())
		}
		fmt.Fprintln(out)
	}
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
		fmt.Fprintf(out, "\n%s@%d\n  Source: %s\n  Loaded: %s\n", kind.Kind, kind.Version, kind.Source, kind.LoadedAt.Format(time.RFC3339))
		for _, policy := range kind.Policies {
			fmt.Fprintf(out, "  %s", policy.Policy)
			if policy.Team != "" {
				fmt.Fprintf(out, "  team=%s", policy.Team)
			}
			fmt.Fprintln(out)
		}
	}
	return nil
}

// payloadValue only receives values decoded from JSON, so marshaling cannot fail.
func payloadValue(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// Response views used by the command packages, one per kind of response.
// Metrics prints a successful body as it is.
const (
	Deploy   = "deploy"
	Access   = "access"
	Policies = "policies"
	Status   = "status"
	Metrics  = "metrics"
)

// Print renders the full response before returning an error for a refusal
// or HTTP failure. JSON output stays machine-readable on every status. view
// is one of the view constants, and explain adds the trace to the Deploy and
// Access views. The error for a status outside 2xx wraps a [*ResponseError].
func Print(out io.Writer, status int, data []byte, view string, asJSON, explain bool) humane.Error {
	rendered, herr := format(status, data, view, asJSON, explain)
	if herr != nil {
		return herr
	}
	if herr := Write(out, rendered); herr != nil {
		return herr
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		err := &ResponseError{status: status}
		return humane.Wrap(err, err.Error(), "read the response for the decision or diagnostics")
	}
	return nil
}

// ResponseError follows a response already printed to stdout, including
// policy refusals. These must remain nonzero in scripts even with --json.
type ResponseError struct {
	status int
}

// Error implements the error interface. It names the HTTP status.
func (e *ResponseError) Error() string {
	return fmt.Sprintf("deploygate answered HTTP %d %s", e.status, http.StatusText(e.status))
}

// ExitCode returns 2 for policy refusals, 403, 409 and 422, and 1 for other
// HTTP failures.
func (e *ResponseError) ExitCode() int {
	switch e.status {
	case http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity:
		return 2
	default:
		return 1
	}
}

func format(status int, data []byte, view string, asJSON, explain bool) ([]byte, humane.Error) {
	switch {
	case view == Metrics && status == http.StatusOK:
		return data, nil
	case asJSON:
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err != nil {
			return nil, humane.Wrap(err, fmt.Sprintf("deploygate returned HTTP %d with an invalid JSON response", status), "check --url points to deploygate")
		}
		return append(bytes.TrimSpace(pretty.Bytes()), '\n'), nil
	default:
		return renderResponse(status, data, view, explain)
	}
}

// Write writes data to out and returns an error with advice when the write
// fails.
func Write(out io.Writer, data []byte) humane.Error {
	if _, err := out.Write(data); err != nil {
		return humane.Wrap(err, "cannot write the response", "check the output destination")
	}
	return nil
}
