// Package dashboards holds the Grafana dashboards the compose stack
// provisions. Grafana loads them as they are, so this test is what catches a
// hand edit that breaks the JSON or stacks one panel on another.
package dashboards

import (
	"encoding/json"
	"os"
	"testing"
)

// gridWidth is the width of Grafana's dashboard grid, in columns.
const gridWidth = 24

// panel is what the test reads of a dashboard panel. A row carries its
// panels inline only while it's collapsed, and then their positions are the
// ones they take once it opens, so they'd overlap the panels below the row;
// every row here stays open, and the test fails when one doesn't.
type panel struct {
	Title   string  `json:"title"`
	Type    string  `json:"type"`
	Panels  []panel `json:"panels"`
	GridPos struct {
		X int `json:"x"`
		Y int `json:"y"`
		W int `json:"w"`
		H int `json:"h"`
	} `json:"gridPos"`
	ID int `json:"id"`
}

func TestDashboardLayout(t *testing.T) {
	data, err := os.ReadFile("deploygate.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		UID    string  `json:"uid"`
		Panels []panel `json:"panels"`
	}
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("deploygate.json isn't valid JSON: %v", err)
	}
	if dashboard.UID != "deploygate" {
		t.Errorf("uid = %q, want deploygate, the README links to /d/deploygate", dashboard.UID)
	}

	panels := dashboard.Panels
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

// overlap reports whether two panels share a grid cell.
func overlap(a, b panel) bool {
	ag, bg := a.GridPos, b.GridPos
	return ag.X < bg.X+bg.W && bg.X < ag.X+ag.W && ag.Y < bg.Y+bg.H && bg.Y < ag.Y+ag.H
}
