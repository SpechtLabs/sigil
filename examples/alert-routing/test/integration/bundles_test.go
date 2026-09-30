package integration

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// alertrouter serves the bundle embedded in its binary when no directory is
// mounted, and the compose stack mounts policies/teams. The two must be the
// same policies, or the binary would behave differently the moment someone
// mounts the directory it was built from.
var _ = Describe("The embedded bundle", func() {
	var embedded, onDisk *store.Snapshot[routing.Input]

	BeforeEach(func() {
		names := teams.Default().Names()
		embedded = loadSnapshot(store.NewRouting(store.WithTeams(names...)))
		onDisk = loadSnapshot(store.NewRouting(store.WithTeams(names...),
			store.WithBundleDir(filepath.Join(fixture.ExamplesDir, "policies", "teams"))))
	})

	It("serves the same teams, policies and content as the directory it was built from", func() {
		Expect(embedded.Source).To(Equal(store.SourceEmbedded))
		Expect(embedded.PolicyNames()).To(Equal(onDisk.PolicyNames()))
		Expect(embedded.PolicyNames()).To(Equal([]string{"checkout.alerts", "payments.alerts"}))
		Expect(embedded.Fingerprint).To(Equal(onDisk.Fingerprint))
	})

	DescribeTable("routes every case the same way as the directory",
		func(c fixture.RouteCase) {
			in := routingInput(c)
			got := evaluate(embedded, c.Team, in)
			want := evaluate(onDisk, c.Team, in)

			Expect(got.Decision).To(Equal(c.Want.Decision))
			Expect(got.Reason).To(Equal(c.Want.Reason))
			Expect(got.Decision).To(Equal(want.Decision))
			Expect(got.Reason).To(Equal(want.Reason))
			Expect(got.Payload).To(Equal(want.Payload))
		},
		fixture.Entries(fixture.RouteCases()),
	)
})

// loadSnapshot loads st the way the service does at startup and returns what
// it loaded.
func loadSnapshot(st *store.Store[routing.Input]) *store.Snapshot[routing.Input] {
	GinkgoHelper()

	Expect(st.InitialLoad(context.Background())).To(Succeed())

	snap, ok := st.Snapshot()
	Expect(ok).To(BeTrue())

	return snap
}

// evaluate runs team's policy from snap and expects no error.
func evaluate(snap *store.Snapshot[routing.Input], team string, in routing.Input) *policy.Result {
	GinkgoHelper()

	p, ok := snap.Policy(team)
	Expect(ok).To(BeTrue(), "no policy for %q", team)

	res, err := p.Eval(context.Background(), in)
	Expect(err).NotTo(HaveOccurred())

	return res
}

// routingInput builds the kind's input the server builds for c: the alert
// as sent, and the team as the default directory lists it.
func routingInput(c fixture.RouteCase) routing.Input {
	GinkgoHelper()

	a := c.Request.Alert
	firingFor, err := time.ParseDuration(a.FiringFor)
	Expect(err).NotTo(HaveOccurred())
	severity, ok := routing.ParseSeverity(a.Severity)
	Expect(ok).To(BeTrue(), "case %q has severity %q", c.Name, a.Severity)
	team, ok := teams.Default().Lookup(c.Team)
	Expect(ok).To(BeTrue(), "case %q is for team %q", c.Name, c.Team)

	return routing.Input{
		Alert: routing.Alert{Name: a.Name, Severity: severity, Labels: a.Labels, FiringFor: firingFor},
		Team:  team,
	}
}
