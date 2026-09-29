// Package usage turns the ways a command line can be wrong into errors
// that say what was expected: which arguments a command takes, which
// flag is missing or misspelled, and how to see its help.
//
// [None], [Exactly], [AtLeast] and [AtMost] are the argument validators
// commands set as their Args. [Humanize] rewrites the usage errors cobra
// and pflag return; the Execute functions of sigil and devtool pass every
// error through it before printing.
package usage

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// None returns a validator for a command that takes no arguments.
func None() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return humane.New(fmt.Sprintf("%s takes no arguments, got %d", name(cmd), len(args)), helpAdvice(cmd)...)
	}
}

// Exactly returns a validator for a command that takes one argument per
// name, in that order.
func Exactly(names ...string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == len(names) {
			return nil
		}
		return humane.New(fmt.Sprintf("%s needs %s, got %d", name(cmd), list(names), len(args)), helpAdvice(cmd)...)
	}
}

// AtLeast returns a validator for a command that takes n or more
// arguments called what.
func AtLeast(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}
		return humane.New(fmt.Sprintf("%s needs at least %s", name(cmd), number(n, what)), helpAdvice(cmd)...)
	}
}

// AtMost returns a validator for a command that takes up to n arguments
// called what.
func AtMost(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) <= n {
			return nil
		}
		return humane.New(fmt.Sprintf("%s takes at most %s, got %d", name(cmd), number(n, what), len(args)), helpAdvice(cmd)...)
	}
}

// Humanize translates the usage errors cobra and pflag produce into ones
// that name the command and say how to fix the call. args are the
// command-line arguments the run was given, which select the command the
// error is about. Any other [humane.Error] is returned as it is, and any
// other error is wrapped with the command's usage and how to see its
// help. A nil err returns nil.
func Humanize(root *cobra.Command, args []string, err error) humane.Error {
	if err == nil {
		return nil
	}
	cmd := root
	if found, _, ferr := root.Find(args); ferr == nil && found != nil {
		cmd = found
	}
	// A flag's own error wraps the humane one its value returned, so the
	// flag errors come before the passthrough.
	if translated := translateFlag(cmd, err); translated != nil {
		return translated
	}
	if he, ok := errors.AsType[humane.Error](err); ok {
		return he
	}
	if translated := translate(cmd, err); translated != nil {
		return translated
	}
	return humane.Wrap(err, err.Error(), helpAdvice(cmd)...)
}

// translateFlag rewrites the typed errors pflag reports, or returns nil.
func translateFlag(cmd *cobra.Command, err error) humane.Error {
	var notExist *pflag.NotExistError
	var valueRequired *pflag.ValueRequiredError
	var invalidValue *pflag.InvalidValueError
	switch {
	case errors.As(err, &notExist):
		return unknownFlag(cmd, notExist)
	case errors.As(err, &valueRequired):
		flag := valueRequired.GetFlag()
		var advice []string
		if flag != nil {
			advice = append(advice, "--"+flag.Name+" takes "+describe(flag))
		}
		return humane.New("--"+flagName(flag, valueRequired.GetSpecifiedName())+" needs a value", append(advice, helpAdvice(cmd)...)...)
	case errors.As(err, &invalidValue):
		return invalidFlagValue(cmd, invalidValue)
	}
	return nil
}

// name is how a message refers to the command: its path without the
// program's name, `gen go`.
func name(cmd *cobra.Command) string {
	_, rest, ok := strings.Cut(cmd.CommandPath(), " ")
	if !ok {
		return cmd.Name()
	}
	return rest
}

var (
	unknownCommand = regexp.MustCompile(`^unknown command "([^"]*)" for "([^"]*)"`)
	requiredFlags  = regexp.MustCompile(`^required flag\(s\) "(.*)" not set$`)
	exclusiveFlags = regexp.MustCompile(`^if any flags in the group \[[^\]]*\] are set none of the others can be; \[([^\]]*)\] were all set$`)
)

