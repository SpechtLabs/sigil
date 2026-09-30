package integration

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/access"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/store"
	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
)

// deploygate serves the bundles embedded in its binary when no directory is
// mounted, and the compose stack mounts policies/teams and policies/access.
// The two must be the same policies, or the binary would behave differently
// the moment someone mounts the directories it was built from.
var _ = Describe("The embedded bundles", func() {
	Context("DeployApproval", func() {
		var embedded, onDisk *store.Snapshot[deploy.Input]

		BeforeEach(func() {
			embedded = loadSnapshot(store.NewDeploy(store.WithTeams(fixture.TeamPayments, fixture.TeamCheckout)))
			onDisk = loadSnapshot(store.NewDeploy(store.WithTeams(fixture.TeamPayments, fixture.TeamCheckout),
				store.WithBundleDir(filepath.Join(fixture.ExamplesDir, "policies", "teams"))))
		})

		It("serves the same teams and policies as the directory it was built from", func() {
			Expect(embedded.Source).To(Equal(store.SourceEmbedded))
			Expect(embedded.PolicyNames()).To(Equal(onDisk.PolicyNames()))
			Expect(embedded.PolicyNames()).To(Equal([]string{"payments.production", "checkout.production"}))
		})

		DescribeTable("decides every case the same way as the directory",
			func(c fixture.DecisionCase) {
				in := deployInput(c)
				got := evaluate(embedded, c.Team, in)
				want := evaluate(onDisk, c.Team, in)

				Expect(got.Decision).To(Equal(c.Decision))
				Expect(got.Reason).To(Equal(c.Reason))
				Expect(got.Decision).To(Equal(want.Decision))
				Expect(got.Reason).To(Equal(want.Reason))
				Expect(got.Payload).To(Equal(want.Payload))
			},
			fixture.Entries(fixture.DecisionCases()),
		)
	})

	Context("AccessGrant", func() {
		var embedded, onDisk *store.Snapshot[access.Input]

		BeforeEach(func() {
			embedded = loadSnapshot(store.NewAccess())
			onDisk = loadSnapshot(store.NewAccess(store.WithBundleDir(filepath.Join(fixture.ExamplesDir, "policies", "access"))))
		})

		It("serves the same root as the directory it was built from", func() {
			Expect(embedded.Source).To(Equal(store.SourceEmbedded))
			Expect(embedded.PolicyNames()).To(Equal(onDisk.PolicyNames()))
			Expect(embedded.PolicyNames()).To(Equal([]string{fixture.AccessPolicy}))
		})

		DescribeTable("grants every case the same roles as the directory",
			func(c fixture.AccessCase) {
				in := accessInput(c.Request)
				got := evaluate(embedded, fixture.AccessPolicy, in)
				want := evaluate(onDisk, fixture.AccessPolicy, in)

				Expect(outcome(got)).To(Equal(outcome(want)))
				Expect(got.Outcome).To(HaveLen(len(c.Grants)))
			},
			fixture.Entries(fixture.AccessCases()),
		)
	})
})

// loadSnapshot loads st the way the service does at startup and returns what
// it loaded.
func loadSnapshot[In any](st *store.Store[In]) *store.Snapshot[In] {
	GinkgoHelper()

	Expect(st.InitialLoad(context.Background())).To(Succeed())

	snap, ok := st.Snapshot()
	Expect(ok).To(BeTrue())

	return snap
}

// evaluate runs the policy snap serves under key, a team for the deploy
// store or the root's name for the access store, and expects no error.
func evaluate[In any](snap *store.Snapshot[In], key string, in In) *policy.Result {
	GinkgoHelper()

	p, ok := snap.Policy(key)
	Expect(ok).To(BeTrue(), "no policy for %q", key)

	res, err := p.Eval(context.Background(), in)
	Expect(err).NotTo(HaveOccurred())

	return res
}

// outcome lists a collecting result's entries as decision, reason and
// payload, the parts two bundles must agree on.
func outcome(res *policy.Result) []any {
	out := make([]any, 0, len(res.Outcome))
	for _, e := range res.Outcome {
		out = append(out, []any{e.Decision, e.Reason, e.Payload})
	}

	return out
}

// deployInput builds the deploy kind's input the server builds for c: the
// groups become the teams and the roles are the ones the access stage
// derives, taken from the case.
func deployInput(c fixture.DecisionCase) deploy.Input {
	GinkgoHelper()

	r := c.Request
	soak, err := time.ParseDuration(r.Release.Soak)
	Expect(err).NotTo(HaveOccurred())

	return deploy.Input{
		Release: deploy.Release{Soak: soak, Hotfix: r.Release.Hotfix},
		Service: deploy.Service{
			Name:   r.Service.Name,
			Tier:   deploy.Tier(r.Service.Tier),
			Owners: r.Service.Owners,
			Labels: r.Service.Labels,
		},
		Actor: deploy.Actor{
			Name:    r.Actor.Name,
			Teams:   r.Actor.Groups,
			Roles:   c.Roles,
			Regions: r.Actor.Regions,
		},
		Environment: r.Environment,
	}
}

// accessInput converts a wire access request into the access kind's input.
func accessInput(r fixture.AccessRequest) access.Input {
	return access.Input{
		Actor:       access.Actor{Name: r.Actor.Name, Groups: r.Actor.Groups, Clearance: r.Actor.Clearance},
		Team:        r.Team,
		Environment: r.Environment,
	}
}
