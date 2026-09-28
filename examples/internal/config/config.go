// Package config is deploygate's command line: the cobra commands, their
// flags, and the DEPLOYGATE_* environment variables viper binds to them. It
// builds everything in constructors instead of init functions, so a test can
// create as many independent command trees as it needs.
package config

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// EnvPrefix is the prefix of every environment variable deploygate reads,
// DEPLOYGATE_ADDR for --addr and so on.
const EnvPrefix = "DEPLOYGATE"

// The viper keys, which are also the flag names. The environment variable of
// a key is EnvPrefix, an underscore and the key upper-cased with dashes as
// underscores. teams is the exception: its flag is the singular --team,
// because it repeats.
const (
	keyAddr            = "addr"
	keyPolicies        = "policies"
	keyTeams           = "teams"
	keyReloadInterval  = "reload-interval"
	keyShutdownTimeout = "shutdown-timeout"
	keyDebug           = "debug"
	keyLogFormat       = "log-format"
	flagTeam           = "team"
)

// Defaults for the serve flags. They are what the Dockerfile and the compose
// stack rely on when nothing is set.
var (
	// DefaultTeams are the teams whose policies ship with the example.
	DefaultTeams = []string{"payments", "checkout"}
)

// The defaults that are single values.
const (
	DefaultAddr            = ":8080"
	DefaultReloadInterval  = 30 * time.Second
	DefaultShutdownTimeout = 15 * time.Second
	DefaultLogFormat       = "json"
)

// Config is the resolved configuration of `deploygate serve`.
type Config struct {
	// Addr is the listen address of the one HTTP port: API, health and
	// metrics.
	Addr string
	// PoliciesDir is the directory holding the team policies. Empty means the
	// team bundle embedded in the binary.
	PoliciesDir string
	// Teams are the teams served; team t evaluates policy t.production.
	Teams []string
	// ReloadInterval is how often the policies directory is polled for
	// changes. Zero disables polling; SIGHUP and the reload endpoint still
	// work.
	ReloadInterval time.Duration
	// ShutdownTimeout bounds the graceful shutdown.
	ShutdownTimeout time.Duration
	// Debug turns on debug logging and gin's debug mode.
	Debug bool
	// LogFormat is "json" or "console".
	LogFormat string
}

// RunFunc is what `deploygate serve` runs once the configuration is resolved.
// It returns when the service stopped; ctx is the command's context.
type RunFunc func(ctx context.Context, cfg Config) error

