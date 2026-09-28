package integration

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/policies"
	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// deploygate serves the teams bundle embedded in its binary when no
// directory is mounted, and the compose stack mounts policies/teams. The two
// must be the same policies, or the binary would behave differently the
// moment someone mounts the directory it was built from.
var _ = Describe("The embedded teams bundle", func() {
	var embedded, onDisk *store.Snapshot

	BeforeEach(func() {
		embedded = loadSnapshot(store.WithTeamsFS(policies.Teams, store.SourceEmbedded))
		onDisk = loadSnapshot(store.WithTeamsDir(filepath.Join(fixture.ExamplesDir, "policies", "teams")))
	})

	It("serves the same teams and policies as the directory it was built from", func() {
		Expect(embedded.Source).To(Equal(store.SourceEmbedded))
		Expect(embedded.Teams).To(Equal(onDisk.Teams))
		Expect(embedded.PolicyNames()).To(Equal([]string{"payments.production", "checkout.production"}))
	})

	DescribeTable("decides every case the same way as the directory",
		func(c fixture.DecisionCase) {
			in := input(c.Request)

			fromEmbedded, ok := embedded.Policy(c.Team)
			Expect(ok).To(BeTrue())
			fromDisk, ok := onDisk.Policy(c.Team)
			Expect(ok).To(BeTrue())

			want, err := fromDisk.Eval(context.Background(), in)
			Expect(err).NotTo(HaveOccurred())
			got, err := fromEmbedded.Eval(context.Background(), in)
			Expect(err).NotTo(HaveOccurred())

			Expect(got.Decision).To(Equal(c.Decision))
			Expect(got.Reason).To(Equal(c.Reason))
			Expect(got.Decision).To(Equal(want.Decision))
			Expect(got.Reason).To(Equal(want.Reason))
			Expect(got.Payload).To(Equal(want.Payload))
		},
		decisionEntries(),
	)
})

// loadSnapshot loads the served teams from the given bundle with the
// embedded platform documents, the way the service does, and returns what
// it loaded.
func loadSnapshot(opt store.Option) *store.Snapshot {
	GinkgoHelper()

	st := store.New(deploy.Kind, store.WithTeams(fixture.TeamPayments, fixture.TeamCheckout), opt)
	Expect(st.Load(context.Background())).To(Succeed())

	snap, ok := st.Snapshot()
	Expect(ok).To(BeTrue())

	return snap
}

// input converts a wire request into the kind's input, the conversion the
// server makes, for evaluating a policy without HTTP in between.
func input(r fixture.DeployRequest) deploy.Input {
	GinkgoHelper()

	soak, err := time.ParseDuration(r.Release.Soak)
	Expect(err).NotTo(HaveOccurred())

	return deploy.Input{
		Release: deploy.Release{Soak: soak, Hotfix: r.Release.Hotfix},
		Service: deploy.Service{
			Name:   r.Service.Name,
			Tier:   r.Service.Tier,
			Owners: r.Service.Owners,
			Labels: r.Service.Labels,
		},
		Actor: deploy.Actor{
			Name:    r.Actor.Name,
			Teams:   r.Actor.Teams,
			Roles:   r.Actor.Roles,
			Regions: r.Actor.Regions,
		},
		Environment: r.Environment,
	}
}
