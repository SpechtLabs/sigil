package fuzz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/corpus"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// defaultRemote is the remote whose fuzz-corpus branch the commands use.
const defaultRemote = "origin"

// corpusOptions configures `fuzz corpus pull` and `fuzz corpus push`.
type corpusOptions struct {
	filter string
	remote string
	from   string // push only: a directory laid out like the fuzz cache
}

func newCorpusCommand(o options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "corpus",
		Short: "Restore or share the inputs fuzzing found",
		Long: `go test keeps the inputs a fuzz target found interesting in its cache, and
starts the next run from them. The fuzz-corpus branch holds them too, for
every target, so a run on any machine or in CI starts from what every
earlier run found.

fuzz corpus pull copies the branch's inputs into the cache; fuzz run does
that by itself before it fuzzes. fuzz corpus push commits the inputs the
branch doesn't have yet and pushes them. The branch shares no history with
the code, and neither command touches the working tree.`,
	}
	cmd.AddCommand(newPullCommand(o), newPushCommand(o))
	return cmd
}

func newPullCommand(o options) *cobra.Command {
	co := corpusOptions{remote: defaultRemote}
	cmd := &cobra.Command{
		Use:   "pull [PACKAGE...]",
		Short: "Copy the fuzz-corpus branch's inputs into the Go cache",
		Long: `Fetches the remote's fuzz-corpus branch and copies the inputs of the fuzz
targets in the packages (every one by default, or those the PACKAGE patterns
select) into go test's fuzz cache. Inputs the cache has already stay as they
are.`,
		Example: `# Every target's inputs
devtool fuzz corpus pull

# The parser's, from a fork's upstream
devtool fuzz corpus pull --remote upstream ./internal/parser`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return pull(cmd.Context(), pretty.New(cmd.OutOrStdout()), o, co, args)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&co.filter, "filter", "f", "", "Regular expression selecting the "+names.Things+" by name")
	f.StringVar(&co.remote, "remote", co.remote, "Remote whose fuzz-corpus branch holds the inputs")
	gotool.BindEnv(cmd, envPrefix, o.getenv)
	return cmd
}

func newPushCommand(o options) *cobra.Command {
	co := corpusOptions{remote: defaultRemote}
	cmd := &cobra.Command{
		Use:   "push [PACKAGE...]",
		Short: "Push the inputs the fuzz-corpus branch doesn't have yet",
		Long: `Commits the inputs of the fuzz targets in the packages (every one by default,
or those the PACKAGE patterns select) that the remote's fuzz-corpus branch
doesn't have yet, and pushes them as one commit. The branch is created when
the remote has none. When another push gets there first, the commit is
rebuilt on top of it.

The inputs come from go test's fuzz cache, or from --from, a directory laid
out like it, as CI collects them from its fuzzing jobs.`,
		Example: `# Share what a local campaign found
devtool fuzz corpus push

# CI: push what the fuzzing jobs uploaded
devtool fuzz corpus push --from corpus`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return push(cmd.Context(), pretty.New(cmd.OutOrStdout()), o, co, args)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&co.filter, "filter", "f", "", "Regular expression selecting the "+names.Things+" by name")
	f.StringVar(&co.remote, "remote", co.remote, "Remote whose fuzz-corpus branch holds the inputs")
	f.StringVar(&co.from, "from", "", "Directory laid out like go test's fuzz cache to push from, instead of the cache")
	gotool.BindEnv(cmd, envPrefix, o.getenv)
	return cmd
}

func pull(ctx context.Context, p *pretty.Printer, o options, co corpusOptions, patterns []string) humane.Error {
	root, targets, err := discover(ctx, o, patterns, co.filter)
	if err != nil {
		return err
	}
	repo := corpus.Repo{Dir: root, Remote: co.remote}
	found, err := repo.Fetch(ctx)
	if err != nil {
		return err
	}
	if !found {
		return p.Note("Nothing to restore", co.remote+" has no "+corpus.Branch+" branch yet; devtool fuzz corpus push creates it")
	}
	cache, err := fuzzCache(ctx, o, root)
	if err != nil {
		return err
	}
	n, err := repo.Restore(ctx, cache, corpusTargets(targets))
	if err != nil {
		return err
	}
	return p.Ok(fmt.Sprintf("Restored %d %s", n, ui.Plural(n, "input", "inputs")),
		fmt.Sprintf("from %s for %d %s, into %s", repo.Display(), len(targets), ui.Plural(len(targets), "fuzz target", "fuzz targets"), cache))
}

