package gotool

import (
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// BindEnv lets every flag of cmd be set from an environment variable named
// after it: prefix, an underscore, then the flag in capitals with dashes as
// underscores, e.g. BENCH_TIME for --time. A flag given on the command line
// wins. Each flag's help names its variable. Call it after the flags are
// defined; it takes over cmd's PreRunE.
func BindEnv(cmd *cobra.Command, prefix string, getenv func(string) string) {
	vars := map[string]string{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		key := EnvName(prefix, f.Name)
		vars[f.Name] = key
		f.Usage += " (env " + key + ")"
	})
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return EnvDefaults(cmd.Flags(), getenv, vars)
	}
}

// EnvName returns the environment variable BindEnv reads a flag from.
func EnvName(prefix, flag string) string {
	return prefix + "_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// EnvDefaults sets each flag that wasn't given on the command line from its
// environment variable, when that's set.
func EnvDefaults(flags *pflag.FlagSet, getenv func(string) string, vars map[string]string) humane.Error {
	for name, key := range vars {
		value := getenv(key)
		if value == "" || flags.Changed(name) {
			continue
		}
		if err := flags.Set(name, value); err != nil {
			return humane.Wrap(err, "invalid "+key+" value "+value, "unset "+key+" or pass --"+name+" instead")
		}
	}
	return nil
}
