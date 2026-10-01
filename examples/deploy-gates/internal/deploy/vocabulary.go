package deploy

import "github.com/spechtlabs/sigil/pkg/build"

// FreezeModule builds deploy.freeze, the platform's vocabulary for the
// change freeze, in Go: is_frozen is true when the input's freeze names the
// environment or is unknown. The rendered module is committed as
// policies/platform/deploy/freeze.sigil, reviewed like any policy, and
// served from the platform's trusted documents; deploy.guardrails imports
// is_frozen and denies with change_freeze.
//
// The module reads the freeze, it doesn't hold it. The host fills
// [Input.Freeze] from its flag system on every request, so a flag flip
// changes the input, never this module: nothing reloads, and a logged input
// replays to the same decision. Each call builds a new document; nothing is
// registered anywhere. TestVocabularyIsCurrent fails when the committed file
// is stale, and `go test ./internal/deploy -run Vocabulary -update` rewrites
// it.
func FreezeModule() *build.ModuleDoc[Input] {
	return build.Module("deploy.freeze", Kind, func(m *build.ModuleDoc[Input], in *Input) {
		m.Comment("freeze is data the host fills in on every request, never part of this\n" +
			"module. An unknown freeze counts as frozen, so the policy fails closed.")
		build.Pub(m, "is_frozen", build.Or(
			build.Field(&in.Freeze.Unknown),
			build.In(build.Field(&in.Environment), build.Field(&in.Freeze.Environments)),
		))
	})
}
