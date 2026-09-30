package request_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/alertmanager"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/policies"
	request "github.com/spechtlabs/sigil/examples/alert-routing/requests"
)

// fallback is the kind's default decision, which an alert gets when no
// policy runs for it: notify(reason: unrouted) with the payload's default
// channel.
var fallback = request.Outcome{Decision: routing.Notify.Name(), Reason: routing.Unrouted.Name(), Channel: routing.DefaultChannel}

// TestCases evaluates every case in cases.json against the embedded
// policies and team directory, the way alertrouter routes it, so the
// manifest every suite and the load test check against can't drift from
// what the policies decide. It covers the policies, not the service: the
// integration suite sends the same cases through the API.
func TestCases(t *testing.T) {
	cases, herr := request.Cases()
	if herr != nil {
		t.Fatal(herr.Display())
	}
	if len(cases) == 0 {
		t.Fatal("cases.json holds no cases")
	}

	r := newRouter(t)
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if seen[c.Name] {
				t.Fatalf("case %s is listed twice", c.Name)
			}
			seen[c.Name] = true

			body, herr := c.Body()
			if herr != nil {
				t.Fatal(herr.Display())
			}

			switch c.Kind {
			case request.KindRoute:
				r.checkRoute(t, c, body)
			case request.KindWebhook:
				r.checkWebhook(t, c, body)
			default:
				t.Fatalf("case %s has the kind %q, want route or webhook", c.Name, c.Kind)
			}
		})
	}
}

// TestEveryFileHasACase fails when a request file isn't in the manifest,
// which would leave it unchecked by every suite.
func TestEveryFileHasACase(t *testing.T) {
	cases, herr := request.Cases()
	if herr != nil {
		t.Fatal(herr.Display())
	}
	listed := map[string]bool{}
	for _, c := range cases {
		listed[c.File] = true
	}

	entries, err := request.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != request.CasesFile && !listed[e.Name()] {
			t.Errorf("%s has no case in %s", e.Name(), request.CasesFile)
		}
	}
}

// TestBodyMissingFile checks that a case naming a file that isn't embedded
// says which case and file, rather than failing a suite with a bare
// file-not-found.
func TestBodyMissingFile(t *testing.T) {
	c := request.Case{Name: "ghost", File: "ghost.json"}
	_, herr := c.Body()
	if herr == nil {
		t.Fatal("Body() succeeded for a file that isn't embedded")
	}
	if want := "case ghost names the file ghost.json"; !strings.Contains(herr.Error(), want) {
		t.Errorf("Body() error = %q, want it to contain %q", herr.Error(), want)
	}
}

// router is the routing half of alertrouter without the HTTP around it:
// the team directory and one policy per team.
type router struct {
	dir      *teams.Directory
	policies map[string]*policy.Policy[routing.Input]
}

func newRouter(t *testing.T) *router {
	t.Helper()

	r := &router{dir: teams.Default(), policies: map[string]*policy.Policy[routing.Input]{}}
	for _, name := range r.dir.Names() {
		p, err := routing.Kind.Load(policies.Teams, name+".alerts",
			policy.Require("platform.paging", policy.From(policies.Platform)))
		if err != nil {
			t.Fatalf("load %s.alerts: %v", name, err)
		}
		r.policies[name] = p
	}
	return r
}

