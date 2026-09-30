// Package config is alertrouter's command line: the cobra commands, their
// flags, and the ALERTROUTER_* environment variables viper binds to them. It
// builds everything in constructors instead of init functions, so a test can
// create as many independent command trees as it needs.
//
// [NewRootCommand] builds the tree: `alertrouter serve`, which resolves a
// [Config] with [Load] and hands it to a [RunFunc], `alertrouter
// healthcheck`, which calls [Probe] on the address [ReadyzURL] derives from
// --addr, and `alertrouter version`. The service itself stays out of this
// package, so its tests check flag and environment handling without starting
// a server.
package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// EnvPrefix is the prefix of every environment variable alertrouter reads,
// ALERTROUTER_ADDR for --addr and so on.
const EnvPrefix = "ALERTROUTER"

// The viper keys, which are also the flag names. The environment variable of
// a key is EnvPrefix, an underscore and the key upper-cased with dashes as
// underscores.
const (
	keyAddr              = "addr"
	keyPolicies          = "policies"
	keyTeamsFile         = "teams-file"
	keyReloadInterval    = "reload-interval"
	keyShutdownTimeout   = "shutdown-timeout"
	keyEvaluationTimeout = "evaluation-timeout"
	keyDebug             = "debug"
	keyLogFormat         = "log-format"
)

// The defaults of the serve flags, one per flag that has one: --addr,
// --reload-interval, --shutdown-timeout, --evaluation-timeout and
// --log-format. They are what the Dockerfile and the compose stack rely on
// when nothing is set.
const (
	DefaultAddr              = ":8080"
	DefaultReloadInterval    = 30 * time.Second
	DefaultShutdownTimeout   = 15 * time.Second
	DefaultEvaluationTimeout = time.Second
	DefaultLogFormat         = "json"
)

// Config is the resolved configuration of `alertrouter serve`.
type Config struct {
	// Addr is the listen address of the one HTTP port: API, health and
	// metrics.
	Addr string
	// PoliciesDir is the directory holding the team policies. Empty means the
	// team bundle embedded in the binary.
	PoliciesDir string
	// TeamsFile is the team directory, a teams.yaml naming each team with its
	// on-call target and channel. Empty means the directory embedded in the
	// binary. Every team in it must have a policy, <team>.alerts, in the
	// bundle.
	TeamsFile string
	// ReloadInterval is how often the policies directory is polled for
	// changes. Zero disables polling; SIGHUP and the reload endpoint still
	// work.
	ReloadInterval time.Duration
	// ShutdownTimeout bounds the graceful shutdown.
	ShutdownTimeout time.Duration
	// EvaluationTimeout bounds each alert's evaluation on its own, so one
	// slow alert in a webhook batch can't use up the others' time. An
	// evaluation still running at the deadline stops, and the alert is routed
	// with the fallback decision.
	EvaluationTimeout time.Duration
	// Debug turns on debug logging and gin's debug mode.
	Debug bool
	// LogFormat is "json" or "console".
	LogFormat string
}

// RunFunc is what `alertrouter serve` runs once the configuration is
// resolved. It returns when the service stopped; ctx is the command's
// context.
type RunFunc func(ctx context.Context, cfg Config) error

