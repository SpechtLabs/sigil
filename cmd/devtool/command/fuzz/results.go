package fuzz

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
)

// resultsFile is where fuzz run records how each target went, for fuzz
// summary to merge the results of several runs.
const resultsFile = "results.json"

// How a target's run ended.
const (
	resultPassed = "passed"
	resultFailed = "failed"
	resultFound  = "found a failing input"
	resultNotRun = "not run" // an earlier target failed, which stops the run
)

// targetResult is how one target's run went.
type targetResult struct {
	Target    string `json:"target"`
	Dir       string `json:"dir"` // the package, in the ./dir form
	Result    string `json:"result"`
	Execs     int64  `json:"execs"`
	NewInputs int64  `json:"new_inputs"`
	// Input is the failing input go test saved, relative to the module
	// root; Replay is the command that replays it, or that reruns a target
	// that failed without one.
	Input  string `json:"input,omitempty"`
	Replay string `json:"replay,omitempty"`
}

// failed reports whether the target failed, with or without an input.
func (r targetResult) failed() bool { return r.Result == resultFailed || r.Result == resultFound }

// runResults is results.json: what one fuzz run did.
type runResults struct {
	Head     string         `json:"head"`
	Time     string         `json:"time"`     // --time
	Restored int            `json:"restored"` // corpus inputs restored before fuzzing
	Targets  []targetResult `json:"targets"`
}

// readResults reads every results.json under the directories, as CI
// downloads them from the jobs of a campaign.
func readResults(dirs []string) ([]runResults, humane.Error) {
	var runs []runResults
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != resultsFile {
				return err
			}
			b, err := os.ReadFile(path) //nolint:gosec // a results file under a directory the caller named
			if err != nil {
				return err
			}
			var r runResults
			if err := json.Unmarshal(b, &r); err != nil {
				return humane.Wrap(err, path+" isn't a fuzz run's results", "download the artifacts again")
			}
			runs = append(runs, r)
			return nil
		})
		if err != nil {
			return nil, humane.Wrap(err, "can't read the fuzz results under "+dir, "pass the directories the fuzz jobs' artifacts were downloaded into")
		}
	}
	if len(runs) == 0 {
		return nil, humane.New("no "+resultsFile+" under "+strings.Join(dirs, ", "),
			"pass the directories the fuzz jobs' artifacts were downloaded into; a job that failed before fuzzing leaves none")
	}
	return runs, nil
}

// summaryMarkdown renders runs as one Markdown summary: a headline with
// the totals, the failures with how to replay each, and a table of every
// target, failures first.
func summaryMarkdown(runs []runResults) string {
	var all []targetResult
	var execs, found int64
	restored := 0
	times := map[string]bool{}
	for _, r := range runs {
		all = append(all, r.Targets...)
		restored += r.Restored
		times[r.Time] = true
		for _, t := range r.Targets {
			execs += t.Execs
			found += t.NewInputs
		}
	}
	passed := 0
	for _, t := range all {
		if t.Result == resultPassed {
			passed++
		}
	}

	var b strings.Builder
	b.WriteString("# Go fuzzing\n\n")
	head := []string{fmt.Sprintf("**%d of %d %s passed**", passed, len(all), ui.Plural(len(all), "target", "targets"))}
	if len(times) == 1 {
		for t := range times {
			head = append(head, t+" per target")
		}
	}
	head = append(head, ui.Count(float64(execs))+" execs", fmt.Sprintf("%d new %s", found, ui.Plural(int(found), "input", "inputs")))
	if restored > 0 {
		head = append(head, fmt.Sprintf("started from %d corpus %s", restored, ui.Plural(restored, "input", "inputs")))
	}
	b.WriteString(strings.Join(head, " · ") + "\n\n")

	failed := slices.DeleteFunc(slices.Clone(all), func(t targetResult) bool { return !t.failed() })
	if len(failed) > 0 {
		b.WriteString("## Failures\n\n")
		for _, t := range failed {
			b.WriteString(failureMarkdown(t))
		}
		b.WriteString("## Targets\n\n")
	}

	// Failures first, then the rest in the order they ran.
	slices.SortStableFunc(all, func(a, b targetResult) int {
		switch {
		case a.failed() == b.failed():
			return 0
		case a.failed():
			return -1
		}
		return 1
	})
	rows := make([][]ui.Cell, 0, len(all))
	for _, t := range all {
		rows = append(rows, []ui.Cell{
			{Text: t.Target}, {Text: t.Dir},
			{Text: ui.Count(float64(t.Execs))}, {Text: fmt.Sprint(t.NewInputs)}, {Text: resultMark(t.Result) + " " + t.Result},
		})
	}
	b.WriteString(ui.Markdown(summaryColumns, rows))
	return b.String()
}

// failureMarkdown describes a failed target and how to replay it.
func failureMarkdown(t targetResult) string {
	what := fmt.Sprintf("**%s** in `%s` failed. Rerun it with:", t.Target, t.Dir)
	if t.Input != "" {
		what = fmt.Sprintf("**%s** in `%s` found a failing input, saved as `%s`. Replay it with:", t.Target, t.Dir, t.Input)
	}
	return what + "\n\n```sh\n" + t.Replay + "\n```\n\n"
}

// resultMark is the symbol before a result in the table.
func resultMark(result string) string {
	switch result {
	case resultPassed:
		return "✓"
	case resultNotRun:
		return "–"
	}
	return "✗"
}