// translate rewrites the usage errors cobra reports as plain text, or
// returns nil.
func translate(cmd *cobra.Command, err error) humane.Error {
	text := err.Error()
	if m := unknownCommand.FindStringSubmatch(text); m != nil {
		return unknownSubcommand(cmd, m[1], m[2], text)
	}
	if m := requiredFlags.FindStringSubmatch(text); m != nil {
		names := strings.Split(m[1], `", "`)
		advice := make([]string, 0, len(names)+1)
		for _, n := range names {
			if f := cmd.Flags().Lookup(n); f != nil {
				advice = append(advice, "--"+n+" takes "+describe(f))
			}
		}
		verb := "is"
		if len(names) > 1 {
			verb = "are"
		}
		return humane.New(flags(names)+" "+verb+" required", append(advice, helpAdvice(cmd)...)...)
	}
	if m := exclusiveFlags.FindStringSubmatch(text); m != nil {
		return humane.New(flags(strings.Fields(m[1]))+" can't be combined", "pass one of them", helpAdvice(cmd)[0])
	}
	return nil
}

// unknownFlag explains a flag the command doesn't have, suggesting the
// closest one it does.
func unknownFlag(cmd *cobra.Command, e *pflag.NotExistError) humane.Error {
	name := e.GetSpecifiedName()
	spelled := "--" + name
	if short := e.GetSpecifiedShortnames(); short != "" {
		spelled = "-" + name[:1]
	}
	advice := []string{}
	if s := closestFlag(cmd, name); s != "" {
		advice = append(advice, "did you mean --"+s+"?")
	}
	return humane.New("unknown flag "+spelled, append(advice, helpAdvice(cmd)...)...)
}

// invalidFlagValue explains a value a flag rejected, keeping the advice
// the flag's own error gives.
func invalidFlagValue(cmd *cobra.Command, e *pflag.InvalidValueError) humane.Error {
	name := "--" + e.GetFlag().Name
	var advice []string
	if he, ok := errors.AsType[humane.Error](e); ok {
		advice = he.Advice()
	} else if cause := errors.Unwrap(e); cause != nil {
		advice = []string{cause.Error()}
	}
	return humane.New(fmt.Sprintf("%q isn't a valid value for %s", e.GetValue(), name), append(advice, helpAdvice(cmd)...)...)
}

// unknownSubcommand explains a command that doesn't exist, keeping
// cobra's suggestions.
func unknownSubcommand(cmd *cobra.Command, name, parent, text string) humane.Error {
	var advice []string
	if _, after, ok := strings.Cut(text, "Did you mean this?\n"); ok {
		var names []string
		for s := range strings.SplitSeq(strings.TrimSpace(after), "\n") {
			names = append(names, parent+" "+strings.TrimSpace(s))
		}
		advice = append(advice, "did you mean "+list(names)+"?")
	}
	msg := fmt.Sprintf("%s has no command %q", parent, name)
	if cmd.Parent() == nil {
		msg = fmt.Sprintf("unknown command %q", name)
	}
	return humane.New(msg, append(advice, "run `"+parent+" --help` to list the commands")...)
}

// helpAdvice says how the command is called and how to see its help.
func helpAdvice(cmd *cobra.Command) []string {
	return []string{
		"usage: " + cmd.UseLine(),
		"run `" + cmd.CommandPath() + " --help` for details",
	}
}

// describe says what a flag's value is, from its usage text.
func describe(f *pflag.Flag) string {
	usage := strings.TrimSuffix(f.Usage, " (required)")
	if usage == "" {
		return "a " + f.Value.Type()
	}
	return "the " + lowerFirst(usage)
}

func flagName(f *pflag.Flag, specified string) string {
	if f != nil {
		return f.Name
	}
	return specified
}

// closestFlag returns the command's flag whose name is closest to name,
// when one is close enough to be a typo.
func closestFlag(cmd *cobra.Command, name string) string {
	best, bestDist := "", 3
	visit := func(f *pflag.Flag) {
		if d := distance(name, f.Name); d < bestDist || (d == bestDist && best == "") {
			best, bestDist = f.Name, d
		}
	}
	cmd.Flags().VisitAll(visit)
	cmd.InheritedFlags().VisitAll(visit)
	if bestDist >= 3 {
		return ""
	}
	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func flags(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "--" + n
	}
	return list(out)
}

// list joins names for prose: `a`, `a and b`, `a, b and c`.
func list(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// number spells `one PATH`, `2 PATHs`.
func number(n int, name string) string {
	if n == 1 {
		return "one " + name
	}
	return fmt.Sprintf("%d %ss", n, name)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
