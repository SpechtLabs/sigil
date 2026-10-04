package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
)

// reasonChangeFreeze is the deny reason of a deploy to a frozen environment.
const reasonChangeFreeze = "change_freeze"

var _ = Describe("A change freeze", func() {
	It("denies a deploy to a frozen environment from the guardrails, and says which freeze it read", func() {
		e := newEnv(withFreeze(freeze.NewStatic(fixture.EnvProduction)))
		e.spans.Reset()

		resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())

		Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))
		Expect(out.Decision).To(Equal(fixture.DecisionDeny))
		Expect(out.Reason).To(Equal(reasonChangeFreeze))
		Expect(out.Freeze).To(Equal(&fixture.Freeze{Environments: []string{fixture.EnvProduction}}))
		Expect(out.Winners()).To(ConsistOf(HaveField("Policy", "deploy.guardrails")))

		Expect(attrs(e.waitForSpan(spanEvaluate).Attributes)).To(And(
			HaveKeyWithValue("sigil.freeze.environments", []string{fixture.EnvProduction}),
			HaveKeyWithValue("sigil.freeze.unknown", false),
			HaveKeyWithValue("sigil.reason", reasonChangeFreeze),
		))
		Expect(e.families().Value(fixture.MetricDecisions, fixture.Labels{
			"team": fixture.TeamPayments, "decision": fixture.DecisionDeny, "reason": reasonChangeFreeze,
		})).To(BeNumerically("==", 1))
	})

	It("refuses a request that claims a freeze of its own", func() {
		e := newEnv(withFreeze(freeze.NewStatic(fixture.EnvProduction)))

		resp, body := e.client.PostRaw(Default, fixture.DeploymentsPath(fixture.TeamPayments),
			fixture.OwnerRequest().JSONWithField("freeze", "none"))

		Expect(resp).To(HaveHTTPStatus(http.StatusBadRequest))
		Expect(fixture.Decode[fixture.ErrorResponse](Default, body).Error.Messages()).To(ContainElement(ContainSubstring(`unknown field "freeze"`)))
	})

	// The input the policy read, with the freeze the response reports,
	// decides the same way again: the freeze is data in the input, not part
	// of the policy, which is what makes a logged decision replayable.
	It("replays: the reported freeze in the input decides the same way again", func() {
		e := newEnv(withFreeze(freeze.NewStatic(fixture.EnvProduction)))
		_, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
		Expect(out.Access.Grants).To(ContainElement(HaveField("Role", "deployer")))

		in := deployInput(fixture.DecisionCase{Request: fixture.OwnerRequest(), Roles: []string{"deployer"}})
		in.Freeze = deploy.Freeze{Environments: out.Freeze.Environments, Unknown: out.Freeze.Unknown}
		Expect(evalDeploy(e, in)).To(Equal(reasonChangeFreeze))

		in.Freeze = deploy.Freeze{Environments: []string{}}
		Expect(evalDeploy(e, in)).To(Equal(fixture.ReasonServiceOwner))
	})

	Context("read from an OFREP flag service", func() {
		var (
			flags *ofrepFlags
			clock *fakeClock
			src   *freeze.OFREP
			e     *env
		)

		BeforeEach(func() {
			flags = newOFREPFlags()
			clock = newFakeClock(clockStart)
			var herr error
			src, herr = freeze.NewOFREP(flags.srv.URL, freeze.WithClock(clock),
				freeze.WithEvaluationContext(map[string]string{"region": "eu-1"}))
			Expect(herr).To(Succeed())
			e = newEnv(withFreeze(src))
		})

		It("denies while the flag freezes production and approves again once it's off", func() {
			flags.value.Store(`"production"`)
			Expect(src.Refresh(context.Background())).To(Succeed())
			resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))
			Expect(out.Reason).To(Equal(reasonChangeFreeze))

			flags.value.Store(`""`)
			Expect(src.Refresh(context.Background())).To(Succeed())
			resp, out = e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
			Expect(out.Reason).To(Equal(fixture.ReasonServiceOwner))
			Expect(out.Freeze).To(Equal(&fixture.Freeze{Environments: []string{}}))
		})

		It("sends the evaluation context it was given", func() {
			Expect(src.Refresh(context.Background())).To(Succeed())
			Expect(flags.lastBody.Load()).To(MatchJSON(`{"context":{"targetingKey":"deploygate","region":"eu-1"}}`))
		})

		It("keeps the last answer through an outage, then fails closed past the staleness", func() {
			flags.value.Store(`"staging"`)
			Expect(src.Refresh(context.Background())).To(Succeed())

			flags.srv.Close()
			Expect(src.Refresh(context.Background())).NotTo(Succeed())
			resp, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
			Expect(out.Freeze).To(Equal(&fixture.Freeze{Environments: []string{"staging"}}))

			clock.advance(2 * freeze.DefaultMaxStaleness)
			resp, out = e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusForbidden))
			Expect(out.Reason).To(Equal(reasonChangeFreeze))
			Expect(out.Freeze).To(Equal(&fixture.Freeze{Environments: []string{"staging"}, Unknown: true}))
		})

		It("refreshes in the background until its context ends", func() {
			flags.value.Store(`"production"`)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				src.Run(ctx)
			}()

			// Run receives a tick only after it handled the one before, so
			// after the second send the first refresh has finished.
			clock.tick()
			clock.tick()
			cancel()
			Eventually(done).WithTimeout(5 * time.Second).Should(BeClosed())

			Expect(flags.requests.Load()).To(BeNumerically(">=", 1))
			_, out := e.client.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(out.Reason).To(Equal(reasonChangeFreeze))
		})
	})
})

// ofrepFlags is an OFREP service in the shape featuregate answers, serving
// the change-freeze flag as a string flag whose value a spec sets.
type ofrepFlags struct {
	srv      *httptest.Server
	value    atomic.Value
	lastBody atomic.Value
	requests atomic.Int32
}

func newOFREPFlags() *ofrepFlags {
	f := &ofrepFlags{}
	f.value.Store(`""`)
	f.lastBody.Store("")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		f.lastBody.Store(string(body))
		if r.URL.Path != "/ofrep/v1/evaluate/flags/"+freeze.DefaultFlag {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"key":"x","errorCode":"FLAG_NOT_FOUND"}`)
			return
		}
		_, _ = io.WriteString(w, `{"key":"change-freeze","value":`+f.value.Load().(string)+
			`,"reason":"TARGETING_MATCH","metadata":{"sigil.policy":"flags.change_freeze"}}`)
	}))
	DeferCleanup(f.srv.Close)
	return f
}

// evalDeploy evaluates payments' policy from e's store against in and
// returns the reason it decided with.
func evalDeploy(e *env, in deploy.Input) string {
	GinkgoHelper()

	p, ok := e.store.Policy(fixture.TeamPayments)
	Expect(ok).To(BeTrue())
	res, err := p.Eval(context.Background(), in)
	Expect(err).NotTo(HaveOccurred())
	return res.Reason
}
