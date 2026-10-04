package fuzz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestReport(t *testing.T) {
	// The results two fuzz jobs left: FuzzPatch's failure, and FuzzA
	// passing; FuzzLost's job died before it wrote any.
	results := t.TempDir()
	writeResults(t, filepath.Join(results, "fuzz-FuzzPatch-3"), runResults{Time: "60m", Targets: []targetResult{{
		Target: "FuzzPatch", Dir: "./internal/stamp", Result: resultFound, Execs: 1000, NewInputs: 3,
		Input:   "internal/stamp/testdata/fuzz/FuzzPatch/5a2f",
		Replay:  "go test -run=FuzzPatch/5a2f ./internal/stamp",
		Message: "fuzz_test.go:40: Verify() after Patch() = the binary is damaged",
	}}})
	writeResults(t, filepath.Join(results, "fuzz-FuzzA-0"), runResults{Time: "60m", Targets: []targetResult{{Target: "FuzzA", Dir: "./a", Result: resultPassed}}})
	jobs := []runJob{
		{ID: 1, Name: "targets", Conclusion: "success", URL: "https://github.com/example/sigil/jobs/1"},
		{ID: 2, Name: "fuzz FuzzA", Conclusion: "success", URL: "https://github.com/example/sigil/jobs/2"},
		{ID: 3, Name: "fuzz FuzzPatch", Conclusion: "failure", URL: "https://github.com/example/sigil/jobs/3"},
		{ID: 4, Name: "fuzz FuzzLost", Conclusion: "failure", URL: "https://github.com/example/sigil/jobs/4"},
		{ID: 5, Name: "Report failed campaign", Conclusion: "", URL: "https://github.com/example/sigil/jobs/5"},
	}
	annotations := map[int64][]string{
		3: {"Process completed with exit code 1."},
		4: {"The hosted runner lost communication with the server."},
	}
	artifacts := []runArtifact{{ID: 70, Name: "fuzz-FuzzPatch-3"}, {ID: 71, Name: "fuzz-FuzzA-0"}, {ID: 72, Name: "corpus-3"}}
	// Enough failing targets, with long messages, that the report can't
	// hold them all.
	many := t.TempDir()
	lots := make([]targetResult, 0, 400)
	for i := range 400 {
		lots = append(lots, targetResult{Target: fmt.Sprintf("FuzzT%d", i), Dir: "./t", Result: resultFailed, Replay: "go test ./t", Message: strings.Repeat("x", 200)})
	}
	writeResults(t, many, runResults{Time: "60m", Targets: lots})

	tests := []struct {
		name string
		env  map[string]string
		args []string
		// pages are the open issues GitHub lists, one slice per page.
		pages      [][]issue
		listStatus int
		postStatus int
		// jobs, annotations and artifacts are the run's, as GitHub lists
		// them; lookupStatus fails every lookup of them.
		jobs         []runJob
		annotations  map[int64][]string
		artifacts    []runArtifact
		lookupStatus int

		wantErr   string
		wantOut   string
		wantCalls []string
		wantTitle string
		wantBody  []string
		wantNot   []string
	}{
		{
			name:      "first failure opens an issue",
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantOut:   "Opened issue #99",
			wantTitle: "Extended fuzzing failed on main",
			wantBody: []string{
				marker,
				"**Extended fuzzing failed on main**: a fuzz job failed.",
				"[Run 123](https://github.com/example/sigil/actions/runs/123/attempts/2) tested [abc123](https://github.com/example/sigil/commit/abc123) for 60m per target, started by schedule.",
				"Keeping a failing input",
			},
		},
		{
			name:        "the issue says what failed, why, and links each failure's job and input",
			args:        []string{"--results", results},
			jobs:        jobs,
			annotations: annotations,
			artifacts:   artifacts,
			wantCalls:   []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantTitle:   "Extended fuzzing failed on main: FuzzPatch, FuzzLost",
			wantBody: []string{
				"**Extended fuzzing failed on main**: 1 of 2 targets failed, and 1 job failed without results.",
				"## Failing targets\n\n**FuzzPatch** in `./internal/stamp` found a failing input, saved as `internal/stamp/testdata/fuzz/FuzzPatch/5a2f`.",
				"```text\nfuzz_test.go:40: Verify() after Patch() = the binary is damaged\n```",
				"Replay it with:\n\n```sh\ngo test -run=FuzzPatch/5a2f ./internal/stamp\n```",
				"[Job log](https://github.com/example/sigil/jobs/3) · [The failing input and logs](https://github.com/example/sigil/actions/runs/123/artifacts/70)",
				"## Jobs that failed without results\n\n**fuzz FuzzLost** (failure): The hosted runner lost communication with the server. [Job log](https://github.com/example/sigil/jobs/4)",
				"<summary>Every target's result</summary>",
				"| FuzzPatch | ./internal/stamp | 1k | 3 | ✗ found a failing input |",
			},
			wantNot: []string{"Process completed with exit code", "Report failed campaign", "GitHub didn't list"},
		},
		{
			name:         "details GitHub won't give are left out, not fatal",
			args:         []string{"--results", results},
			lookupStatus: http.StatusForbidden,
			wantCalls:    []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantTitle:    "Extended fuzzing failed on main: FuzzPatch",
			wantBody:     []string{"1 of 2 targets failed.", "_GitHub didn't list the run's jobs, the run's artifacts, so links to them are missing._"},
			wantNot:      []string{"[Job log]"},
		},
		{
			name:      "results that don't fit are cut short",
			args:      []string{"--results", many},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantTitle: "Extended fuzzing failed on main: FuzzT0, FuzzT1, FuzzT2 and 397 more",
			wantBody:  []string{"_The report is cut short here; the [run's summary page](https://github.com/example/sigil/actions/runs/123) has all of it._"},
			wantNot:   []string{"Every target's result"},
		},
		{
			name:      "results that aren't there leave the report without them",
			args:      []string{"--results", filepath.Join(results, "missing")},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantBody:  []string{"a fuzz job failed."},
		},
		{
			name:      "repeated failure comments on the open issue",
			pages:     [][]issue{{campaignIssue(42)}},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues/42/comments"},
			wantOut:   "Commented on issue #42",
		},
		{
			name:      "several matches comment only on the first",
			pages:     [][]issue{{campaignIssue(42), campaignIssue(43)}},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues/42/comments"},
		},
		{
			name:  "the issue is found on a later page",
			pages: [][]issue{{otherIssue(1, "unrelated"), pullRequest(2)}, {campaignIssue(7)}},
			wantCalls: []string{
				"GET /repos/example/sigil/issues?state=open&per_page=100",
				"GET /repos/example/sigil/issues?page=2",
				"POST /repos/example/sigil/issues/7/comments",
			},
		},
		{
			name:      "pull requests and issues without a body don't match",
			pages:     [][]issue{{pullRequest(3), {Number: 4}}},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
		},
		{
			name:      "setup failure is reported when fuzzing was skipped",
			args:      []string{"--targets-result", "failure", "--fuzz-result", "skipped"},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantBody:  []string{"**Extended fuzzing failed on main**: target discovery failed, so no target was fuzzed."},
		},
		{
			name:      "manual dispatch reports the selected duration",
			env:       map[string]string{"GITHUB_EVENT_NAME": "workflow_dispatch"},
			args:      []string{"--time", "10m"},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantBody:  []string{"for 10m per target, started by workflow_dispatch."},
		},
		{
			name:      "GITHUB_TOKEN is used without GH_TOKEN",
			env:       map[string]string{"GH_TOKEN": "", "GITHUB_TOKEN": "fallback"},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
		},
		{name: "success isn't reported", args: []string{"--fuzz-result", "success"}, wantOut: "no job failed"},
		{name: "cancellation isn't reported", args: []string{"--fuzz-result", "cancelled"}, wantOut: "no job failed"}, //nolint:misspell // GitHub's spelling
		{name: "pull requests aren't reported", env: map[string]string{"GITHUB_EVENT_NAME": "pull_request"}, wantOut: "only scheduled"},
		{name: "branches aren't reported", env: map[string]string{"GITHUB_REF": "refs/heads/feature"}, wantOut: "didn't run on main"},
		{name: "tags aren't reported", env: map[string]string{"GITHUB_REF": "refs/tags/main"}, wantOut: "didn't run on main"},
		{
			name:    "missing environment",
			env:     map[string]string{"GITHUB_SHA": "", "GH_TOKEN": ""},
			wantErr: "missing GitHub Actions environment",
		},
		{
			name:    "missing server URL",
			env:     map[string]string{"GITHUB_SERVER_URL": "", "GITHUB_API_URL": ""},
			wantErr: "missing GitHub Actions environment: GITHUB_SERVER_URL",
		},
		{
			name:       "failed lookup never opens a duplicate",
			pages:      [][]issue{{campaignIssue(42)}},
			listStatus: http.StatusInternalServerError,
			wantCalls:  []string{"GET /repos/example/sigil/issues?state=open&per_page=100"},
			wantErr:    "500 Internal Server Error",
		},
		{
			name:       "failed issue creation fails the job",
			postStatus: http.StatusForbidden,
			wantCalls:  []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantErr:    "403 Forbidden",
		},
		{
			name:       "failed comment fails the job",
			pages:      [][]issue{{campaignIssue(42)}},
			postStatus: http.StatusForbidden,
			wantCalls:  []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues/42/comments"},
			wantErr:    "403 Forbidden",
		},
		{
			name:    "skipped runs need no details",
			args:    []string{"--time="},
			env:     map[string]string{"GITHUB_REF": "refs/heads/feature"},
			wantOut: "didn't run on main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &fakeGitHub{
				pages: tt.pages, listStatus: tt.listStatus, postStatus: tt.postStatus,
				jobs: tt.jobs, annotations: tt.annotations, artifacts: tt.artifacts, lookupStatus: tt.lookupStatus,
			}
			server := httptest.NewServer(gh)
			t.Cleanup(server.Close)
			gh.url = server.URL

			env := map[string]string{
				"GITHUB_REF":         "refs/heads/main",
				"GITHUB_EVENT_NAME":  "schedule",
				"GITHUB_REPOSITORY":  "example/sigil",
				"GITHUB_SERVER_URL":  "https://github.com",
				"GITHUB_API_URL":     server.URL,
				"GITHUB_RUN_ID":      "123",
				"GITHUB_RUN_ATTEMPT": "2",
				"GITHUB_SHA":         "abc123",
				"GH_TOKEN":           "secret",
			}
			maps.Copy(env, tt.env)

			var out bytes.Buffer
			cmd := NewCommand(WithGetenv(func(k string) string { return env[k] }), WithHTTPClient(server.Client()))
			args := []string{"report", "--targets-result", "success", "--fuzz-result", "failure", "--time", "60m"}
			cmd.SetArgs(append(args, tt.args...))
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			checkErr(t, cmd.Execute(), tt.wantErr)

			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output %q doesn't contain %q", out.String(), tt.wantOut)
			}
			if got, want := strings.Join(gh.calls, "\n"), strings.Join(tt.wantCalls, "\n"); got != want {
				t.Errorf("calls:\n%s\nwant:\n%s", got, want)
			}
			if len(gh.body) > maxBody {
				t.Errorf("issue body is %d bytes, GitHub takes at most %d", len(gh.body), maxBody)
			}
			if tt.wantTitle != "" && gh.title != tt.wantTitle {
				t.Errorf("title = %q, want %q", gh.title, tt.wantTitle)
			}
			for _, text := range tt.wantNot {
				if strings.Contains(gh.body, text) {
					t.Errorf("issue body contains %q:\n%s", text, gh.body)
				}
			}
			for _, text := range tt.wantBody {
				if !strings.Contains(gh.body, text) {
					t.Errorf("issue body doesn't contain %q:\n%s", text, gh.body)
				}
			}
			if len(gh.calls) > 0 && gh.auth != "Bearer "+env["GH_TOKEN"] && gh.auth != "Bearer "+env["GITHUB_TOKEN"] {
				t.Errorf("Authorization = %q", gh.auth)
			}
		})
	}
}

