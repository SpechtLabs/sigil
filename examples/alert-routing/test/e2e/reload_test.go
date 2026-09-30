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

	"github.com/spechtlabs/sigil/examples/alert-routing/test/internal/fixture"
)

// What the hot reload specs change in the bind-mounted bundle.
const (
	checkoutPolicy = "checkout/alerts.sigil"
	checkoutRules  = `paging(page_after: 10m)`
	checkoutEdited = `paging(page_after: 20m)`

	brokenFile = "broken.sigil"

	// brokenDocument has a valid header, so the loader indexes it, and an
	// unfinished condition, so it fails to parse and takes the whole
	// bundle down with it.
	brokenDocument = "policy broken.alerts: AlertRouting@1\n\nwhen alert.severity == {\n  drop(reason: muted)\n}\n"

	// unpaged is a checkout policy that leaves out the platform's paging,
	// which the host's Require rejects no matter what else it says.
	unpaged = "policy checkout.alerts: AlertRouting@1\n\nuse platform.routing\n\nrouting()\n"
)

// Serial because these specs change what every other spec is evaluated
// against. Each one registers its restore before it touches a file, so a
// failed assertion or an interrupted run still puts the bundle back.
var _ = Describe("Hot reload", Serial, func() {
	Context("when a team edits its policy", func() {
		It("serves the edited threshold after a reload and the original after restoring it", func() {
			dir := requireWritable(policiesDir)
			editFile(filepath.Join(dir, checkoutPolicy), checkoutRules, checkoutEdited, func() { expectCheckoutPagesAfter12m(true) })

			expectReloadOK()
			expectCheckoutPagesAfter12m(false)
		})
	})

	Context("when the new bundle doesn't load", func() {
		DescribeTable("rejects the reload and keeps serving the last good bundle",
			func(write func(dir string), diagnostic string) {
				dir := requireWritable(policiesDir)
				good := alertrouter.Served(Default)
				failure := fixture.Labels{"result": "failure"}
				failuresBefore := scrapeMetrics(Default).Value(fixture.MetricReloads, failure)

				write(dir)

				resp, body := alertrouter.Reload(Default)
				Expect(resp).To(HaveHTTPStatus(http.StatusInternalServerError))
				// The diagnostics name what is wrong, so whoever broke the
				// bundle sees it from the reload response alone.
				herr := fixture.Decode[fixture.ErrorResponse](Default, body).Error
				Expect(herr).NotTo(BeNil())
				Expect(herr.Messages()).To(ContainElement(ContainSubstring(diagnostic)))

				By("still routing with the last good bundle")
				expectCheckoutPagesAfter12m(true)
				now := alertrouter.Served(Default)
				Expect(now.LoadedAt).To(BeTemporally("==", good.LoadedAt))
				Expect(now.Fingerprint).To(Equal(good.Fingerprint))

				By("counting the failed reload and marking the latest load as failed")
				families := scrapeMetrics(Default)
				Expect(families.Value(fixture.MetricReloads, failure) - failuresBefore).To(BeNumerically(">=", 1))
				Expect(families.Value(fixture.MetricReloadOK, nil)).To(BeNumerically("==", 0))
				// The time is that of the bundle that still serves.
				Expect(families.Value(fixture.MetricLastReload, nil)).
					To(BeNumerically("~", float64(good.LoadedAt.UnixNano())/float64(time.Second), 1e-3))
			},
			Entry("a document that doesn't parse", func(dir string) {
				createFile(filepath.Join(dir, brokenFile), brokenDocument)
			}, brokenFile+":3:"),
			Entry("a team policy that leaves out the platform's paging", func(dir string) {
				replaceFile(filepath.Join(dir, checkoutPolicy), unpaged)
			}, "checkout.alerts doesn't invoke platform.paging"),
		)
	})
})

// requireWritable returns dir, the host side of the compose bind mount, and
// skips the spec when the suite can't change what the service reads: the
// directory is missing or read-only here, or the service serves its
// embedded bundle.
func requireWritable(dir string) string {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		Skip("ALERTROUTER_POLICIES_DIR " + dir + " is not a directory")
	}

	// The probe starts with a dot, which the bundle loader skips, so a
	// reload racing with it never sees the file.
	probe, err := os.CreateTemp(dir, ".e2e-probe-*")
	if err != nil {
		Skip("ALERTROUTER_POLICIES_DIR " + dir + " is not writable: " + err.Error())
	}

	Expect(probe.Close()).To(Succeed())
	Expect(os.Remove(probe.Name())).To(Succeed())

	if alertrouter.Served(Default).Source == "embedded" {
		Skip("alertrouter serves its embedded bundle, so editing " + dir + " changes nothing")
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

// replaceFile replaces path with content, and registers the restore first:
// the original bytes go back and the service reloads them.
func replaceFile(path, content string) {
	GinkgoHelper()

	original, mode := readFile(path)
	DeferCleanup(func() {
		Expect(os.WriteFile(path, original, mode)).To(Succeed(), "restoring %s", path)
		expectReloadOK()
	})

	Expect(os.WriteFile(path, []byte(content), mode)).To(Succeed())
}

// createFile writes a new file at path, and registers its removal first. It
// refuses to overwrite a file it didn't create.
func createFile(path, content string) {
	GinkgoHelper()

	_, err := os.Stat(path)
	Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue(),
		"%s already exists; the suite won't overwrite a file it didn't create", path)

	DeferCleanup(func() {
		Expect(os.Remove(path)).To(Succeed(), "removing %s", path)
		expectReloadOK()
		Expect(scrapeMetrics(Default).Value(fixture.MetricReloadOK, nil)).
			To(BeNumerically("==", 1), "the bundle loaded again, so its latest load succeeded")
	})

	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
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

	resp, _ := alertrouter.Reload(Default)
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
}

// expectCheckoutPagesAfter12m asserts whether a checkout warning that has
// fired for twelve minutes pages, which is the one outcome the threshold
// edit changes: past the shipped ten minutes it pages, short of the edited
// twenty it posts to the team's channel.
func expectCheckoutPagesAfter12m(pages bool) {
	GinkgoHelper()

	resp, out := alertrouter.Route(Default, fixture.TeamCheckout,
		fixture.FiringAlert(fixture.CheckoutLatency, fixture.SeverityWarning, fixture.FiringFor("12m")))
	Expect(resp).To(HaveHTTPStatus(http.StatusOK))
	if pages {
		Expect(out.Decision).To(Equal(fixture.DecisionPage))
		Expect(out.Reason).To(Equal(fixture.ReasonSustained))
		return
	}
	Expect(out.Decision).To(Equal(fixture.DecisionNotify))
	Expect(out.Channel).To(Equal(fixture.CheckoutChannel))
}
