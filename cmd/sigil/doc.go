// Command sigil formats, checks, evaluates, explains and tests Sigil
// policies from the command line. It reads the kind the policies are
// written against from the kind file a host exports, so a policy
// repository runs it in CI without the host's Go code.
//
// Install it with Go:
//
//	go install github.com/spechtlabs/sigil/cmd/sigil@latest
//
// # Usage
//
//	sigil <command> [flags] [PATH...]
//
// Every PATH is a file, a directory or "-" for stdin, and the documents
// found in all of them form one bundle, indexed by the names in their
// headers. Each document is checked against the kind its header names,
// found among the paths, in a kind file named with --kind (-k), or linked
// into a host binary; one run can hold documents of several kinds. Every
// command takes two global flags: --output (-o) picks text,
// json or yaml, and --color picks auto, always or never. Every command
// exits with status 0 on success and 1 on any failure.
//
// The commands are:
//
//	fmt         rewrite policy, module and kind files in the canonical style
//	check       parse, type-check and compile policies against a kind, and lint them
//	eval        evaluate a policy against a JSON input and print the trace
//	explain     flatten a policy into its guarded decisions
//	test        run the test cases in *_test.yaml files
//	export      write the kind file of a kind linked into a host binary
//	breaking    compare two kind files for incompatible changes (not implemented yet)
//	gen go      generate typed Go code from a kind file (not implemented yet)
//	lsp         run the Sigil language server (not implemented yet)
//	version     print the version and build information
//	completion  print a shell completion script for bash, fish, powershell or zsh
//	help        print a command's help, as --help (-h) does
//
// The commands not implemented yet are registered, so their help is
// there, but each one only prints an error and exits with status 1.
//
// # Host binaries
//
// A kind file carries each host function's signature but not its
// implementation, so this binary's eval and test fail with a runtime error
// when a rule reaches a host function call. A host that needs its real
// functions builds its own sigil binary with package
// [github.com/spechtlabs/sigil/pkg/cli], which is this whole command line
// with the host's kind linked in:
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
// That binary decodes inputs into the host's own Go types, calls its
// functions, and uses the linked kind with no kind file. This command is the
// same call with no kind linked in: cli.Main(cli.WithVersion(version)).
//
// The full reference, with every flag, output record and error message, is
// at https://sigil.specht-labs.de/reference/cli/.
package main