func TestRequiredReportFlags(t *testing.T) {
	cmd := NewCommand(WithGetenv(func(string) string { return "" }))
	cmd.SetArgs([]string{"report"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	checkErr(t, cmd.Execute(), `required flag(s) "fuzz-result", "targets-result", "time" not set`)
}

func TestNextPage(t *testing.T) {
	tests := []struct {
		link string
		want string
	}{
		{link: "", want: ""},
		{link: `<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=5>; rel="last"`, want: "https://api.github.com/x?page=2"},
		{link: `<https://api.github.com/x?page=1>; rel="prev", <https://api.github.com/x?page=3>; rel="next"`, want: "https://api.github.com/x?page=3"},
		{link: `<https://api.github.com/x?page=1>; rel="first"`, want: ""},
		{link: `garbage`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			if got := nextPage(tt.link); got != tt.want {
				t.Errorf("nextPage(%q) = %q, want %q", tt.link, got, tt.want)
			}
		})
	}
}

func TestGitHubErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		api     string
		wantErr string
	}{
		{
			name:    "invalid JSON",
			handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{") },
			wantErr: "can't read GitHub's issue list",
		},
		{name: "unreachable API", api: "http://127.0.0.1:0", wantErr: "failed"},
		{name: "invalid API URL", api: "http://[::1", wantErr: "can't build the request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := tt.api
			if tt.handler != nil {
				server := httptest.NewServer(tt.handler)
				t.Cleanup(server.Close)
				api = server.URL
			}
			g := github{client: http.DefaultClient, api: api, token: "t"}
			_, err := g.findIssue(t.Context(), "example/sigil")
			checkErr(t, err, tt.wantErr)
		})
	}
}

