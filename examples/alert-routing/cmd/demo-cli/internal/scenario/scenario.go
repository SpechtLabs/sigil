// Package scenario supplies the walkthrough's named requests: which request
// file each route and webhook scenario sends, and for a route, to which
// team. The scenarios are the cases of requests/cases.json, the manifest
// the test suites and k6 check alertrouter against, and the request bodies
// are embedded by package
// github.com/spechtlabs/sigil/examples/alert-routing/requests.
package scenario

import (
	"fmt"
	"io"
	"os"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/input"
	request "github.com/spechtlabs/sigil/examples/alert-routing/requests"
)

// The kinds of scenario, the commands that take a scenario name.
const (
	Route   = request.KindRoute
	Webhook = request.KindWebhook
)

// maxFileBytes caps a request read with --file. alertrouter refuses bodies
// over 1 MiB, so there is no point sending a larger one.
const maxFileBytes = 1 << 20

// Scenario supplies the metadata for one walkthrough request.
type Scenario struct {
	// Kind is the command the scenario belongs to, Route or Webhook.
	// Name is what the user passes on the command line, Team the team a
	// route targets and empty for a webhook, Description one line for the
	// listing, and File the embedded request body.
	Kind, Name, Team, Description, File string
}

// List returns the available scenarios in walkthrough order, the order of
// requests/cases.json.
func List() ([]Scenario, humane.Error) {
	cases, herr := request.Cases()
	if herr != nil {
		return nil, humane.Wrap(herr, "cannot read the built-in scenarios", "rebuild demo-cli from this checkout")
	}
	out := make([]Scenario, 0, len(cases))
	for _, c := range cases {
		out = append(out, Scenario{Kind: c.Kind, Name: c.Name, Team: c.Team, Description: c.Description, File: c.File})
	}
	return out, nil
}

// Names lists the scenarios of a kind, for help and completion. It lists
// none when the manifest doesn't read, which [List] reports.
func Names(kind string) []string {
	scenarios, _ := List()
	names := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		if s.Kind == kind {
			names = append(names, s.Name)
		}
	}
	return names
}

// Read chooses a scenario or a file and resolves the route's team. With
// file set, it reads that file, or stdin for "-", up to 1 MiB, and returns
// team as given; a route needs team then, and args must be empty.
// Otherwise it returns the body of the scenario named by args[0], or by
// fallback when args is empty, and team, or the scenario's team when team
// is empty. An unknown scenario is an error.
func Read(stdin io.Reader, kind, fallback string, args []string, file, team string) ([]byte, string, humane.Error) {
	if file != "" {
		if len(args) != 0 {
			return nil, "", humane.New("a scenario and --file cannot be used together", "choose a scenario or supply your own request")
		}
		if kind == Route && strings.TrimSpace(team) == "" {
			return nil, "", humane.New("--team is required with --file", "set the team whose policy should route the alert")
		}
		if file != "-" {
			f, err := os.Open(file) // #nosec G304 -- --file deliberately accepts a user-selected path.
			if err != nil {
				return nil, "", humane.Wrap(err, "cannot open request file", "check the --file path")
			}
			defer func() { _ = f.Close() }()
			stdin = f
		}
		body, err := input.ReadLimited(stdin, maxFileBytes)
		return body, team, err
	}
	name := fallback
	if len(args) != 0 {
		name = args[0]
	}
	scenarios, herr := List()
	if herr != nil {
		return nil, "", herr
	}
	for _, s := range scenarios {
		if s.Kind != kind || s.Name != name {
			continue
		}
		body, err := request.Files.ReadFile(s.File)
		if err != nil {
			return nil, "", humane.Wrap(err, "cannot read the built-in scenario", "rebuild demo-cli from this checkout")
		}
		if team == "" {
			team = s.Team
		}
		return body, team, nil
	}
	return nil, "", humane.New(fmt.Sprintf("unknown %s scenario %q", kind, name), "run demo-cli scenario list to list the available scenarios")
}
