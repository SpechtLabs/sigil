package fuzz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
)

// maxBody is how long an issue or comment body may be: GitHub refuses
// longer ones.
const maxBody = 65536

// titleTargets is how many failed targets an issue's title names.
const titleTargets = 3

// failure is how GitHub names a job's failed conclusion, and a failed
// step's annotation level.
const failure = "failure"

// genericAnnotation starts the annotation every failed step gets; it says
// nothing the job's result doesn't.
const genericAnnotation = "Process completed with exit code"

// runJob is the part of a workflow job the report reads.
type runJob struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
}

// runArtifact is the part of a workflow artifact the report reads.
type runArtifact struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// target returns the fuzz target whose job uploaded the artifact, named
// fuzz-<target>-<job index>, or "" for another artifact.
func (a runArtifact) target() string {
	rest, ok := strings.CutPrefix(a.Name, "fuzz-")
	i := strings.LastIndex(rest, "-")
	if !ok || i <= 0 {
		return ""
	}
	return rest[:i]
}

// failedJob is a job of the campaign that didn't succeed, with what its
// annotations say about why.
type failedJob struct {
	runJob
	notes []string
}

// target returns the fuzz target a job fuzzed, or "" for a job that
// fuzzes none, such as target discovery.
func (j runJob) target() string {
	name, ok := strings.CutPrefix(j.Name, "fuzz ")
	if !ok || strings.ContainsAny(name, " ()") {
		return ""
	}
	return name
}

// findings is what the issue reports: the targets' results, the jobs that
// failed, and where each target's artifact is.
type findings struct {
	runs      []runResults
	jobs      []failedJob
	artifacts map[string]string // a target's name to its artifact's page
	// lookup says which details GitHub wouldn't give, or is empty.
	lookup string
}

// lookUp asks GitHub for the run's failed jobs, what their annotations
// say, and its artifacts. Each is best effort: an issue without the
// details beats none, so a failure is only noted in it.
func (g github) lookUp(ctx context.Context, c campaign, runs []runResults) findings {
	f := findings{runs: runs, artifacts: map[string]string{}}
	base := strings.TrimSuffix(g.api, "/") + "/repos/" + c.Repository
	var missing []string

	var jobs []runJob
	err := g.pages(ctx, base+"/actions/runs/"+c.RunID+"/attempts/"+c.RunAttempt+"/jobs?per_page=100", func(b []byte) error {
		var page struct{ Jobs []runJob }
		if err := json.Unmarshal(b, &page); err != nil {
			return humane.Wrap(err, "GitHub listed the run's jobs as JSON devtool can't read", "retry the reporting job")
		}
		jobs = append(jobs, page.Jobs...)
		return nil
	})
	if err != nil {
		missing = append(missing, "the run's jobs")
	}
	for _, j := range jobs {
		if j.Conclusion == "success" || j.Conclusion == "skipped" || j.Name == "Report failed campaign" {
			continue
		}
		fj := failedJob{runJob: j}
		var notes []struct {
			Level   string `json:"annotation_level"`
			Message string `json:"message"`
		}
		if aerr := g.getJSON(ctx, fmt.Sprintf("%s/check-runs/%d/annotations", base, j.ID), &notes); aerr != nil {
			missing = append(missing, "the annotations of "+j.Name)
		}
		for _, n := range notes {
			if n.Level == failure && !strings.HasPrefix(n.Message, genericAnnotation) {
				fj.notes = append(fj.notes, n.Message)
			}
		}
		f.jobs = append(f.jobs, fj)
	}

	err = g.pages(ctx, base+"/actions/runs/"+c.RunID+"/artifacts?per_page=100", func(b []byte) error {
		var page struct{ Artifacts []runArtifact }
		if err := json.Unmarshal(b, &page); err != nil {
			return humane.Wrap(err, "GitHub listed the run's artifacts as JSON devtool can't read", "retry the reporting job")
		}
		for _, a := range page.Artifacts {
			if t := a.target(); t != "" {
				f.artifacts[t] = fmt.Sprintf("%s/actions/runs/%s/artifacts/%d", c.RepoURL, c.RunID, a.ID)
			}
		}
		return nil
	})
	if err != nil {
		missing = append(missing, "the run's artifacts")
	}
	if len(missing) > 0 {
		f.lookup = "GitHub didn't list " + strings.Join(missing, ", ") + ", so links to them are missing."
	}
	return f
}