// fakeGitHub serves the issue list in pages and records every request.
type fakeGitHub struct {
	mu           sync.Mutex
	url          string
	pages        [][]issue
	listStatus   int
	postStatus   int
	jobs         []runJob
	annotations  map[int64][]string
	artifacts    []runArtifact
	lookupStatus int

	// calls are the requests about issues; lookups of the run's jobs,
	// annotations and artifacts aren't among them.
	calls []string
	title string
	body  string
	auth  string
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.auth = r.Header.Get("Authorization")
	if g.lookUp(w, r) {
		return
	}
	g.calls = append(g.calls, r.Method+" "+r.URL.RequestURI())

	if r.Method == http.MethodPost {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		g.title, g.body = payload["title"], payload["body"]
		if g.postStatus != 0 {
			http.Error(w, "denied", g.postStatus)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"number": 99, "html_url": "https://github.com/example/sigil/issues/99"}`)
		return
	}

	if g.listStatus != 0 {
		http.Error(w, "unavailable", g.listStatus)
		return
	}
	page := 1
	_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
	var issues []issue
	if page <= len(g.pages) {
		issues = g.pages[page-1]
	}
	if page < len(g.pages) {
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, g.url, r.URL.Path, page+1))
	}
	_ = json.NewEncoder(w).Encode(issues)
}

// lookUp answers the requests for the run's jobs, a job's annotations
// and the run's artifacts, and reports whether it was one of them.
func (g *fakeGitHub) lookUp(w http.ResponseWriter, r *http.Request) bool {
	var answer any
	switch path := r.URL.Path; {
	case path == "/repos/example/sigil/actions/runs/123/attempts/2/jobs":
		answer = map[string][]runJob{"jobs": g.jobs}
	case path == "/repos/example/sigil/actions/runs/123/artifacts":
		answer = map[string][]runArtifact{"artifacts": g.artifacts}
	case strings.HasPrefix(path, "/repos/example/sigil/check-runs/"):
		var id int64
		_, _ = fmt.Sscanf(strings.TrimPrefix(path, "/repos/example/sigil/check-runs/"), "%d/annotations", &id)
		notes := make([]map[string]string, 0, len(g.annotations[id]))
		for _, m := range g.annotations[id] {
			notes = append(notes, map[string]string{"annotation_level": "failure", "message": m})
		}
		answer = notes
	default:
		return false
	}
	if g.lookupStatus != 0 {
		http.Error(w, "denied", g.lookupStatus)
		return true
	}
	_ = json.NewEncoder(w).Encode(answer)
	return true
}

// writeResults writes a fuzz job's results.json into dir, as the job's
// downloaded artifact holds it.
func writeResults(t *testing.T, dir string, r runResults) {
	t.Helper()
	dir = filepath.Join(dir, "fuzz-results")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, resultsFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func campaignIssue(n int) issue {
	return otherIssue(n, "details\n"+marker+"\n")
}

func otherIssue(n int, body string) issue {
	return issue{Number: n, Body: &body}
}

func pullRequest(n int) issue {
	i := campaignIssue(n)
	i.PullRequest = &struct {
		URL string `json:"url"`
	}{URL: "https://example.com/pr"}
	return i
}
