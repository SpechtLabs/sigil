// Command devtool is sigil's development tooling: benchmark comparisons and
// fuzz campaigns. It isn't released; run it from the repository with
//
//	go run ./cmd/devtool <command> [flags]
//
// or as `mise run devtool -- <command>`. The mise tasks bench, bench-smoke
// and fuzz and the CI workflows call it the same way. The commands are:
//
//	bench run    measure the benchmarks, or compare them with a base revision
//	bench list   list the benchmarks bench run would run
//	fuzz run     fuzz every fuzz target, one at a time
//	fuzz list    list the fuzz targets fuzz run would run
//	fuzz report  open or update the issue for a failed extended fuzz campaign
//
// Every flag of the run and list commands can also be set with an
// environment variable, BENCH_<FLAG> or FUZZ_<FLAG>, e.g. BENCH_BASELINE=main
// or FUZZ_TIME=1m. A flag on the command line wins. Each command's --help
// describes its flags.
package main

import (
	"context"
	"os"

	"github.com/spechtlabs/sigil/cmd/devtool/command"
)

func main() {
	os.Exit(command.Execute(context.Background(), command.NewCommand(), nil))
}
