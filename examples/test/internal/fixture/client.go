package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onsi/gomega"
)

// The API's paths, as a client spells them.
const (
	PathPolicies = "/api/v1/policies"
	PathReload   = "/api/v1/policies/reload"
	PathHealthz  = "/healthz"
	PathReadyz   = "/readyz"
	PathMetrics  = "/metrics"
)

// requestTimeout bounds every call a Client makes. The servers under test run
// locally, so anything slower than this is a hang, not a slow answer.
const requestTimeout = 10 * time.Second

// Client calls one HTTP server and fails the running spec on transport
// errors, so the specs only state what they expect of the answer. Every
// method takes the Gomega to fail, which is Default in a spec body and the
// argument of the polled function inside Eventually.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient returns a client for the server at baseURL, such as
// http://localhost:8080 or an httptest server's URL.
func NewClient(baseURL string) *Client {
	return &Client{
		http:    &http.Client{Timeout: requestTimeout},
		baseURL: strings.TrimSuffix(baseURL, "/"),
	}
}

// DeploymentsPath is the evaluation endpoint of team.
func DeploymentsPath(team string) string {
	return "/api/v1/teams/" + team + "/deployments"
}

// Deploy evaluates req against team's policy and returns the response with
// its decoded body. A body that isn't JSON is left zero, and the status
// matcher reports it.
func (c *Client) Deploy(g gomega.Gomega, team string, req DeployRequest) (*http.Response, DecisionResponse) {
	resp, body := c.PostJSON(g, DeploymentsPath(team), req)
	// Send already read the body and left a reader over the copy, so
	// closing it only marks the response as handled.
	defer func() { _ = resp.Body.Close() }()

	var out DecisionResponse
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		g.Expect(json.Unmarshal(body, &out)).To(gomega.Succeed(), "decoding %s", body)
	}

	return resp, out
}

// ListPolicies returns what GET /api/v1/policies reports, and expects it to
// answer 200.
func (c *Client) ListPolicies(g gomega.Gomega) PoliciesResponse {
	resp, body := c.Get(g, PathPolicies)
	defer func() { _ = resp.Body.Close() }()

	g.Expect(resp).To(gomega.HaveHTTPStatus(http.StatusOK))

	return Decode[PoliciesResponse](g, body)
}

// Reload asks the server to reload its team policies now.
func (c *Client) Reload(g gomega.Gomega) (*http.Response, []byte) {
	return c.Send(g, http.MethodPost, PathReload, "", nil)
}

// Get fetches path, which may carry a query string.
func (c *Client) Get(g gomega.Gomega, path string) (*http.Response, []byte) {
	return c.Send(g, http.MethodGet, path, "", nil)
}

// PostJSON posts v rendered as JSON to path.
func (c *Client) PostJSON(g gomega.Gomega, path string, v any) (*http.Response, []byte) {
	body, err := json.Marshal(v)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	return c.PostRaw(g, path, string(body))
}

// PostRaw posts body to path as JSON, byte for byte, which is how the specs
// send bodies a well-behaved encoder never would.
func (c *Client) PostRaw(g gomega.Gomega, path, body string) (*http.Response, []byte) {
	return c.Send(g, http.MethodPost, path, "application/json", strings.NewReader(body))
}

// Send performs one request and reads the whole body. It puts a fresh reader
// back on the response, because HaveHTTPStatus prints the body when it fails
// and an already drained body would make that message empty.
func (c *Client) Send(g gomega.Gomega, method, path, contentType string, body io.Reader) (*http.Response, []byte) {
	req, err := http.NewRequestWithContext(context.Background(), method, c.baseURL+path, body)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	g.Expect(err).NotTo(gomega.HaveOccurred(), "%s %s%s", method, c.baseURL, path)

	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	g.Expect(err).NotTo(gomega.HaveOccurred())

	resp.Body = io.NopCloser(bytes.NewReader(data))

	return resp, data
}

// Decode unmarshals body into a T, failing the spec when it doesn't decode.
func Decode[T any](g gomega.Gomega, body []byte) T {
	var out T
	g.Expect(json.Unmarshal(body, &out)).To(gomega.Succeed(), "decoding %s", body)

	return out
}
