package deploy_test

import (
	"flag"
	"os"
	"testing"

	"github.com/spechtlabs/sigil/pkg/build"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/policies"
)

// platformDir holds the platform's trusted documents, where the rendered
// vocabulary lives at its default path, deploy/freeze.sigil.
const platformDir = "../../policies/platform"

var update = flag.Bool("update", false, "rewrite the rendered vocabulary under policies/platform")

// TestVocabularyIsCurrent fails when policies/platform/deploy/freeze.sigil
// isn't what FreezeModule renders. Regenerate it with
// `go test ./internal/deploy -run Vocabulary -update`.
func TestVocabularyIsCurrent(t *testing.T) {
	doc := deploy.FreezeModule()
	if *update {
		if err := build.Write(platformDir, doc); err != nil {
			t.Fatalf("writing %s: %v", doc.Path(), err)
		}
	}
	if err := build.Diff(os.DirFS(platformDir), doc); err != nil {
		t.Fatalf("%v\nregenerate it with: go test ./internal/deploy -run Vocabulary -update", err)
	}
}

// TestVocabularyChecks type-checks the rendered module against the kind,
// next to the handwritten platform documents that import it, the way the
// service loads them as its trusted source: the deploy/ view of the
// platform tree, since the access/ documents are of another kind.
func TestVocabularyChecks(t *testing.T) {
	platformDeploy := policies.Only(os.DirFS(platformDir), "deploy")
	if err := build.Check(deploy.Kind, platformDeploy, deploy.FreezeModule()); err != nil {
		t.Fatal(err)
	}
}
