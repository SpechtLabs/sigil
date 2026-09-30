// Package route implements `demo-cli route`, which asks a team's policy what
// to do with one alert. It posts a built-in scenario's request, the JSON
// read with --file, or an alert described by --name, --severity, --label
// and --firing-for to /api/v1/teams/{team}/route, and prints the decision
// and, with --explain, the trace.
package route

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/output"
	"github.com/spechtlabs/sigil/examples/alert-routing/cmd/demo-cli/internal/scenario"
)

// defaultScenario is what `demo-cli route` sends without an argument.
const defaultScenario = "checkout-critical"

// alert is the one alert of a route request built from flags. The flags
// are sent as given, the severity and the duration unchecked, so the
// service's own validation is what answers a mistake.
type alert struct {
	Labels    map[string]string `json:"labels"`
	Name      string            `json:"name"`
	Severity  string            `json:"severity"`
	FiringFor string            `json:"firing_for"`
}

// NewCommand returns the route command. Without an argument or --name it
// runs the checkout-critical scenario. --team overrides the scenario's
// team, and --file and --name need it, since a request body doesn't name
// its team.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}
	var file, team, name, severity, firingFor string
	var labels map[string]string
	var explain bool
	names := scenario.Names(scenario.Route)
	cmd := &cobra.Command{
		Use: "route [scenario]", Short: "Ask a team's policy how to route one alert",
		Long: fmt.Sprintf("Run a built-in route scenario, submit your own JSON with --file, or describe the alert with --name and --severity.\nScenarios: %s. Default: %s.",
			strings.Join(names, ", "), defaultScenario),
		Example:           "  demo-cli route checkout-sustained --explain\n  demo-cli route --team payments --name PaymentsLatencyHigh --severity warning --label env=production --firing-for 7m",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cobra.FixedCompletions(names, cobra.ShellCompDirectiveNoFileComp),
		RunE: func(cmd *cobra.Command, args []string) error {
			var body []byte
			var herr humane.Error
			targetTeam := team
			if name != "" {
				body, herr = fromFlags(args, file, team, name, severity, firingFor, labels)
			} else {
				body, targetTeam, herr = scenario.Read(cmd.InOrStdin(), scenario.Route, defaultScenario, args, file, team)
			}
			if herr != nil {
				return herr
			}
			path := "/api/v1/teams/" + url.PathEscape(targetTeam) + "/route"
			status, data, herr := o.client.Do(cmd.Context(), http.MethodPost, path, body)
			if herr != nil {
				return herr
			}
			return output.Print(cmd.OutOrStdout(), status, data, output.Route, *o.json, explain)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "read a request from a JSON file, or - for stdin")
	cmd.Flags().StringVar(&team, "team", "", "target team (required with --file and --name; otherwise the scenario's team)")
	cmd.Flags().StringVar(&name, "name", "", "the alert's name, to describe an alert instead of running a scenario")
	cmd.Flags().StringVar(&severity, "severity", "", "the alert's severity: critical, warning or info")
	cmd.Flags().StringToStringVar(&labels, "label", nil, "an alert label as key=value, repeatable, such as env=production")
	cmd.Flags().StringVar(&firingFor, "firing-for", "0s", "how long the alert has fired, a duration such as 12m")
	cmd.Flags().BoolVar(&explain, "explain", false, "include candidates, conditions and source locations")
	_ = cmd.MarkFlagFilename("file", "json")
	return cmd
}

// fromFlags renders the route request the alert flags describe.
func fromFlags(args []string, file, team, name, severity, firingFor string, labels map[string]string) ([]byte, humane.Error) {
	switch {
	case len(args) != 0 || file != "":
		return nil, humane.New("--name cannot be used with a scenario or --file", "describe the alert with flags, or choose a scenario or a file")
	case strings.TrimSpace(team) == "":
		return nil, humane.New("--team is required with --name", "set the team whose policy should route the alert")
	case severity == "":
		return nil, humane.New("--severity is required with --name", "set --severity to critical, warning or info")
	}
	a := alert{Name: name, Severity: severity, FiringFor: firingFor, Labels: map[string]string{}}
	maps.Copy(a.Labels, labels)
	// A struct of strings and a string map always marshals.
	body, _ := json.Marshal(map[string]alert{"alert": a})
	return body, nil
}
