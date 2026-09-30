//go:build e2e

package e2e

import (
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

var _ = Describe("Routing one alert", func() {
	DescribeTable("answers 200 with the route the team's policy chose",
		func(c fixture.RouteCase) {
			resp, out := alertrouter.Route(Default, c.Team, c.Request)
			fixture.ExpectRouteCase(Default, c, resp, out)
		},
		fixture.Entries(fixture.RouteCases()),
	)

	Context("when the request can't be evaluated", func() {
		It("answers 404 for a team the directory doesn't list", func() {
			resp, body := alertrouter.PostJSON(Default, fixture.RoutePath("marketing"),
				fixture.FiringAlert(fixture.CheckoutErrorRate, fixture.SeverityCritical))

			Expect(resp).To(HaveHTTPStatus(http.StatusNotFound))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error).NotTo(BeNil())
		})

		DescribeTable("refuses a body it won't evaluate",
			func(c fixture.BadRequestCase) {
				resp, raw := alertrouter.PostRaw(Default, fixture.RoutePath(fixture.TeamCheckout), c.Body)

				Expect(resp).To(HaveHTTPStatus(c.Status))
				Expect(fixture.Decode[fixture.ErrorResponse](Default, raw).Error).NotTo(BeNil())
			},
			fixture.Entries(fixture.RouteBadRequestCases()),
		)
	})
})

var _ = Describe("Receiving an Alertmanager webhook", func() {
	It("routes every firing alert, acknowledges the resolved one and loses none", func() {
		batch := fixture.MixedBatch(time.Now())
		resp, out := alertrouter.Webhook(Default, batch.Webhook)
		fixture.ExpectBatch(Default, batch, resp, out)
	})

	DescribeTable("answers 400 for a payload it can't read",
		func(c fixture.BadRequestCase) {
			resp, raw := alertrouter.PostRaw(Default, fixture.PathAlerts, c.Body)

			Expect(resp).To(HaveHTTPStatus(c.Status))
			Expect(fixture.Decode[fixture.ErrorResponse](Default, raw).Error).NotTo(BeNil())
		},
		fixture.Entries(fixture.WebhookBadRequestCases()),
	)
})

// The files under requests/ are what demo-cli, the README's curl examples
// and k6 send, and requests/cases.json says what each must answer. Running
// them against the container keeps the three honest.
var _ = Describe("The sample requests", func() {
	DescribeTable("answer what requests/cases.json expects",
		func(c fixture.ManifestCase) {
			fixture.ExpectManifestCase(Default, alertrouter, c)
		},
		fixture.Entries(fixture.ManifestCases()),
	)
})