// issueReport returns the issue's title and body: which targets failed and what
// go test said, the jobs that failed without results and why, every
// target's result, and how to keep a failing input.
func issueReport(c campaign, f findings) (string, string) {
	var failed []targetResult
	have := map[string]bool{}
	total := 0
	for _, r := range f.runs {
		for _, t := range r.Targets {
			total++
			have[t.Target] = true
			if t.failed() {
				failed = append(failed, t)
			}
		}
	}
	var lost []failedJob
	jobOf := map[string]failedJob{}
	for _, j := range f.jobs {
		if t := j.target(); t != "" {
			jobOf[t] = j
		}
		if t := j.target(); !have[t] {
			lost = append(lost, j)
		}
	}

	var b strings.Builder
	b.WriteString(marker + "\n")
	b.WriteString(headline(c, len(failed), total, len(lost)) + "\n\n")
	fmt.Fprintf(&b, "[Run %s](%s/actions/runs/%s/attempts/%s) tested [%s](%s/commit/%s) for %s per target, started by %s.\n\n",
		c.RunID, c.RepoURL, c.RunID, c.RunAttempt, shortSHA(c.SHA), c.RepoURL, c.SHA, c.Fuzztime, c.Event)

	if len(failed) > 0 {
		b.WriteString("## Failing targets\n\n")
		for _, t := range failed {
			b.WriteString(failureMarkdown(t, links(t, jobOf, f.artifacts)...))
		}
	}
	if len(lost) > 0 {
		b.WriteString("## Jobs that failed without results\n\n")
		for _, j := range lost {
			why := "It left no results; its log says why."
			if len(j.notes) > 0 {
				why = strings.Join(j.notes, " ")
			}
			fmt.Fprintf(&b, "**%s** (%s): %s [Job log](%s)\n\n", j.Name, j.Conclusion, why, j.URL)
		}
	}
	if f.lookup != "" {
		b.WriteString("_" + f.lookup + "_\n\n")
	}

	table := ""
	if total > 0 {
		table = fmt.Sprintf("<details>\n<summary>Every target's result</summary>\n\n%s\n</details>\n\n", targetTable(f.runs))
	}
	help := "<details>\n<summary>Keeping a failing input</summary>\n\n" +
		"Download the target's artifact, copy the input into the package's `testdata/fuzz/<target>/` directory, " +
		"and replay it with the command above. Keep the input with its fix, so every `go test` run replays it.\n\n" +
		"A job that failed without results was likely killed: by its timeout, or by a runner that ran out of memory. " +
		"Fuzz the target locally with `mise run fuzz -- --filter '^<target>$' <package>` to tell.\n</details>\n"
	body := b.String()
	if len(body)+len(table)+len(help) <= maxBody {
		body += table + help
	}
	if len(body) > maxBody {
		cut := fmt.Sprintf("\n\n_The report is cut short here; the [run's summary page](%s/actions/runs/%s) has all of it._\n", c.RepoURL, c.RunID)
		body = body[:max(0, strings.LastIndex(body[:maxBody-len(cut)], "\n"))] + cut
	}
	return title(failed, lost), body
}

// links point a failed target to its job's log and to its artifact,
// where GitHub listed them.
func links(t targetResult, jobOf map[string]failedJob, artifacts map[string]string) []string {
	var out []string
	if j, ok := jobOf[t.Target]; ok {
		out = append(out, "[Job log]("+j.URL+")")
	}
	if a, ok := artifacts[t.Target]; ok {
		what := "Logs"
		if t.Input != "" {
			what = "The failing input and logs"
		}
		out = append(out, "["+what+"]("+a+")")
	}
	return out
}

// headline says in a sentence what went wrong.
func headline(c campaign, failed, total, lost int) string {
	var parts []string
	if c.Targets == failure {
		parts = append(parts, "target discovery failed, so no target was fuzzed")
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d %s failed", failed, total, ui.Plural(total, "target", "targets")))
	}
	if lost > 0 {
		parts = append(parts, fmt.Sprintf("%d %s failed without results", lost, ui.Plural(lost, "job", "jobs")))
	}
	if len(parts) == 0 {
		parts = append(parts, "a fuzz job failed")
	}
	return "**Extended fuzzing failed on main**: " + strings.Join(parts, ", and ") + "."
}

// title names the failed targets, the first few of them.
func title(failed []targetResult, lost []failedJob) string {
	var names []string
	for _, t := range failed {
		names = append(names, t.Target)
	}
	for _, j := range lost {
		if t := j.target(); t != "" {
			names = append(names, t)
		}
	}
	names = slices.Compact(names)
	if len(names) == 0 {
		return "Extended fuzzing failed on main"
	}
	shown := strings.Join(names[:min(len(names), titleTargets)], ", ")
	if len(names) > titleTargets {
		shown += fmt.Sprintf(" and %d more", len(names)-titleTargets)
	}
	return "Extended fuzzing failed on main: " + shown
}

// shortSHA abbreviates a commit ID the way git log --oneline does.
func shortSHA(sha string) string { return sha[:min(len(sha), 7)] }

// pages GETs url and each rel="next" page after it, handing every body to
// read.
func (g github) pages(ctx context.Context, url string, read func([]byte) error) humane.Error {
	for url != "" {
		resp, err := g.do(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		b, rerr := readAll(resp)
		if rerr == nil {
			rerr = read(b)
		}
		if rerr != nil {
			return humane.Wrap(rerr, "can't read GitHub's answer to GET "+url, "retry the reporting job")
		}
		url = nextPage(resp.Header.Get("Link"))
	}
	return nil
}

// getJSON GETs url and decodes the answer into v.
func (g github) getJSON(ctx context.Context, url string, v any) humane.Error { //nolint:emptyinterface // decodes into whatever it's given
	return g.pages(ctx, url, func(b []byte) error { return json.Unmarshal(b, v) })
}
