// Package config is deploygate's command line: the cobra commands, their
// flags, and the DEPLOYGATE_* environment variables viper binds to them. It
// builds everything in constructors instead of init functions, so a test can
// create as many independent command trees as it needs.
//
// [NewRootCommand] builds the tree: `deploygate serve`, which resolves a
// [Config] with [Load] and hands it to a [RunFunc], `deploygate healthcheck`,
// which calls [Probe] on the address [ReadyzURL] derives from --addr, and
// `deploygate version`. The service itself stays out of this package, so its
// tests check flag and environment handling without starting a server.
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

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
)

// EnvPrefix is the prefix of every environment variable deploygate reads,
// DEPLOYGATE_ADDR for --addr and so on.
const EnvPrefix = "DEPLOYGATE"

// The viper keys, which are also the flag names. The environment variable of
// a key is EnvPrefix, an underscore and the key upper-cased with dashes as
// underscores. teams is the exception: its flag is the singular --team,
// because it repeats.
const (
	keyAddr                  = "addr"
	keyPolicies              = "policies"
	keyAccessPolicies        = "access-policies"
	keyTeams                 = "teams"
	keyReloadInterval        = "reload-interval"
	keyShutdownTimeout       = "shutdown-timeout"
	keyEvaluationTimeout     = "evaluation-timeout"
	keyDebug                 = "debug"
	keyLogFormat             = "log-format"
	keyFreezeEnvironments    = "freeze-environments"
	keyFreezeOFREPURL        = "freeze-ofrep-url"
	keyFreezeFlag            = "freeze-flag"
	keyFreezeContext         = "freeze-context"
	keyFreezeRefreshInterval = "freeze-refresh-interval"
	keyFreezeMaxStaleness    = "freeze-max-staleness"
	flagTeam                 = "team"
)

// Defaults for the serve flags. They are what the Dockerfile and the compose
// stack rely on when nothing is set.
var (
	// DefaultTeams are the teams whose policies ship with the example.
	DefaultTeams = []string{"payments", "checkout"}
)

// The defaults that are single values, one per serve flag: --addr,
// --reload-interval, --shutdown-timeout, --evaluation-timeout,
// --log-format, --freeze-flag, --freeze-refresh-interval and
// --freeze-max-staleness. The freeze defaults are the freeze package's.
const (
	DefaultAddr                  = ":8080"
	DefaultReloadInterval        = 30 * time.Second
	DefaultShutdownTimeout       = 15 * time.Second
	DefaultEvaluationTimeout     = time.Second
	DefaultLogFormat             = "json"
	DefaultFreezeFlag            = freeze.DefaultFlag
	DefaultFreezeRefreshInterval = freeze.DefaultRefreshInterval
	DefaultFreezeMaxStaleness    = freeze.DefaultMaxStaleness
)

// Config is the resolved configuration of `deploygate serve`.
type Config struct {
	// Addr is the listen address of the one HTTP port: API, health and
	// metrics.
	Addr string
	// PoliciesDir is the directory holding the team policies. Empty means the
	// team bundle embedded in the binary.
	PoliciesDir string
	// AccessPoliciesDir is the directory holding the access bundle, whose
	// root is access.main. Empty means the access bundle embedded in the
	// binary. It reloads the same way as the team policies.
	AccessPoliciesDir string
	// Teams are the teams served; team t evaluates policy t.production.
	Teams []string
	// ReloadInterval is how often both policies directories are polled for
	// changes. Zero disables polling; SIGHUP and the reload endpoint still
	// work.
	ReloadInterval time.Duration
	// ShutdownTimeout bounds the graceful shutdown.
	ShutdownTimeout time.Duration
	// EvaluationTimeout bounds each policy evaluation, the access stage and
	// the deploy stage each on its own. An evaluation still running at the
	// deadline stops and the request answers 503 with the fallback decision.
	EvaluationTimeout time.Duration
	// Debug turns on debug logging and gin's debug mode.
	Debug bool
	// LogFormat is "json" or "console".
	LogFormat string

	// FreezeEnvironments are the environments frozen for as long as the
	// process runs, when no flag service is configured. Empty freezes
	// nothing.
	FreezeEnvironments []string
	// FreezeOFREPURL is the base URL of the OFREP flag service the freeze
	// is read from, such as http://flagd:8016. Empty means the freeze
	// is FreezeEnvironments. Setting both is an error.
	FreezeOFREPURL string
	// FreezeFlag is the key of the flag that lists the frozen environments.
	FreezeFlag string
	// FreezeContext are attributes of the evaluation context the flag is
	// evaluated with, next to the targetingKey deploygate, which they may
	// override.
	FreezeContext map[string]string
	// FreezeRefreshInterval is how often the flag is evaluated.
	FreezeRefreshInterval time.Duration
	// FreezeMaxStaleness is how old the last answer may get before the
	// freeze is unknown and every deploy is denied as frozen. It must be
	// longer than FreezeRefreshInterval.
	FreezeMaxStaleness time.Duration
}

