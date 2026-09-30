//go:build e2e

// Package e2e tests alertrouter from the outside, over HTTP, against the
// docker compose stack in examples/alert-routing/. It never imports the
// service: every assertion is about the wire contract, metrics in Mimir, logs
// in Loki, traces in Tempo and profiles in Pyroscope, which is what
// Alertmanager, a client or an operator sees.
//
// Start the stack and run the suite with `mise run e2e` from
// examples/alert-routing/. The endpoints come from ALERTROUTER_URL, MIMIR_URL,
// TEMPO_URL, LOKI_URL, PYROSCOPE_URL and GRAFANA_URL. The hot reload and
// timeout specs edit the team policies under ALERTROUTER_POLICIES_DIR.
package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// The suite's configuration. The defaults match the ports docker-compose.yaml
// publishes, so a plain `go test -tags e2e ./test/e2e/...` works against a
// local stack.
var (
	alertrouterURL = envOr("ALERTROUTER_URL", "http://localhost:8080")
	tempoURL       = envOr("TEMPO_URL", "http://localhost:3200")
	mimirURL       = envOr("MIMIR_URL", "http://localhost:9009")
	lokiURL        = envOr("LOKI_URL", "http://localhost:3100")
	pyroscopeURL   = envOr("PYROSCOPE_URL", "http://localhost:4040")
	grafanaURL     = envOr("GRAFANA_URL", "http://localhost:3000")

	// policiesDir is the host side of the compose bind mount, relative to
	// this package's directory because that is where `go test` runs.
	policiesDir = envOr("ALERTROUTER_POLICIES_DIR", fixture.ExamplesDir+"/policies/teams")
)

// One client per backend, shared by every spec.
var (
	alertrouter = fixture.NewClient(alertrouterURL)
	tempo       = fixture.NewClient(tempoURL)
	mimir       = fixture.NewClient(mimirURL)
	loki        = fixture.NewClient(lokiURL)
	pyroscope   = fixture.NewClient(pyroscopeURL)
	grafana     = fixture.NewClient(grafanaURL)
)

// TestE2E hands the suite to Ginkgo.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "alertrouter e2e")
}

var _ = BeforeSuite(func() {
	// `docker compose up --wait` returns once the healthchecks pass, but the
	// suite may also run against a stack that is still starting, so it waits
	// for readiness itself instead of failing every spec with a refused
	// connection.
	Eventually(func(g Gomega) {
		resp, _ := alertrouter.Get(g, fixture.PathReadyz)
		g.Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	}).WithTimeout(60*time.Second).WithPolling(time.Second).Should(Succeed(),
		"alertrouter at %s never became ready; is the compose stack up?", alertrouterURL)
})

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}

	return fallback
}
