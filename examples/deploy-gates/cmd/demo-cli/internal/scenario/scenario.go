// Package scenario supplies the walkthrough's embedded request metadata:
// which request file each named deploy and access scenario sends, and to
// which team. The request bodies themselves are embedded by package
// github.com/spechtlabs/sigil/examples/deploy-gates/requests.
package scenario

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/input"
	request "github.com/spechtlabs/sigil/examples/deploy-gates/requests"
)

// Actions with built-in scenarios, the commands that take a scenario name.
const (
	Deploy       = "deploy"
	Access       = "access"
	teamPayments = "payments"
)

// Scenario supplies the metadata for one walkthrough request.
type Scenario struct {
	// Command is the action the scenario belongs to, Deploy or Access.
	// Name is what the user passes on the command line, Team the team the
	// request targets, Description one line for the listing, and File the
	// embedded request body.
	Command, Name, Team, Description, File string
}

var scenarios = []Scenario{
	{Deploy, "owner", teamPayments, "PCI service owner needs review", "owner.json"},
	{Deploy, "short-soak", teamPayments, "Two-hour soak is denied", "short-soak.json"},
	{Deploy, "sre", teamPayments, "On-call SRE gets a 15-minute bake", "sre.json"},
	{Deploy, "unnamed-actor", "checkout", "Missing actor name fails an assert", "unnamed-actor.json"},
	{Access, "member", teamPayments, "Team member gets reader and deployer", "access-member.json"},
	{Access, "outsider", teamPayments, "Outsider gets no grants", "access-outsider.json"},
	{Access, "break-glass-platform", teamPayments, "Admin and release manager conflict", "access-break-glass-platform.json"},
	{Access, "compliance-member", teamPayments, "Auditor and deployer fail separation of duties", "access-compliance-member.json"},
}

// List returns the available scenarios in walkthrough order.
func List() []Scenario { return slices.Clone(scenarios) }

// Names lists the scenarios for an action, for help and completion.
func Names(action string) []string {
	names := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		if s.Command == action {
			names = append(names, s.Name)
		}
	}
	return names
}

// Read chooses a scenario or a file and resolves the deployment team. With
// file set, it reads that file, or stdin for "-", up to 1 MiB, and returns
// team as given; a deploy needs team then, and args must be empty.
// Otherwise it returns the body of the scenario named by args[0], or by
// fallback when args is empty, and team, or the scenario's team when team
// is empty. An unknown scenario is an error.
func Read(stdin io.Reader, action, fallback string, args []string, file, team string) ([]byte, string, humane.Error) {
	if file != "" {
		if len(args) != 0 {
			return nil, "", humane.New("a scenario and --file cannot be used together", "choose a scenario or supply your own request")
		}
		if action == Deploy && strings.TrimSpace(team) == "" {
			return nil, "", humane.New("--team is required with --file", "set the team whose deployment policy should run")
		}
		if file != "-" {
			f, err := os.Open(file) // #nosec G304 -- --file deliberately accepts a user-selected path.
			if err != nil {
				return nil, "", humane.Wrap(err, "cannot open request file", "check the --file path")
			}
			defer func() { _ = f.Close() }()
			stdin = f
		}
		body, err := input.ReadLimited(stdin, 1<<20)
		return body, team, err
	}
	name := fallback
	if len(args) != 0 {
		name = args[0]
	}
	for _, s := range scenarios {
		if s.Command != action || s.Name != name {
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
	return nil, "", humane.New(fmt.Sprintf("unknown %s scenario %q", action, name), "run demo-cli scenarios to list the available scenarios")
}
