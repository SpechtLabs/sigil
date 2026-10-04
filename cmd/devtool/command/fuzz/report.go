package fuzz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
)

// marker identifies the campaign's issue. Looking it up in issue bodies
// avoids search-index delays and survives a changed title.
const marker = "<!-- sigil:extended-fuzzing -->"

// campaign is the GitHub Actions run being reported, read from the
// environment Actions sets.
type campaign struct {
	Ref        string
	Event      string
	Repository string
	RepoURL    string
	APIURL     string
	RunID      string
	RunAttempt string
	SHA        string
	Token      string

	Targets  string
	Fuzz     string
	Fuzztime string
}

// issue is the part of GitHub's issue object the lookup reads.
type issue struct {
	Number      int     `json:"number"`
	Body        *string `json:"body"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

// posted is the part of a created issue or comment the report prints.
type posted struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
}

func newReportCommand(o options) *cobra.Command {
	var ro reportOptions

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Open or update the issue for a failed extended fuzz campaign",
		Long: `Opens an issue for a failed extended fuzz campaign on main, or comments on the
open one. It only reports scheduled and manually dispatched runs of main in
which target discovery or a fuzz job failed, and does nothing otherwise.

The run's details come from the GitHub Actions environment; the token from
GH_TOKEN or GITHUB_TOKEN needs permission to write issues.`,
		Example: `devtool fuzz report --targets-result success --fuzz-result failure --time 60m`,
		Args:    usage.None(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return report(cmd.Context(), pretty.New(cmd.OutOrStdout()), o, ro)
		},
	}

	f := cmd.Flags()
	f.StringVar(&ro.targets, "targets-result", "", "Result of the target discovery job")
	f.StringVar(&ro.fuzz, "fuzz-result", "", "Result of the fuzz jobs")
	f.StringVar(&ro.time, "time", "", "Time per fuzz target the campaign requested")
	f.StringVar(&ro.results, "results", "", "Directory with the fuzz jobs' downloaded artifacts, whose results the issue details")
	for _, name := range []string{"targets-result", "fuzz-result", "time"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func report(ctx context.Context, p *pretty.Printer, o options, ro reportOptions) humane.Error {
	c := readCampaign(o.getenv, ro)
	if reason := c.skip(); reason != "" {
		return p.Note("Nothing to report", reason)
	}
	if err := c.validate(); err != nil {
		return err
	}

	// Discovery failing, or a setup failure in every job, leaves no
	// results; the jobs' annotations still say what happened.
	var runs []runResults
	if ro.results != "" {
		runs, _ = readResults([]string{ro.results})
	}
	gh := github{client: o.client, api: c.APIURL, token: c.Token}
	title, body := issueReport(c, gh.lookUp(ctx, c, runs))

	number, err := gh.findIssue(ctx, c.Repository)
	if err != nil {
		return err
	}
	path := "/repos/" + c.Repository + "/issues"
	payload := map[string]string{"title": title, "body": body}
	if number != 0 {
		path = fmt.Sprintf("%s/%d/comments", path, number)
		payload = map[string]string{"body": body}
	}
	res, err := gh.post(ctx, path, payload)
	if err != nil {
		return err
	}
	if number != 0 {
		return p.Ok(fmt.Sprintf("Commented on issue #%d", number), res.URL)
	}
	return p.Ok(fmt.Sprintf("Opened issue #%d", res.Number), res.URL)
}

func readCampaign(getenv func(string) string, ro reportOptions) campaign {
	c := campaign{
		Ref:        getenv("GITHUB_REF"),
		Event:      getenv("GITHUB_EVENT_NAME"),
		Repository: getenv("GITHUB_REPOSITORY"),
		APIURL:     getenv("GITHUB_API_URL"),
		RunID:      getenv("GITHUB_RUN_ID"),
		RunAttempt: getenv("GITHUB_RUN_ATTEMPT"),
		SHA:        getenv("GITHUB_SHA"),
		Token:      getenv("GH_TOKEN"),
		Targets:    ro.targets,
		Fuzz:       ro.fuzz,
		Fuzztime:   ro.time,
	}
	if server := getenv("GITHUB_SERVER_URL"); server != "" && c.Repository != "" {
		c.RepoURL = strings.TrimSuffix(server, "/") + "/" + c.Repository
	}
	if c.APIURL == "" {
		c.APIURL = "https://api.github.com"
	}
	if c.Token == "" {
		c.Token = getenv("GITHUB_TOKEN")
	}
	return c
}

// skip returns why the run isn't reported, or "" when it is: only
// scheduled and dispatched runs of main in which a job failed are.
func (c campaign) skip() string {
	switch {
	case c.Ref != "refs/heads/main":
		return "the campaign didn't run on main"
	case c.Event != "schedule" && c.Event != "workflow_dispatch":
		return "only scheduled and manually dispatched campaigns are reported"
	case c.Targets != failure && c.Fuzz != failure:
		return "no job failed"
	}
	return ""
}

func (c campaign) validate() humane.Error {
	var missing []string
	for name, value := range map[string]string{
		"GITHUB_REPOSITORY":        c.Repository,
		"GITHUB_SERVER_URL":        c.RepoURL,
		"GITHUB_RUN_ID":            c.RunID,
		"GITHUB_RUN_ATTEMPT":       c.RunAttempt,
		"GITHUB_SHA":               c.SHA,
		"GH_TOKEN or GITHUB_TOKEN": c.Token,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return humane.New("missing GitHub Actions environment: "+strings.Join(missing, ", "),
			"run fuzz report from the extended fuzzing workflow, which sets them")
	}
	return nil
}

// github is a minimal GitHub REST API client.
type github struct {
	client *http.Client
	api    string
	token  string
}

// findIssue returns the number of the first open issue carrying the marker,
// or 0 when there's none. It pages through every open issue; a failed
// lookup is an error so a transient failure never opens a duplicate.
func (g github) findIssue(ctx context.Context, repo string) (int, humane.Error) {
	url := strings.TrimSuffix(g.api, "/") + "/repos/" + repo + "/issues?state=open&per_page=100"
	for url != "" {
		resp, err := g.do(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		var page []issue
		derr := json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if derr != nil {
			return 0, humane.Wrap(derr, "can't read GitHub's issue list", "retry the reporting job")
		}
		for _, i := range page {
			if i.PullRequest == nil && i.Body != nil && strings.Contains(*i.Body, marker) {
				return i.Number, nil
			}
		}
		url = nextPage(resp.Header.Get("Link"))
	}
	return 0, nil
}

func (g github) post(ctx context.Context, path string, payload map[string]string) (posted, humane.Error) {
	b, merr := json.Marshal(payload)
	if merr != nil {
		return posted{}, humane.Wrap(merr, "can't encode the request to GitHub", "this is a bug in devtool")
	}
	resp, err := g.do(ctx, http.MethodPost, strings.TrimSuffix(g.api, "/")+path, b)
	if err != nil {
		return posted{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	// GitHub accepted the request, so an unreadable answer only costs the
	// link in the output.
	var p posted
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return p, nil
}

// do sends a request and returns the response when GitHub accepted it.
func (g github) do(ctx context.Context, method, url string, body []byte) (*http.Response, humane.Error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, humane.Wrap(err, "can't build the request to "+url, "check GITHUB_API_URL")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, humane.Wrap(err, method+" "+url+" failed", "retry the reporting job")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, humane.New(fmt.Sprintf("%s %s: %s: %s", method, url, resp.Status, bytes.TrimSpace(msg)),
			"check that the token may read and write issues")
	}
	return resp, nil
}

// nextPage returns the rel="next" URL of a Link header, or "".
func nextPage(link string) string {
	for part := range strings.SplitSeq(link, ",") {
		target, params, ok := strings.Cut(part, ";")
		if ok && strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}
	return ""
}

// readAll reads a response's body and closes it.
func readAll(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}
