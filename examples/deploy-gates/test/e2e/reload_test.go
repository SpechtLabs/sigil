//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/deploy-gates/test/internal/fixture"
)

// What the hot reload specs change in the bind-mounted bundles.
const (
	paymentsPolicy = "payments/production.sigil"
	sreRule        = "approve(reason: payments_sre, bake: 15m)"
	sreRuleEdited  = "approve(reason: payments_sre, bake: 30m)"

	accessPolicy     = "main.sigil"
	oncallRule       = "deployer(reason: oncall, ttl: 2h)"
	oncallRuleEdited = "deployer(reason: oncall, ttl: 3h)"

	brokenFile = "broken.sigil"

	// brokenDocument has a valid header, so the loader indexes it, and an
	// unfinished condition, so it fails to parse and takes the whole
	// bundle down with it.
	brokenDocument = "policy broken.production: DeployApproval@1\n\nwhen service.tier == {\n  deny(reason: not_eligible)\n}\n"
)

// Serial because these specs change what every other spec is evaluated
// against. Each one registers its restore before it touches a file, so a
// failed assertion or an interrupted run still puts the bundle back.
var _ = Describe("Hot reload", Serial, func() {
	Context("when a team edits its deploy policy", func() {
		It("serves the edited rule after a reload and the original after restoring it", func() {
			dir := requireWritable(policiesDir, "DEPLOYGATE_POLICIES_DIR", "DeployApproval")
			editFile(filepath.Join(dir, paymentsPolicy), sreRule, sreRuleEdited, func() { expectSREBake("15m") })

			expectReloadOK()
			expectSREBake("30m")
		})
	})

	Context("when the platform edits the access policy", func() {
		It("grants the edited time to live after a reload and the original after restoring it", func() {
			dir := requireWritable(accessPoliciesDir, "DEPLOYGATE_ACCESS_POLICIES_DIR", "AccessGrant")
			editFile(filepath.Join(dir, accessPolicy), oncallRule, oncallRuleEdited, func() { expectOncallTTL("2h") })

			expectReloadOK()
			expectOncallTTL("3h")
		})
	})

	Context("when a broken document lands in the team bundle", func() {
		It("rejects the reload and keeps serving the last good bundle", func() {
			dir := requireWritable(policiesDir, "DEPLOYGATE_POLICIES_DIR", "DeployApproval")
			path := filepath.Join(dir, brokenFile)
			_, err := os.Stat(path)
			Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue(),
				"%s already exists; the suite won't overwrite a file it didn't create", path)

			good, _ := deploygate.ListPolicies(Default).Kind("DeployApproval")
			failure := fixture.Labels{"kind": "DeployApproval", "result": "failure"}
			failuresBefore := scrapeMetrics(Default).Value(fixture.MetricReloads, failure)

			DeferCleanup(func() {
				Expect(os.Remove(path)).To(Succeed(), "removing %s", path)
				expectReloadOK()
				Expect(scrapeMetrics(Default).Value(fixture.MetricReloadOK, fixture.Labels{"kind": "DeployApproval"})).
					To(BeNumerically("==", 1), "the team bundle loaded again, so its latest load succeeded")
			})

			Expect(os.WriteFile(path, []byte(brokenDocument), 0o644)).To(Succeed())

			resp, body := deploygate.Reload(Default)
			Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))
			// The compile diagnostics name the file, so whoever broke the
			// bundle sees where from the reload response alone.
			herr := fixture.Decode[fixture.ErrorResponse](Default, body).Error
			Expect(herr).NotTo(BeNil())
			Expect(herr.Messages()).To(ContainElement(ContainSubstring(brokenFile + ":3:")))

			By("still evaluating with the last good bundle")
			resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest())
			Expect(resp).To(HaveHTTPStatus(http.StatusAccepted))
			Expect(out.Reason).To(Equal(fixture.ReasonServiceOwner))
			now, _ := deploygate.ListPolicies(Default).Kind("DeployApproval")
			Expect(now.LoadedAt).To(BeTemporally("==", good.LoadedAt))

			By("counting the failed reload and marking only the team bundle as failing")
			families := scrapeMetrics(Default)
			Expect(families.Value(fixture.MetricReloads, failure) - failuresBefore).
				To(BeNumerically(">=", 1))
			Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": "DeployApproval"})).To(BeNumerically("==", 0))
			// The time is that of the bundle that still serves, to the
			// nanosecond the listing reports.
			Expect(families.Value(fixture.MetricLastReload, fixture.Labels{"kind": "DeployApproval"})).
				To(BeNumerically("~", float64(good.LoadedAt.UnixNano())/float64(time.Second), 1e-3))
			// The same POST reloaded the access bundle, which is fine.
			Expect(families.Value(fixture.MetricReloadOK, fixture.Labels{"kind": "AccessGrant"})).To(BeNumerically("==", 1))
		})
	})
})