// NewRootCommand builds the alertrouter command tree: `serve`, which
// resolves the configuration and hands it to run, `healthcheck`, which
// probes the local server's readiness, and `version`, which prints version.
// Keeping run a parameter keeps this package free of the service's
// dependencies and lets tests check flag handling without starting anything.
func NewRootCommand(version string, run RunFunc) *cobra.Command {
	root := &cobra.Command{
		Use:   "alertrouter",
		Short: "Alert routing decided by Sigil policies",
		Long: `alertrouter receives alerts from Alertmanager and asks the owning team's Sigil
policy what to do with each: page someone, post to a channel, or drop it. The
platform's paging rule is built into the binary and every team policy has to
invoke it. Team policies are read from a directory and reloaded in place when
it changes, keeping the last bundle that loaded.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newServeCommand(run), newHealthcheckCommand(), newVersionCommand(version))
	return root
}

// Load resolves the configuration from v, flags over environment over
// defaults, and checks it with [Config.Validate]. It returns the zero Config
// with the error when a setting can't work.
func Load(v *viper.Viper) (Config, humane.Error) {
	cfg := Config{
		Addr:              v.GetString(keyAddr),
		PoliciesDir:       v.GetString(keyPolicies),
		TeamsFile:         v.GetString(keyTeamsFile),
		ReloadInterval:    v.GetDuration(keyReloadInterval),
		ShutdownTimeout:   v.GetDuration(keyShutdownTimeout),
		EvaluationTimeout: v.GetDuration(keyEvaluationTimeout),
		Debug:             v.GetBool(keyDebug),
		LogFormat:         v.GetString(keyLogFormat),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports the first setting that can't work, with advice on what to
// set instead.
func (c Config) Validate() humane.Error {
	if c.Addr == "" {
		return humane.New("the listen address is empty", "set --addr or ALERTROUTER_ADDR, for example :8080")
	}
	if c.ReloadInterval < 0 {
		return humane.New("the reload interval "+c.ReloadInterval.String()+" is negative",
			"set --reload-interval to a positive duration such as 30s, or 0 to disable polling")
	}
	if c.ShutdownTimeout <= 0 {
		return humane.New("the shutdown timeout "+c.ShutdownTimeout.String()+" isn't positive",
			"set --shutdown-timeout to a positive duration such as 15s")
	}
	if c.EvaluationTimeout <= 0 {
		return humane.New("the evaluation timeout "+c.EvaluationTimeout.String()+" isn't positive",
			"set --evaluation-timeout to a positive duration such as 1s; an evaluation without a deadline could hold an alert for as long as its input makes it run")
	}
	if c.LogFormat != "json" && c.LogFormat != "console" {
		return humane.New("unknown log format "+c.LogFormat, "set --log-format to json or console")
	}
	return nil
}

// newServeCommand builds `alertrouter serve` with its own viper instance, so
// two command trees never share configuration.
func newServeCommand(run RunFunc) *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the alert-routing API",
		Long: `Serve the alert-routing API, health checks and metrics on one port.

Each firing alert is routed by the policy of the team its team label names,
<team>.alerts, with the team's on-call target and channel from the team
directory. An alert no team owns, or one the router can't read, goes to the
kind's default, notify(reason: unrouted), so no alert is silently lost.

Every flag can also be set through an environment variable: ALERTROUTER_ and
the flag name upper-cased with dashes as underscores, for example
ALERTROUTER_RELOAD_INTERVAL=1m.

Tracing follows the standard OpenTelemetry variables: OTEL_EXPORTER_OTLP_ENDPOINT,
OTEL_SERVICE_NAME (default alertrouter) and OTEL_TRACES_EXPORTER=none.

SIGHUP reloads the team policies; SIGINT and SIGTERM shut down gracefully.`,
		Example: `  # Serve the policies and team directory embedded in the binary
  alertrouter serve

  # Serve team policies from a mounted ConfigMap, polling every minute
  alertrouter serve --policies /etc/alertrouter/policies --reload-interval 1m

  # Serve your own team directory, with readable logs
  ALERTROUTER_TEAMS_FILE=/etc/alertrouter/teams.yaml alertrouter serve --log-format console --debug`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if herr := bind(v, cmd); herr != nil {
				return herr
			}
			cfg, err := Load(v)
			if err != nil {
				return humane.Wrap(err, "alertrouter serve can't start with this configuration",
					"run alertrouter serve --help for the flags and their environment variables")
			}
			return run(cmd.Context(), cfg)
		},
	}

	flags := cmd.Flags()
	flags.String(keyAddr, DefaultAddr, "HTTP listen address for the API, health checks and metrics")
	flags.String(keyPolicies, "", "directory holding the team policies; empty serves the team policies embedded in the binary")
	flags.String(keyTeamsFile, "", "team directory file (teams.yaml); empty serves the team directory embedded in the binary")
	flags.Duration(keyReloadInterval, DefaultReloadInterval, "how often to poll the policies directory for changes; 0 disables polling")
	flags.Duration(keyShutdownTimeout, DefaultShutdownTimeout, "how long a graceful shutdown may take")
	flags.Duration(keyEvaluationTimeout, DefaultEvaluationTimeout, "how long one alert's evaluation may take before it is routed with the fallback decision")
	flags.Bool(keyDebug, false, "debug logging and gin debug mode")
	flags.String(keyLogFormat, DefaultLogFormat, "log format: json or console")

	return cmd
}

// newVersionCommand builds `alertrouter version`.
func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "alertrouter %s\n", version)
			return err
		},
	}
}

// bind wires every flag of cmd to v and v to the environment, so each
// command reads ALERTROUTER_<FLAG> the same way. It runs when the command
// runs rather than when it is built, so building a command tree can't fail.
func bind(v *viper.Viper, cmd *cobra.Command) humane.Error {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	var herr humane.Error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if err := v.BindPFlag(f.Name, f); err != nil && herr == nil {
			herr = humane.Wrap(err, "binding the flag --"+f.Name+" failed",
				"this is a bug in alertrouter; please report it")
		}
	})
	return herr
}