// RunFunc is what `deploygate serve` runs once the configuration is resolved.
// It returns when the service stopped; ctx is the command's context.
type RunFunc func(ctx context.Context, cfg Config) error

// NewRootCommand builds the deploygate command tree: `serve`, which resolves
// the configuration and hands it to run, `healthcheck`, which probes the
// local server's readiness, and `version`, which prints version. Keeping run
// a parameter keeps this package free of the service's dependencies and lets
// tests check flag handling without starting anything.
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
// defaults, and checks it with [Config.Validate]. The team list is split on
// commas and deduplicated, so DEPLOYGATE_TEAMS=payments,checkout and two
// --team flags give the same teams, and so are the frozen environments and
// the freeze's context attributes. It returns the zero Config with the error
// when a setting can't work.
func Load(v *viper.Viper) (Config, humane.Error) {
	cfg := Config{
		Addr:              v.GetString(keyAddr),
		PoliciesDir:       v.GetString(keyPolicies),
		AccessPoliciesDir: v.GetString(keyAccessPolicies),
		Teams:             splitList(v.GetStringSlice(keyTeams)),
		ReloadInterval:    v.GetDuration(keyReloadInterval),
		ShutdownTimeout:   v.GetDuration(keyShutdownTimeout),
		EvaluationTimeout: v.GetDuration(keyEvaluationTimeout),
		Debug:             v.GetBool(keyDebug),
		LogFormat:         v.GetString(keyLogFormat),

		FreezeEnvironments:    splitList(v.GetStringSlice(keyFreezeEnvironments)),
		FreezeOFREPURL:        v.GetString(keyFreezeOFREPURL),
		FreezeFlag:            v.GetString(keyFreezeFlag),
		FreezeRefreshInterval: v.GetDuration(keyFreezeRefreshInterval),
		FreezeMaxStaleness:    v.GetDuration(keyFreezeMaxStaleness),
	}

	freezeContext, err := splitPairs(v.GetStringSlice(keyFreezeContext))
	if err != nil {
		return Config{}, err
	}
	cfg.FreezeContext = freezeContext

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
	if c.EvaluationTimeout <= 0 {
		return humane.New("the evaluation timeout "+c.EvaluationTimeout.String()+" isn't positive",
			"set --evaluation-timeout to a positive duration such as 1s; an evaluation without a deadline could hold a request for as long as its input makes it run")
	}
	if c.LogFormat != "json" && c.LogFormat != "console" {
		return humane.New("unknown log format "+c.LogFormat, "set --log-format to json or console")
	}
	return c.validateFreeze()
}

// FreezeOptions are the options of the OFREP freeze source the configuration
// describes, for [freeze.NewOFREP] with FreezeOFREPURL.
func (c Config) FreezeOptions() []freeze.Option { //nolint:optionspattern // returns the freeze package's options built from the configuration; it isn't an option itself
	return []freeze.Option{
		freeze.WithFlag(c.FreezeFlag),
		freeze.WithEvaluationContext(c.FreezeContext),
		freeze.WithRefreshInterval(c.FreezeRefreshInterval),
		freeze.WithMaxStaleness(c.FreezeMaxStaleness),
	}
}

