// Package request embeds the walkthrough's requests, and what alertrouter
// must answer to each. Every JSON file but cases.json is a request body: a
// RouteRequest for POST /api/v1/teams/:team/route, or an Alertmanager
// webhook for POST /api/v1/alerts. cases.json names each one, the team it
// is for and the expected outcome, and it is the one source of truth for
// every consumer: demo-cli's scenarios, the integration and end-to-end
// suites and the k6 load test all check alertrouter against it. The
// package's own test evaluates every case against the policies, so a case
// can't expect what the policies don't decide.
package request

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// CasesFile is the manifest in [Files] that [Cases] reads.
const CasesFile = "cases.json"

// The kinds of case, which say which endpoint a case's file is sent to.
const (
	// KindRoute is a RouteRequest, sent to POST /api/v1/teams/:team/route.
	KindRoute = "route"
	// KindWebhook is an Alertmanager webhook, sent to POST /api/v1/alerts.
	KindWebhook = "webhook"
)

// The statuses a webhook result can have, as alertrouter reports them.
const (
	StatusRouted   = "routed"
	StatusResolved = "resolved"
	StatusUnowned  = "unowned"
	StatusInvalid  = "invalid"
	StatusFailed   = "failed"
)

// Files contains the request bodies and the manifest. Embedding them lets
// demo-cli run from any working directory.
//
//go:embed *.json
var Files embed.FS

// Case is one request and what alertrouter must answer to it.
type Case struct {
	// Name is the scenario's name, what demo-cli takes on the command line.
	Name string `json:"name"`
	// File is the request body in [Files].
	File string `json:"file"`
	// Kind is [KindRoute] or [KindWebhook].
	Kind string `json:"kind"`
	// Team is the team a route request is sent for, and empty for a
	// webhook, whose alerts name their teams in their labels.
	Team string `json:"team,omitempty"`
	// Description is one line for demo-cli's listing and a spec's name.
	Description string `json:"description"`
	// HTTPStatus is the status alertrouter answers with, and zero for 200;
	// see [Case.StatusCode]. A case that isn't 200 expects no Outcome: 404
	// for a team the directory doesn't list, 422 for an alert the router
	// can't read.
	HTTPStatus int `json:"status,omitempty"`
	// Expect is what alertrouter answers.
	Expect Expect `json:"expect"`
}

// Outcome is a routing decision as the wire carries it. Target is set for a
// page only, and Channel for a notification only.
type Outcome struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Target   string `json:"target,omitempty"`
	Channel  string `json:"channel,omitempty"`
}

// Expect is the expected answer. A route case that answers 200 sets the
// Outcome; a webhook case sets Received, Routed and one Result per alert, in
// the webhook's order.
type Expect struct {
	Outcome

	Results []Result `json:"results,omitempty"`
	// Received is how many alerts the webhook carried, firing or resolved.
	Received int `json:"received,omitempty"`
	// Routed is how many of them a team's policy decided, the results
	// with the status routed.
	Routed int `json:"routed,omitempty"`
}

// Result is the expected answer for one alert of a webhook. A resolved
// alert has no Outcome. Team is empty when the check shouldn't compare it,
// as for an alert whose team label names no known team.
type Result struct {
	Outcome

	Fingerprint string `json:"fingerprint"`
	AlertName   string `json:"alertname"`
	Status      string `json:"status"`
	Team        string `json:"team,omitempty"`
}

// Cases returns every case in [CasesFile], in the manifest's order, which is
// the walkthrough's.
func Cases() ([]Case, humane.Error) {
	data, err := Files.ReadFile(CasesFile)
	if err != nil {
		return nil, humane.Wrap(err, "cannot read "+CasesFile, "check that requests/"+CasesFile+" exists; it is embedded at build time")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var cases []Case
	if err := dec.Decode(&cases); err != nil {
		return nil, humane.Wrap(err, CasesFile+" isn't a valid list of cases",
			"each case takes name, file, kind, team, description and expect, as the other cases do")
	}
	return cases, nil
}

// StatusCode is the HTTP status alertrouter answers the case with: 200
// unless the case sets another.
func (c Case) StatusCode() int {
	if c.HTTPStatus == 0 {
		return http.StatusOK
	}
	return c.HTTPStatus
}

// Body returns the case's request body from [Files].
func (c Case) Body() ([]byte, humane.Error) {
	body, err := Files.ReadFile(c.File)
	if err != nil {
		return nil, humane.Wrap(err, fmt.Sprintf("case %s names the file %s, which isn't embedded", c.Name, c.File),
			"add the file to requests/ or fix the case's file in "+CasesFile)
	}
	return body, nil
}
