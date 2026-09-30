// Package client talks to the running deploygate service over HTTP. One
// [Client] is shared by every demo-cli command; the root binds its --url
// and --timeout flags to the client's fields.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/input"
)

// Client holds the connection settings shared by the commands. The root
// binds flags to these fields so commands see their parsed values.
type Client struct {
	// URL is deploygate's base URL, http or https, without a query or
	// fragment.
	URL string
	// Timeout bounds each request, and must be positive.
	Timeout time.Duration
}

// New returns a client for $DEPLOYGATE_URL, or http://localhost:8080 when it
// is unset, with a ten-second timeout.
func New() *Client {
	target := os.Getenv("DEPLOYGATE_URL")
	if target == "" {
		target = "http://localhost:8080"
	}
	return &Client{URL: target, Timeout: 10 * time.Second}
}

// Do sends one request without retrying or following redirects, and returns
// the status and the body, read up to 8 MiB. path is appended to URL as it
// is. A non-nil body must be valid JSON and is sent as application/json. It
// returns an error when URL or Timeout is invalid, the request can't be
// sent, or the body is larger than the limit; any status is a response,
// not an error.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (int, []byte, humane.Error) {
	target, herr := c.targetURL(path)
	if herr != nil {
		return 0, nil, herr
	}
	if body != nil && !json.Valid(body) {
		return 0, nil, humane.New("the request is not valid JSON", "supply a JSON request with --file, or use a built-in scenario")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, humane.Wrap(err, "cannot create the deploygate request", "check --url")
	}
	req.Header.Set("Accept", "application/json")
	if path == "/metrics" {
		req.Header.Set("Accept", "text/plain")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout: c.Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, humane.Wrap(err, "cannot reach deploygate", "start the service with mise run up, or check --url and --timeout")
	}
	defer func() { _ = resp.Body.Close() }()
	data, herr := input.ReadLimited(resp.Body, 8<<20)
	return resp.StatusCode, data, herr
}

func (c *Client) targetURL(path string) (string, humane.Error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		return "", humane.New("invalid deploygate URL", "set --url or DEPLOYGATE_URL to an HTTP(S) URL without a query or fragment")
	}
	if c.Timeout <= 0 {
		return "", humane.New("the HTTP timeout must be positive", "set --timeout to a duration such as 10s")
	}
	return strings.TrimRight(u.String(), "/") + path, nil
}
