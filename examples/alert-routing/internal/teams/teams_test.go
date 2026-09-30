package teams_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
)

// The embedded directory's teams, as the policies' tests and the request
// cases expect them.
var (
	checkout = routing.Team{Name: "checkout", Oncall: "checkout-primary", Channel: "#checkout-alerts"}
	payments = routing.Team{Name: "payments", Oncall: "payments-primary", Channel: "#payments-alerts"}
)

func TestDefault(t *testing.T) {
	d := teams.Default()

	if got := d.Source(); got != teams.DefaultSource {
		t.Errorf("Source() = %q, want %q", got, teams.DefaultSource)
	}
	if got, want := d.Names(), []string{"checkout", "payments"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if got, want := d.Teams(), []routing.Team{checkout, payments}; !slices.Equal(got, want) {
		t.Errorf("Teams() = %v, want %v", got, want)
	}
}

func TestLookup(t *testing.T) {
	d := teams.Default()

	tests := []struct {
		name   string
		want   routing.Team
		wantOK bool
	}{
		{name: "checkout", want: checkout, wantOK: true},
		{name: "payments", want: payments, wantOK: true},
		{name: "search"},
		{name: ""},
		{name: "Checkout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := d.Lookup(tt.name)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Lookup(%q) = %+v, %v, want %+v, %v", tt.name, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestNamesIsACopy guards the directory's immutability: a server shares one
// directory between requests, so a caller's append or sort must not reach it.
func TestNamesIsACopy(t *testing.T) {
	d := teams.Default()
	d.Names()[0] = "changed"
	d.Teams()[0].Oncall = "changed"

	if got := d.Names()[0]; got != "checkout" {
		t.Errorf("Names()[0] = %q after the caller changed its copy", got)
	}
	if got, _ := d.Lookup("checkout"); got != checkout {
		t.Errorf("Lookup(checkout) = %+v after the caller changed its copy", got)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want are the teams of a directory that loads, sorted by name.
		want []routing.Team
		// wantErr and wantAdvice are substrings of the error's message and
		// advice when it doesn't.
		wantErr    string
		wantAdvice string
	}{
		{
			name: "teams sorted by name",
			src: `teams:
  - {name: payments, oncall: payments-primary, channel: "#payments-alerts"}
  - {name: checkout, oncall: checkout-primary, channel: "#checkout-alerts"}
`,
			want: []routing.Team{checkout, payments},
		},
		{
			name: "a name with digits and underscores",
			src:  `teams: [{name: team_2, oncall: t2, channel: "#t2"}]`,
			want: []routing.Team{{Name: "team_2", Oncall: "t2", Channel: "#t2"}},
		},
		{
			name:       "a misspelled team key",
			src:        `teams: [{name: checkout, oncal: checkout-primary, channel: "#c"}]`,
			wantErr:    `test.yaml:1:26: unknown key "oncal" in a team`,
			wantAdvice: `did you mean "oncall"?`,
		},
		{
			name:       "a misspelled top-level key",
			src:        `team: []`,
			wantErr:    `test.yaml:1:1: unknown key "team" in the directory`,
			wantAdvice: `did you mean "teams"?`,
		},
		{
			name:       "an unknown key nothing is close to",
			src:        `teams: [{name: checkout, oncall: c, channel: "#c", escalation: sre}]`,
			wantErr:    `unknown key "escalation"`,
			wantAdvice: "the keys are name, oncall, channel",
		},
		{
			name:       "a key set twice",
			src:        `teams: [{name: checkout, oncall: a, oncall: b, channel: "#c"}]`,
			wantErr:    `the key "oncall" is set twice in a team`,
			wantAdvice: "keep one of them",
		},
		{
			name:       "invalid YAML",
			src:        "teams: [",
			wantErr:    "isn't valid YAML",
			wantAdvice: "teams:",
		},
		{
			name:    "an empty file",
			src:     "",
			wantErr: "test.yaml is empty",
		},
		{
			name:    "a list at the top level",
			src:     `[checkout]`,
			wantErr: "the directory must be a map with the keys teams",
		},
		{
			name:    "no teams",
			src:     `teams: []`,
			wantErr: "lists no teams",
		},
		{
			name:    "an empty map",
			src:     `{}`,
			wantErr: "lists no teams",
		},
		{
			name:    "teams as a map",
			src:     `teams: {checkout: {}}`,
			wantErr: "lists no teams",
		},
		{
			name:    "a team that isn't a map",
			src:     `teams: [checkout]`,
			wantErr: "test.yaml:1:9: a team must be a map",
		},
		{
			name:    "a team with a list for a name",
			src:     `teams: [{name: [a], oncall: a, channel: "#a"}]`,
			wantErr: "test.yaml:1:9: the team isn't a map of strings",
		},
		{
			name:       "a team without a name",
			src:        `teams: [{oncall: a, channel: "#a"}]`,
			wantErr:    "a team has no name",
			wantAdvice: "team label",
		},
		{
			name:       "a name that isn't an identifier",
			src:        `teams: [{name: check-out, oncall: a, channel: "#a"}]`,
			wantErr:    `the team name "check-out" isn't a lower-case identifier`,
			wantAdvice: "<team>.alerts",
		},
		{
			name:    "an upper-case name",
			src:     `teams: [{name: Checkout, oncall: a, channel: "#a"}]`,
			wantErr: `the team name "Checkout" isn't a lower-case identifier`,
		},
		{
			name:       "a team without an oncall",
			src:        `teams: [{name: checkout, channel: "#a"}]`,
			wantErr:    "team checkout has no oncall",
			wantAdvice: "pages no one",
		},
		{
			name:       "a channel without #",
			src:        `teams: [{name: checkout, oncall: a, channel: checkout-alerts}]`,
			wantErr:    `team checkout has the channel "checkout-alerts", which doesn't start with #`,
			wantAdvice: "quoted",
		},
		{
			// An unquoted # starts a YAML comment, so the channel is empty.
			name:    "an unquoted channel",
			src:     "teams:\n  - name: checkout\n    oncall: a\n    channel: #checkout-alerts\n",
			wantErr: `team checkout has the channel ""`,
		},
		{
			name: "a team listed twice",
			src: `teams:
  - {name: checkout, oncall: a, channel: "#a"}
  - {name: checkout, oncall: b, channel: "#b"}
`,
			wantErr:    "test.yaml:3:5: team checkout is listed twice",
			wantAdvice: "merge the two entries",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := teams.Load(strings.NewReader(tt.src), "test.yaml")
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load() = %v, want an error containing %q", d.Names(), tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.wantAdvice) {
					t.Errorf("advice = %q, want it to contain %q", advice, tt.wantAdvice)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() error = %s", err.Display())
			}
			if got := d.Teams(); !slices.Equal(got, tt.want) {
				t.Errorf("Teams() = %+v, want %+v", got, tt.want)
			}
			if got := d.Source(); got != "test.yaml" {
				t.Errorf("Source() = %q, want test.yaml", got)
			}
		})
	}
}

func TestLoadReadError(t *testing.T) {
	cause := errors.New("disk on fire")
	_, err := teams.Load(iotest.ErrReader(cause), "broken.yaml")
	if err == nil {
		t.Fatal("Load() succeeded on a failing reader")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error = %v, want it to wrap %v", err, cause)
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "teams.yaml")
	if err := os.WriteFile(path, []byte(`teams: [{name: search, oncall: search-primary, channel: "#search"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	d, herr := teams.LoadFile(path)
	if herr != nil {
		t.Fatalf("LoadFile() error = %s", herr.Display())
	}
	if got := d.Source(); got != path {
		t.Errorf("Source() = %q, want %q", got, path)
	}
	if got, want := d.Names(), []string{"search"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if _, herr := teams.LoadFile(missing); herr == nil || !errors.Is(herr, os.ErrNotExist) {
		t.Errorf("LoadFile(missing) error = %v, want one wrapping os.ErrNotExist", herr)
	}
}
