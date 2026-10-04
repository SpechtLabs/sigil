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
	summaries := t.TempDir()
	short := filepath.Join(summaries, "short.md")
	long := filepath.Join(summaries, "long.md")
	for file, content := range map[string]string{
		short: "# Go fuzzing\n\n**32 of 33 targets passed**\n\n## Failures\n\n**FuzzPatch** failed.\n",
		long:  "# Go fuzzing\n\n" + strings.Repeat("| FuzzX | ./x | 1 | 0 | ✓ passed |\n", 3000),
	} {
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		env  map[string]string
		args []string
		// pages are the open issues GitHub lists, one slice per page.
		pages      [][]issue
		listStatus int
		postStatus int

		wantErr   string
		wantOut   string
		wantCalls []string
		wantBody  []string
	}{
		{
			name:      "first failure opens an issue",
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantOut:   "Opened issue #99",
			wantBody: []string{
				marker,
				"https://github.com/example/sigil/actions/runs/123/attempts/2",
				"https://github.com/example/sigil/commit/abc123",
				"https://github.com/example/sigil/actions/runs/123#artifacts",
				"Trigger: schedule",
				"60m, with two workers",
				"reproduction command printed in fuzz-results/fuzz.log",
			},
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
			wantBody:  []string{"Target discovery: failure", "Fuzz jobs: skipped"},
		},
		{
			name:      "manual dispatch reports the selected duration",
			env:       map[string]string{"GITHUB_EVENT_NAME": "workflow_dispatch"},
			args:      []string{"--time", "10m"},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantBody:  []string{"10m, with two workers", "Trigger: workflow_dispatch"},
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
			name:      "the campaign's summary goes into the issue, a level down",
			args:      []string{"--summary", short},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantBody:  []string{"Keep the input\nwith its fix so ordinary tests replay it.\n\n## Go fuzzing", "**32 of 33 targets passed**", "### Failures"},
		},
		{
			name:      "a summary too long for an issue is cut short",
			args:      []string{"--summary", long},
			wantCalls: []string{"GET /repos/example/sigil/issues?state=open&per_page=100", "POST /repos/example/sigil/issues"},
			wantBody:  []string{"| ✓ passed |\n\n_The summary is cut short here; the [run's summary page](https://github.com/example/sigil/actions/runs/123) has all of it._"},
		},
		{name: "a missing summary file fails before calling GitHub", args: []string{"--summary", filepath.Join(summaries, "missing.md")}, wantErr: "can't read the summary"},
		{
			name:    "skipped runs need no details",
			args:    []string{"--time="},
			env:     map[string]string{"GITHUB_REF": "refs/heads/feature"},
			wantOut: "didn't run on main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gh := &fakeGitHub{pages: tt.pages, listStatus: tt.listStatus, postStatus: tt.postStatus}
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
	mu         sync.Mutex
	url        string
	pages      [][]issue
	listStatus int
	postStatus int

	calls []string
	body  string
	auth  string
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, r.Method+" "+r.URL.RequestURI())
	g.auth = r.Header.Get("Authorization")

	if r.Method == http.MethodPost {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		g.body = payload["body"]
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