// checkRoute decodes a RouteRequest strictly, as the API does, and routes it
// for the case's team.
func (r *router) checkRoute(t *testing.T, c request.Case, body []byte) {
	t.Helper()

	var req struct {
		Alert struct {
			Labels    map[string]string `json:"labels"`
			Name      string            `json:"name"`
			Severity  string            `json:"severity"`
			FiringFor string            `json:"firing_for"`
		} `json:"alert"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("%s isn't a RouteRequest: %v", c.File, err)
	}

	// The status the API answers before it evaluates anything: 404 for a
	// team the directory doesn't list, 422 for an alert it can't read.
	status := http.StatusOK
	_, known := r.dir.Lookup(c.Team)
	severity, sevOK := routing.ParseSeverity(req.Alert.Severity)
	firingFor, durErr := time.ParseDuration(req.Alert.FiringFor)
	switch {
	case !known:
		status = http.StatusNotFound
	case !sevOK || durErr != nil || firingFor < 0:
		status = http.StatusUnprocessableEntity
	}

	if status != c.StatusCode() {
		t.Fatalf("%s for team %q answers %d, the case expects %d", c.File, c.Team, status, c.StatusCode())
	}
	if status != http.StatusOK {
		if c.Expect.Outcome != (request.Outcome{}) {
			t.Errorf("the case answers %d and expects the outcome %+v, which the API doesn't send", status, c.Expect.Outcome)
		}
		return
	}

	got := r.route(t, c.Team, routing.Alert{
		Name: req.Alert.Name, Severity: severity, Labels: req.Alert.Labels, FiringFor: firingFor,
	})
	if got != c.Expect.Outcome {
		t.Errorf("%s routes to %+v, the case expects %+v", c.File, got, c.Expect.Outcome)
	}
}

// checkWebhook routes every alert of a webhook the way the API does:
// resolved alerts aren't evaluated, and an alert without a known team or
// with a label the router can't read gets the kind's default.
func (r *router) checkWebhook(t *testing.T, c request.Case, body []byte) {
	t.Helper()

	var w alertmanager.Webhook
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("%s isn't a webhook: %v", c.File, err)
	}
	if herr := w.Validate(); herr != nil {
		t.Fatalf("%s: %s", c.File, herr.Display())
	}
	if c.Expect.Received != len(w.Alerts) {
		t.Errorf("the case expects %d received, %s holds %d alerts", c.Expect.Received, c.File, len(w.Alerts))
	}
	if len(c.Expect.Results) != len(w.Alerts) {
		t.Fatalf("the case expects %d results, %s holds %d alerts", len(c.Expect.Results), c.File, len(w.Alerts))
	}

	if c.StatusCode() != http.StatusOK {
		t.Errorf("the case expects %d, and a webhook that validates answers 200", c.StatusCode())
	}

	now := time.Now()
	routed := 0
	for i, a := range w.Alerts {
		want := c.Expect.Results[i]
		got := r.webhookResult(t, a, now)
		if got != want {
			t.Errorf("alert %d of %s: got %+v, the case expects %+v", i, c.File, got, want)
		}
		if got.Status == request.StatusRouted {
			routed++
		}
	}
	if c.Expect.Routed != routed {
		t.Errorf("the case expects %d routed, %s routes %d", c.Expect.Routed, c.File, routed)
	}
}

// webhookResult is what alertrouter answers for one alert of a webhook.
func (r *router) webhookResult(t *testing.T, a alertmanager.Alert, now time.Time) request.Result {
	t.Helper()

	res := request.Result{Fingerprint: a.Fingerprint, AlertName: a.Labels[alertmanager.LabelAlertName]}
	if !a.Firing() {
		res.Status = request.StatusResolved
		return res
	}

	team := a.Labels[alertmanager.LabelTeam]
	if _, ok := r.dir.Lookup(team); !ok {
		res.Status, res.Outcome = request.StatusUnowned, fallback
		return res
	}
	res.Team = team

	alert, herr := alertmanager.Convert(a, now)
	if herr != nil {
		res.Status, res.Outcome = request.StatusInvalid, fallback
		return res
	}

	res.Status, res.Outcome = request.StatusRouted, r.route(t, team, alert)
	return res
}

// route evaluates the team's policy for alert and renders the result the
// way the wire carries it.
func (r *router) route(t *testing.T, name string, alert routing.Alert) request.Outcome {
	t.Helper()

	team, ok := r.dir.Lookup(name)
	if !ok {
		t.Fatalf("the directory has no team %s", name)
	}
	res, err := r.policies[name].Eval(t.Context(), routing.Input{Alert: alert, Team: team})
	if err != nil {
		t.Fatalf("evaluate %s.alerts: %v", name, err)
	}

	out := request.Outcome{Decision: res.Decision, Reason: res.Reason}
	if page, ok := routing.Page.Match(res); ok {
		out.Target = page.Target
	}
	if note, ok := routing.Notify.Match(res); ok {
		out.Channel = note.Channel
	}
	return out
}