// requireWritable returns dir, the host side of a compose bind mount, and
// skips the spec when the suite can't change what the service reads: the
// directory is missing or read-only here, or the service serves the kind
// from its embedded bundle.
func requireWritable(dir, envVar, kind string) string {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		Skip(envVar + " " + dir + " is not a directory")
	}

	// The probe starts with a dot, which the bundle loader skips, so a
	// reload racing with it never sees the file.
	probe, err := os.CreateTemp(dir, ".e2e-probe-*")
	if err != nil {
		Skip(envVar + " " + dir + " is not writable: " + err.Error())
	}

	Expect(probe.Close()).To(Succeed())
	Expect(os.Remove(probe.Name())).To(Succeed())

	served, ok := deploygate.ListPolicies(Default).Kind(kind)
	Expect(ok).To(BeTrue(), "deploygate doesn't list %s", kind)
	if served.Source == "embedded" {
		Skip("deploygate serves its embedded " + kind + " bundle, so editing " + dir + " changes nothing")
	}

	return dir
}

// editFile replaces the one occurrence of from in path with to, and
// registers the restore first: the original bytes go back, the service
// reloads, and verify checks the original behavior is back.
func editFile(path, from, to string, verify func()) {
	GinkgoHelper()

	original, mode := readFile(path)
	Expect(bytes.Count(original, []byte(from))).To(Equal(1),
		"%s no longer holds %q once; update the spec with the policy", path, from)

	DeferCleanup(func() {
		Expect(os.WriteFile(path, original, mode)).To(Succeed(), "restoring %s", path)
		expectReloadOK()
		verify()
	})

	Expect(os.WriteFile(path, bytes.Replace(original, []byte(from), []byte(to), 1), mode)).To(Succeed())
}

func readFile(path string) ([]byte, fs.FileMode) {
	info, err := os.Stat(path)
	Expect(err).NotTo(HaveOccurred())

	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())

	return data, info.Mode().Perm()
}

func expectReloadOK() {
	GinkgoHelper()

	resp, _ := deploygate.Reload(Default)
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
}

// expectSREBake asserts the bake the payments SRE approval carries, which is
// the one value the deploy policy edit changes.
func expectSREBake(bake string) {
	GinkgoHelper()

	resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Groups("payments-sre")))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	Expect(out.Reason).To(Equal("payments_sre"))
	Expect(out.Payload).To(MatchJSON(`{"bake": "` + bake + `"}`))
}

// expectOncallTTL asserts the time to live of the on-call deployer grant,
// which is the one value the access policy edit changes.
func expectOncallTTL(ttl string) {
	GinkgoHelper()

	resp, out := deploygate.Access(Default, fixture.AccessFor("payments-sre"))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	Expect(out.Grants).To(HaveLen(1))
	Expect(out.Grants[0].Reason).To(Equal("oncall"))
	Expect(out.Grants[0].TTL).To(Equal(ttl))
}
