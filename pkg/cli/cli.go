// Package cli builds the sigil command line into a host's own binary.
//
// The stock sigil binary reads the kind from its exported file, so it can
// check, explain and format policies, and evaluate them as long as no host
// function is called: it has only the functions' signatures. A host that
// wants eval and sigil test to call its real functions, and to decode
// inputs into its own Go types, links its kind into a binary of its own:
//
//	package main
//
//	import (
//		"github.com/spechtlabs/sigil/pkg/cli"
//
//		"example.com/deploy"
//	)
//
//	//go:generate go run . export --out ../../policies/deploy_approval.sigil
//
//	func main() {
//		cli.Main(cli.WithKind(deploy.Kind))
//	}
//
// That binary is the whole sigil CLI, with every subcommand of the stock
// one. Commands use the linked kind without --kind, and `sigil export`
// writes its kind file, which is what the go:generate line above keeps
// current. A --kind file for a linked kind must match it exactly, which
// catches a stale export.
//
// The command reference is at https://sigil.specht-labs.de/reference/cli/.
package cli

import (
	"context"
	"os"

	"github.com/spechtlabs/sigil/cmd/sigil/command"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Option configures the binary [Main] runs. Build one with [WithKind] or
// [WithVersion].
type Option = command.Option //nolint:optionspattern // an alias of command.Option, which is the func(*options) type the rule asks for

// Main runs the sigil command line with os.Args and exits: with status 1
// when the command failed, after printing why, and 0 otherwise. An
// interrupt or SIGTERM cancels the running command. Main doesn't return.
func Main(opts ...Option) {
	os.Exit(command.Execute(context.Background(), command.NewCommand(opts...), nil))
}

// WithKind links k into the binary. `sigil eval` and `sigil test` then
// decode inputs into k's Go input type and call its real host functions,
// and every command uses k without --kind. A nil k is ignored.
//
// Repeat it for a host with several kinds. Commands then pick one by its
// kind file, passed with --kind and matched by the kind's name, and
// `sigil export` takes the kind's name as its argument.
func WithKind[In any](k *policy.Kind[In]) Option { return command.WithKind(k) }

// WithVersion sets the version `sigil version` reports, typically set at
// build time with -ldflags "-X main.version=...". When it is empty, the
// main module's version from the Go build info is reported instead.
func WithVersion(version string) Option { return command.WithVersion(version) }
