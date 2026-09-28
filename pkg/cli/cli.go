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
//	func main() {
//		cli.Main(cli.WithKind(deploy.Kind))
//	}
//
// That binary is the whole sigil CLI. Commands use the linked kind without
// --kind, and `sigil export` writes its kind file.
package cli

import (
	"context"
	"os"

	"github.com/spechtlabs/sigil/cmd/sigil/command"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Option configures the binary.
type Option = command.Option //nolint:optionspattern // an alias of command.Option, which is the func(*options) type the rule asks for

// Main runs the sigil command line with os.Args and exits: with status 1
// when the command failed, after printing why.
func Main(opts ...Option) {
	os.Exit(command.Execute(context.Background(), command.NewCommand(opts...)))
}

// WithKind links a kind into the binary. Repeat it for a host with
// several kinds; commands then pick one with --kind.
func WithKind[In any](k *policy.Kind[In]) Option { return command.WithKind(k) }

// WithVersion sets the version `sigil version` reports.
func WithVersion(version string) Option { return command.WithVersion(version) }