// newServeCommand builds `deploygate serve` with its own viper instance, so
// two command trees never share configuration.
func newServeCommand(run RunFunc) *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the deploy-approval API",
		Long: `Serve the deploy-approval API, health checks and metrics on one port.

A deployment request is decided in two stages: the access policy, access.main,
grants the requestor roles for the team, and the team's deploy policy decides
with those roles.

Every flag can also be set through an environment variable: DEPLOYGATE_ and
the flag name upper-cased with dashes as underscores, for example
DEPLOYGATE_RELOAD_INTERVAL=1m. --team is DEPLOYGATE_TEAMS, comma-separated.

A change freeze denies every deploy to a frozen environment with
change_freeze. deploygate puts the freeze into each deploy input itself; a
request can't carry one. --freeze-environments freezes a fixed list.
--freeze-ofrep-url reads the list from a flag on an OpenFeature Remote
Evaluation Protocol service instead, refreshed in the background: when the
service hasn't answered for --freeze-max-staleness, the freeze is unknown,
and every deploy is denied as frozen until it answers again.

Tracing follows the standard OpenTelemetry variables: OTEL_EXPORTER_OTLP_ENDPOINT,
OTEL_SERVICE_NAME (default deploygate) and OTEL_TRACES_EXPORTER=none.

SIGHUP reloads the team and access policies; SIGINT and SIGTERM shut down
gracefully.`,
		Example: `  # Serve the policies embedded in the binary
  deploygate serve

  # Serve team policies from a mounted ConfigMap, polling every minute
  deploygate serve --policies /etc/deploygate/policies --reload-interval 1m

  # Serve both bundles from mounted directories
  deploygate serve --policies /etc/deploygate/policies --access-policies /etc/deploygate/access

  # Serve only the payments team, with readable logs
  DEPLOYGATE_TEAMS=payments deploygate serve --log-format console --debug

  # Freeze production, without a flag service
  deploygate serve --freeze-environments production

  # Read the freeze from the change-freeze flag of an OFREP service, in region eu-1
  deploygate serve --freeze-ofrep-url http://flagd:8016 --freeze-context region=eu-1`,
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
	flags.String(keyAccessPolicies, "", "directory holding the access policies (root access.main); empty serves the access policies embedded in the binary")
	flags.StringSlice(flagTeam, DefaultTeams, "team to serve, repeatable; team t evaluates policy t.production")
	flags.Duration(keyReloadInterval, DefaultReloadInterval, "how often to poll the policies directory for changes; 0 disables polling")
	flags.Duration(keyShutdownTimeout, DefaultShutdownTimeout, "how long a graceful shutdown may take")
	flags.Duration(keyEvaluationTimeout, DefaultEvaluationTimeout, "how long one policy evaluation may take before the request answers 503 with the fallback decision")
	flags.Bool(keyDebug, false, "debug logging and gin debug mode")
	flags.String(keyLogFormat, DefaultLogFormat, "log format: json or console")
	flags.StringSlice(keyFreezeEnvironments, nil, "environment frozen for as long as deploygate runs, repeatable; can't be combined with --freeze-ofrep-url")
	flags.String(keyFreezeOFREPURL, "", "base URL of the OFREP flag service the change freeze is read from, such as http://flagd:8016; empty uses --freeze-environments")
	flags.String(keyFreezeFlag, DefaultFreezeFlag, "key of the flag that lists the frozen environments, as a list of strings or one comma-separated string")
	flags.StringSlice(keyFreezeContext, nil, "key=value attribute of the freeze flag's evaluation context, repeatable, such as region=eu-1")
	flags.Duration(keyFreezeRefreshInterval, DefaultFreezeRefreshInterval, "how often to evaluate the freeze flag")
	flags.Duration(keyFreezeMaxStaleness, DefaultFreezeMaxStaleness, "how old the last freeze flag answer may get before the freeze is unknown and every deploy is denied as frozen; longer than --freeze-refresh-interval")

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

// validateFreeze reports a freeze setting that can't work: a fixed list and
// a flag service both set, or flag service settings freeze.NewOFREP refuses.
func (c Config) validateFreeze() humane.Error {
	if c.FreezeOFREPURL == "" {
		return nil
	}
	if len(c.FreezeEnvironments) > 0 {
		return humane.New("the change freeze is set twice, as a fixed list of environments and as a flag service",
			"set --freeze-environments for a fixed freeze or --freeze-ofrep-url to read it from a flag service, not both")
	}
	if _, herr := freeze.NewOFREP(c.FreezeOFREPURL, c.FreezeOptions()...); herr != nil {
		return humane.Wrap(herr, "the change freeze's flag service settings can't work",
			"check --freeze-ofrep-url, --freeze-flag, --freeze-refresh-interval and --freeze-max-staleness")
	}
	return nil
}

// splitList normalizes a repeatable list flag, such as the teams. viper
// splits an environment variable on whitespace, not commas, so
// DEPLOYGATE_TEAMS=payments,checkout arrives as one element; a flag given as
// --team a,b arrives split already. Splitting every element on commas
// handles both, and duplicates are dropped so a team is loaded once.
func splitList(in []string) []string {
	var out []string
	for _, item := range in {
		for elem := range strings.SplitSeq(item, ",") {
			elem = strings.TrimSpace(elem)
			if elem != "" && !slices.Contains(out, elem) {
				out = append(out, elem)
			}
		}
	}
	return out
}

// splitPairs reads key=value pairs from a list flag split like splitList's,
// a later pair overriding an earlier one with the same key. It returns nil
// for no pairs, and an error for an element without a key.
func splitPairs(in []string) (map[string]string, humane.Error) {
	items := splitList(in)
	if len(items) == 0 {
		return nil, nil
	}
	pairs := make(map[string]string, len(items))
	for _, item := range items {
		key, value, _ := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, humane.New("the freeze context attribute "+item+" has no key",
				"set --freeze-context to key=value pairs, such as region=eu-1")
		}
		pairs[key] = strings.TrimSpace(value)
	}
	return pairs, nil
}
