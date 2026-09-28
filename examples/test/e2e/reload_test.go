//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spechtlabs/sigil/examples/test/internal/fixture"
)

// What the hot reload specs change in the bind-mounted team policies.
const (
	paymentsPolicy = "payments/production.sigil"
	sreRule        = "approve(payments_sre, bake: 15m)"
	sreRuleEdited  = "approve(payments_sre, bake: 30m)"

	brokenFile = "broken.sigil"

	// brokenDocument has a valid header, so the loader indexes it, and an
	// unfinished condition, so it fails to parse and takes the whole
	// bundle down with it.
	brokenDocument = "policy broken.production: DeployApproval@1\n\nwhen service.tier == {\n  deny(not_eligible)\n}\n"
)

// Serial because these specs change what every other spec is evaluated
// against. Each one registers its restore before it touches a file, so a
// failed assertion or an interrupted run still puts the bundle back.
var _ = Describe("Hot reload", Serial, func() {
	var dir string

	BeforeEach(func() {
		dir = requireWritablePolicies()
	})

	Context("when a team edits its policy", func() {
		It("serves the edited rule after a reload and the original after restoring it", func() {
			path := filepath.Join(dir, paymentsPolicy)
			original, mode := readFile(path)
			Expect(bytes.Count(original, []byte(sreRule))).To(Equal(1),
				"%s no longer holds %q once; update the spec with the policy", path, sreRule)

			DeferCleanup(func() {
				Expect(os.WriteFile(path, original, mode)).To(Succeed(), "restoring %s", path)
				expectReloadOK()
				expectSREBake("15m")
			})

			Expect(os.WriteFile(path, bytes.Replace(original, []byte(sreRule), []byte(sreRuleEdited), 1), mode)).
				To(Succeed())
			expectReloadOK()
			expectSREBake("30m")
		})
	})

	Context("when a broken document lands in the bundle", func() {
		It("rejects the reload and keeps serving the last good bundle", func() {
			path := filepath.Join(dir, brokenFile)
			_, err := os.Stat(path)
			Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue(),
				"%s already exists; the suite won't overwrite a file it didn't create", path)

			good := deploygate.ListPolicies(Default)
			failure := fixture.Labels{"result": "failure"}
			failuresBefore := scrapeMetrics(Default).Value(fixture.MetricReloads, failure)

			DeferCleanup(func() {
				Expect(os.Remove(path)).To(Succeed(), "removing %s", path)
				expectReloadOK()
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
			Expect(deploygate.ListPolicies(Default).LoadedAt).To(BeTemporally("==", good.LoadedAt))

			By("counting the failed reload")
			Expect(scrapeMetrics(Default).Value(fixture.MetricReloads, failure) - failuresBefore).
				To(BeNumerically(">=", 1))
		})
	})
})

// requireWritablePolicies returns the host directory behind the compose bind
// mount, and skips the spec when the suite can't change what the service
// reads: the directory is missing or read-only here, or the service runs on
// its embedded teams bundle.
func requireWritablePolicies() string {
	info, err := os.Stat(policiesDir)
	if err != nil || !info.IsDir() {
		Skip("DEPLOYGATE_POLICIES_DIR " + policiesDir + " is not a directory")
	}

	// The probe starts with a dot, which the bundle loader skips, so a
	// reload racing with it never sees the file.
	probe, err := os.CreateTemp(policiesDir, ".e2e-probe-*")
	if err != nil {
		Skip("DEPLOYGATE_POLICIES_DIR " + policiesDir + " is not writable: " + err.Error())
	}

	Expect(probe.Close()).To(Succeed())
	Expect(os.Remove(probe.Name())).To(Succeed())

	if src := deploygate.ListPolicies(Default).Source; src == "embedded" {
		Skip("deploygate serves its embedded teams bundle, so editing " + policiesDir + " changes nothing")
	}

	return policiesDir
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
// the one value the edit changes.
func expectSREBake(bake string) {
	GinkgoHelper()

	resp, out := deploygate.Deploy(Default, fixture.TeamPayments, fixture.OwnerRequest(fixture.Teams("payments-sre")))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	Expect(out.Reason).To(Equal("payments_sre"))
	Expect(out.Payload).To(MatchJSON(`{"bake": "` + bake + `"}`))
}
