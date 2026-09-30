// Package integration runs alertrouter in process: the real store, server,
// team directory, metrics and policies, served by httptest, with spans
// captured by an in-memory exporter and every notification recorded. It
// covers the same contract as the end-to-end suite, and what only an
// in-process test can see, without Docker: exact metric values on a fresh
// registry, every span attribute and event, every notification the
// dispatcher got, a store that never loaded, and polling driven by a fake
// clock.
package integration

import (
	"testing"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shared serves the checked-in team policies for the specs that only read.
// Specs that count metrics, spans or notifications, change policies or need
// an unloaded store build an env of their own, so they never see each
// other's traffic.
var shared *env

// TestIntegration hands the suite to Ginkgo.
func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "alertrouter integration")
}

var _ = BeforeSuite(func() {
	// Debug mode prints every route of every env's router.
	gin.SetMode(gin.TestMode)

	shared = newEnv()
})
