// Package dashboards holds the Grafana dashboards the compose stack
// provisions. Grafana loads them as they are, so these tests are what catch a
// hand edit that breaks the JSON, stacks one panel on another, or queries a
// metric alertrouter doesn't export.
package dashboards

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// gridWidth is the width of Grafana's dashboard grid, in columns.
const gridWidth = 24

var (
	// metrics is every series alertrouter exports that the dashboard may
	// query, with the labels it carries, as internal/telemetry registers
	// them. A query for anything else is empty in Grafana without an error,
	// so the test is the only place a renamed metric or label shows up.
	metrics = map[string][]string{
		"alertrouter_alerts_received_total":                {"status"},
		"alertrouter_alerts_routed_total":                  {"team", "outcome"},
		"alertrouter_decisions_total":                      {"team", "policy", "decision", "reason"},
		"alertrouter_evaluation_duration_seconds":          {"team"},
		"alertrouter_evaluation_errors_total":              {"team", "kind"},
		"alertrouter_notifications_total":                  {"decision", "destination"},
		"alertrouter_webhook_batch_size":                   {},
		"alertrouter_policy_reloads_total":                 {"result"},
		"alertrouter_policy_last_reload_timestamp_seconds": {},
		"alertrouter_policy_last_reload_successful":        {},
		"alertrouter_policy_loaded_info":                   {"team", "policy", "fingerprint", "source"},
		"alertrouter_requests_total":                       {"code", "method", "route"},
		"alertrouter_request_duration_seconds":             {"method", "route"},
	}

	// scrapeLabels are the labels every scraped series carries besides its
	// own: Alloy's job and instance, and le on a histogram's buckets.
	scrapeLabels = []string{"job", "instance", "le"}

	// selector matches an alertrouter series in a PromQL expression and its
	// label matchers, if it has any.
	selector = regexp.MustCompile(`\b(alertrouter_[a-z_]+)(\{[^}]*\})?`)

	// matcher matches one label matcher inside a selector's braces.
	matcher = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)`)
)

// dashboard is what the tests read of the dashboard.
type dashboard struct {
	UID    string  `json:"uid"`
	Panels []panel `json:"panels"`
}

// panel is what the tests read of a dashboard panel. A row carries its
// panels inline only while it's collapsed, and then their positions are the
// ones they take once it opens, so they'd overlap the panels below the row;
// every row here stays open, and the test fails when one doesn't.
type panel struct {
	Title   string   `json:"title"`
	Type    string   `json:"type"`
	Panels  []panel  `json:"panels"`
	Targets []target `json:"targets"`
	GridPos struct {
		X int `json:"x"`
		Y int `json:"y"`
		W int `json:"w"`
		H int `json:"h"`
	} `json:"gridPos"`
	ID int `json:"id"`
}

// target is one query of a panel. Only Mimir queries have an expr.
type target struct {
	Expr string `json:"expr"`
}

func TestDashboardLayout(t *testing.T) {
	d := load(t)
	if d.UID != "alertrouter" {
		t.Errorf("uid = %q, want alertrouter, the README links to /d/alertrouter", d.UID)
	}

	panels := d.Panels
	ids := map[int]string{}
	for _, p := range panels {
		if other, ok := ids[p.ID]; ok {
			t.Errorf("panels %q and %q share the id %d", other, p.Title, p.ID)
		}
		ids[p.ID] = p.Title
		if len(p.Panels) > 0 {
			t.Errorf("row %q is collapsed; open it, or teach this test to check the panels inside it", p.Title)
		}

		g := p.GridPos
		if g.W <= 0 || g.H <= 0 || g.X < 0 || g.Y < 0 || g.X+g.W > gridWidth {
			t.Errorf("panel %q has gridPos %+v, outside the %d-column grid", p.Title, g, gridWidth)
		}
	}

	for i, a := range panels {
		for _, b := range panels[i+1:] {
			if overlap(a, b) {
				t.Errorf("panels %q %+v and %q %+v overlap", a.Title, a.GridPos, b.Title, b.GridPos)
			}
		}
	}
}

func TestDashboardMetrics(t *testing.T) {
	d := load(t)

	queried := map[string]bool{}
	for _, p := range d.Panels {
		for _, q := range p.Targets {
			for _, m := range selector.FindAllStringSubmatch(q.Expr, -1) {
				name := series(m[1])
				labels, ok := metrics[name]
				if !ok {
					t.Errorf("panel %q queries %s, which alertrouter doesn't export", p.Title, m[1])
					continue
				}
				queried[name] = true
				for _, l := range matcher.FindAllStringSubmatch(m[2], -1) {
					if !slices.Contains(labels, l[1]) && !slices.Contains(scrapeLabels, l[1]) {
						t.Errorf("panel %q matches label %q on %s, which has only %v", p.Title, l[1], name, labels)
					}
				}
			}
		}
	}

	// A metric no panel shows is one an operator can't see without writing
	// the query themselves.
	for name := range metrics {
		if !queried[name] {
			t.Errorf("no panel queries %s", name)
		}
	}
}

// load reads and decodes alertrouter.json.
func load(t *testing.T) dashboard {
	t.Helper()
	var d dashboard
	data, err := os.ReadFile("alertrouter.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("alertrouter.json isn't valid JSON: %v", err)
	}

	return d
}

// series strips the suffix Prometheus adds to a histogram's series, so a
// query for alertrouter_webhook_batch_size_bucket finds its histogram.
func series(name string) string {
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if base, ok := strings.CutSuffix(name, suffix); ok {
			if _, known := metrics[base]; known {
				return base
			}
		}
	}

	return name
}

// overlap reports whether two panels share a grid cell.
func overlap(a, b panel) bool {
	ag, bg := a.GridPos, b.GridPos
	return ag.X < bg.X+bg.W && bg.X < ag.X+ag.W && ag.Y < bg.Y+bg.H && bg.Y < ag.Y+ag.H
}