func push(ctx context.Context, p *pretty.Printer, o options, co corpusOptions, patterns []string) humane.Error {
	root, targets, err := discover(ctx, o, patterns, co.filter)
	if err != nil {
		return err
	}
	src := co.from
	if src == "" {
		if src, err = fuzzCache(ctx, o, root); err != nil {
			return err
		}
	} else if src, err = absDir(src); err != nil {
		return err
	}
	repo := corpus.Repo{Dir: root, Remote: co.remote}
	n, err := repo.Save(ctx, src, corpusTargets(targets), source(ctx, o, root))
	if err != nil {
		return err
	}
	if n == 0 {
		return p.Ok("Nothing to push", repo.Display()+" has every input already")
	}
	return p.Ok(fmt.Sprintf("Pushed %d %s", n, ui.Plural(n, "input", "inputs")), "to "+repo.Display())
}

// restore copies the corpus of targets from the remote's branch into the
// cache before fuzz run fuzzes them, and returns the run header's Corpus
// row. A failure doesn't stop the run, which then starts from the seeds
// and whatever the cache has: it's printed as a warning.
func restore(ctx context.Context, p *pretty.Printer, o options, root, remote string, targets []gotool.Target) string {
	repo := corpus.Repo{Dir: root, Remote: remote}
	if ok, err := repo.HasRemote(ctx); err != nil || !ok {
		return "not restored, no remote " + remote
	}
	n := 0
	found, err := repo.Fetch(ctx)
	if err == nil && found {
		var cache string
		if cache, err = fuzzCache(ctx, o, root); err == nil {
			n, err = repo.Restore(ctx, cache, corpusTargets(targets))
		}
	}
	switch {
	case err != nil:
		_ = p.Warning("Can't restore the fuzzing corpus from "+repo.Display(), err.Error(),
			"Fuzzing starts from the seeds and the inputs in the Go cache.")
		return "not restored, see the warning above"
	case !found:
		return remote + " has no " + corpus.Branch + " branch yet"
	}
	return fmt.Sprintf("%d new %s from %s", n, ui.Plural(n, "input", "inputs"), repo.Display())
}

// corpusTargets returns what the corpus package needs to know about
// targets.
func corpusTargets(targets []gotool.Target) []corpus.Target {
	out := make([]corpus.Target, 0, len(targets))
	for _, t := range targets {
		dir := strings.TrimPrefix(t.Dir, "./")
		if dir == "." {
			dir = ""
		}
		out = append(out, corpus.Target{Package: t.Package, Dir: dir, Name: t.Name})
	}
	return out
}

// fuzzCache returns go test's fuzz cache directory, where it keeps each
// target's corpus.
func fuzzCache(ctx context.Context, o options, root string) (string, humane.Error) {
	if o.fuzzCache != "" {
		return o.fuzzCache, nil
	}
	cache, err := gotool.Output(ctx, root, nil, "go", "env", "GOCACHE")
	if err != nil {
		return "", err
	}
	if cache = strings.TrimSpace(cache); cache == "" || cache == "off" {
		return "", humane.New("the Go build cache is off, so go test keeps no corpus", "unset GOCACHE or point it at a directory")
	}
	return filepath.Join(cache, "fuzz"), nil
}

// source says in the corpus commit where its inputs come from: the GitHub
// Actions run in CI, or the machine and the commit that was fuzzed.
func source(ctx context.Context, o options, root string) string {
	if server, repo, run := o.getenv("GITHUB_SERVER_URL"), o.getenv("GITHUB_REPOSITORY"), o.getenv("GITHUB_RUN_ID"); server != "" && repo != "" && run != "" {
		return strings.TrimSuffix(server, "/") + "/" + repo + "/actions/runs/" + run
	}
	from := "a local run"
	if host, err := os.Hostname(); err == nil {
		from = "a run on " + host
	}
	if head, err := gotool.Output(ctx, root, nil, "git", "rev-parse", "--short", "HEAD"); err == nil {
		from += " at " + strings.TrimSpace(head)
	}
	return from
}

// absDir returns dir as an absolute path, and an error when it isn't a
// directory.
func absDir(dir string) (string, humane.Error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", humane.Wrap(err, "can't resolve "+dir, "pass --from as an absolute path")
	}
	if info, serr := os.Stat(abs); serr != nil || !info.IsDir() {
		return "", humane.New(dir+" isn't a directory", "pass --from the directory CI downloaded the corpus into")
	}
	return abs, nil
}