// NewRootCommand builds the deploygate command tree: `serve`, which resolves
// the configuration and hands it to run, and `version`, which prints version.
// Keeping run a parameter keeps this package free of the service's
// dependencies and lets tests check flag handling without starting anything.
func NewRootCommand(version string, run RunFunc) *cobra.Command {
	root := &cobra.Command{
		Use:   "deploygate",
		Short: "Deploy approvals decided by Sigil policies",
		Long: `deploygate is a deploy-approval API. Each team writes its deploy policy in
Sigil; the platform's guardrails are built into the binary and every team
policy has to invoke them. Team policies are read from a directory and
reloaded in place when it changes, keeping the last bundle that loaded.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newServeCommand(run), newHealthcheckCommand(), newVersionCommand(version))
	return root
}

// Load resolves the configuration from v, flags over environment over
// defaults, and checks it.
func Load(v *viper.Viper) (Config, humane.Error) {
	cfg := Config{
		Addr:            v.GetString(keyAddr),
		PoliciesDir:     v.GetString(keyPolicies),
		Teams:           splitTeams(v.GetStringSlice(keyTeams)),
		ReloadInterval:  v.GetDuration(keyReloadInterval),
		ShutdownTimeout: v.GetDuration(keyShutdownTimeout),
		Debug:           v.GetBool(keyDebug),
		LogFormat:       v.GetString(keyLogFormat),
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
		return humane.New("the listen address is empty", "set --addr or DEPLOYGATE_ADDR, for example :8080")
	}
	if len(c.Teams) == 0 {
		return humane.New("no team to serve", "set --team or DEPLOYGATE_TEAMS, for example payments,checkout")
	}
	if c.ReloadInterval < 0 {
		return humane.New("the reload interval "+c.ReloadInterval.String()+" is negative",
			"set --reload-interval to a positive duration such as 30s, or 0 to disable polling")
	}
	if c.ShutdownTimeout <= 0 {
		return humane.New("the shutdown timeout "+c.ShutdownTimeout.String()+" isn't positive",
			"set --shutdown-timeout to a positive duration such as 15s")
	}
	if c.LogFormat != "json" && c.LogFormat != "console" {
		return humane.New("unknown log format "+c.LogFormat, "set --log-format to json or console")
	}
	return nil
}

// newServeCommand builds `deploygate serve` with its own viper instance, so
// two command trees never share configuration.
func newServeCommand(run RunFunc) *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the deploy-approval API",
		Long: `Serve the deploy-approval API, health checks and metrics on one port.

Every flag can also be set through an environment variable: DEPLOYGATE_ and
the flag name upper-cased with dashes as underscores, for example
DEPLOYGATE_RELOAD_INTERVAL=1m. --team is DEPLOYGATE_TEAMS, comma-separated.

Tracing follows the standard OpenTelemetry variables: OTEL_EXPORTER_OTLP_ENDPOINT,
OTEL_SERVICE_NAME (default deploygate) and OTEL_TRACES_EXPORTER=none.

SIGHUP reloads the team policies; SIGINT and SIGTERM shut down gracefully.`,
		Example: `  # Serve the policies embedded in the binary
  deploygate serve

  # Serve team policies from a mounted ConfigMap, polling every minute
  deploygate serve --policies /etc/deploygate/policies --reload-interval 1m

  # Serve only the payments team, with readable logs
  DEPLOYGATE_TEAMS=payments deploygate serve --log-format console --debug`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if herr := bind(v, cmd); herr != nil {
				return herr
			}
			cfg, err := Load(v)
			if err != nil {
				return humane.Wrap(err, "deploygate serve can't start with this configuration",
					"run deploygate serve --help for the flags and their environment variables")
			}
			return run(cmd.Context(), cfg)
		},
	}

	flags := cmd.Flags()
	flags.String(keyAddr, DefaultAddr, "HTTP listen address for the API, health checks and metrics")
	flags.String(keyPolicies, "", "directory holding the team policies; empty serves the team policies embedded in the binary")
	flags.StringSlice(flagTeam, DefaultTeams, "team to serve, repeatable; team t evaluates policy t.production")
	flags.Duration(keyReloadInterval, DefaultReloadInterval, "how often to poll the policies directory for changes; 0 disables polling")
	flags.Duration(keyShutdownTimeout, DefaultShutdownTimeout, "how long a graceful shutdown may take")
	flags.Bool(keyDebug, false, "debug logging and gin debug mode")
	flags.String(keyLogFormat, DefaultLogFormat, "log format: json or console")

	return cmd
}

// newVersionCommand builds `deploygate version`.
func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "deploygate %s\n", version)
			return err
		},
	}
}

// bind wires every flag of cmd to v and v to the environment, so each
// command reads DEPLOYGATE_<FLAG> the same way. It runs when the command runs
// rather than when it is built, so building a command tree can't fail.
func bind(v *viper.Viper, cmd *cobra.Command) humane.Error {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	var herr humane.Error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		key := f.Name
		if key == flagTeam {
			key = keyTeams
		}
		if err := v.BindPFlag(key, f); err != nil && herr == nil {
			herr = humane.Wrap(err, "binding the flag --"+f.Name+" failed",
				"this is a bug in deploygate; please report it")
		}
	})
	return herr
}

// splitTeams normalizes the team list. viper splits an environment variable
// on whitespace, not commas, so DEPLOYGATE_TEAMS=payments,checkout arrives as
// one element; a flag given as --team a,b arrives split already. Splitting
// every element on commas handles both, and duplicates are dropped so a team
// is loaded once.
func splitTeams(in []string) []string {
	var teams []string
	for _, item := range in {
		for team := range strings.SplitSeq(item, ",") {
			team = strings.TrimSpace(team)
			if team != "" && !slices.Contains(teams, team) {
				teams = append(teams, team)
			}
		}
	}
	return teams
}
