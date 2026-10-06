package lsp

import (
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/types"
)

// TestFixes checks the quick fixes of the diagnostics of each source,
// which replaces production.sigil in the test workspace: each fix as its
// title and what its edit leaves in the source.
func TestFixes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // "title => the edited text around the edit"
	}{
		{name: "a misspelled name", src: "when servce.name == \"\" {\n  deny(reason: not_eligible)\n}\n", want: []string{"Change to `service` => when service.name"}},
		{name: "a misspelled field", src: "when service.nme == \"\" {\n  deny(reason: not_eligible)\n}\n", want: []string{"Change to `name` => when service.name =="}},
		{name: "a misspelled enum value", src: "when service.tier == critcal {\n  deny(reason: not_eligible)\n}\n", want: []string{"Change to `critical` => == critical {"}},
		{name: "a misspelled reason", src: "when cleared {\n  deny(reason: not_eligble)\n}\n", want: []string{"Change to `not_eligible` => (reason: not_eligible)"}},
		{name: "a misspelled payload field", src: "when cleared {\n  review(reason: service_owner, approvrs: [])\n}\n", want: []string{"Change to `approvers` => , approvers: []", "Add the missing field `approvers` => approvrs: [], approvers: [])"}},
		{name: "a map key that reads a name", src: "pub let m = {servce: \"x\"}\n", want: []string{`Write the string key "servce" => {"servce": "x"}`, "Change to `service` => {service: \"x\"}"}},
		{name: "a missing field", src: "when cleared {\n  review(reason: service_owner)\n}\n", want: []string{"Add the missing field `approvers` => review(reason: service_owner, approvers: [])"}},
		{name: "a missing field after a trailing comma", src: "when cleared {\n  review(\n    reason: service_owner,\n  )\n}\n", want: []string{"Add the missing field `approvers` => service_owner,\n   approvers: [])"}},
		{name: "a misspelled imported let", src: "use deploy.common.{owns_servce as o}\n", want: []string{"Change to `owns_service` => {owns_service as o}"}},
		{name: "a misspelled decision", src: "assert(\"x\", outcome.revew == [])\n", want: []string{"Change to `review` => outcome.review"}},
		{name: "a quoted reason", src: "when cleared {\n  deny(reason: \"not_eligible\")\n}\n", want: []string{"Write the reason as `not_eligible` => (reason: not_eligible)"}},
		{name: "a qualified reason", src: "when cleared {\n  deny(reason: deny.not_eligible)\n}\n", want: []string{"Write the reason as `not_eligible` => (reason: not_eligible)"}},
		{name: "a positional reason", src: "when cleared {\n  deny(not_eligible)\n}\n", want: []string{"Write `reason: not_eligible` => deny(reason: not_eligible)"}},
		{name: "a positional reason misspelled", src: "when cleared {\n  deny(not_eligble)\n}\n", want: []string{"Write `reason: not_eligible` => deny(reason: not_eligible)"}},
		{name: "an unknown reason", src: "when cleared {\n  deny(reason: nope)\n}\n", want: []string{"Change to `not_eligible` => (reason: not_eligible)", "Change to `soak_too_short` => (reason: soak_too_short)", "Change to `no_rule_matched` => (reason: no_rule_matched)"}},
		{name: "nothing to fix", src: "when cleared {\n  deny(reason: not_eligible, extra: 1)\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := head + tt.src
			l0 := &memLoader{files: testWorkspace(t)}
			snap := l0.Load(root, map[string][]byte{root + "/production.sigil": []byte(src)})
			k := newView(snap.Project, root+"/production.sigil", []byte(src)).kindNamed("DeployApproval").Model
			l := newLines([]byte(src), "utf-16")
			var got []string
			for _, e := range snap.Diagnostics {
				if e.File != root+"/production.sigil" {
					continue
				}
				data := fixesOf(e, []byte(src), k, l)
				if data == nil {
					continue
				}
				for _, fix := range data.Fixes {
					from, to := l.offset(fix.Edit.Range.Start), l.offset(fix.Edit.Range.End)
					if src[from:to] != fix.Replaces {
						t.Errorf("%s replaces %q, want %q", fix.Title, src[from:to], fix.Replaces)
					}
					edited := src[:from] + fix.Edit.NewText + src[to:]
					got = append(got, fix.Title+" => "+around(edited, from, len(fix.Edit.NewText)))
				}
			}
			for _, want := range tt.want {
				if !slices.ContainsFunc(got, func(g string) bool {
					return strings.HasPrefix(want, strings.SplitN(g, " => ", 2)[0]) && strings.Contains(g, strings.SplitN(want, " => ", 2)[1])
				}) {
					t.Errorf("fixes = %q, want one like %q", got, want)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("fixes = %q, want %d", got, len(tt.want))
			}
		})
	}
}

// around returns the edited text from 30 bytes before the edit to 30
// after it, for a test to look for what the edit left.
func around(s string, from, n int) string {
	return s[max(0, from-30):min(len(s), from+n+30)]
}

// TestZeroOf checks the literal a missing field is filled with, by its
// type.
func TestZeroOf(t *testing.T) {
	tests := []struct {
		typ  types.Type
		want string // "" for none
	}{
		{types.String, `""`},
		{types.Int, "0"},
		{types.Float, "0.0"},
		{types.Bool, "false"},
		{types.Duration, "0s"},
		{types.Timestamp, ""},
		{&types.List{Elem: types.String}, "[]"},
		{&types.Map{Key: types.String, Value: types.Int}, "{}"},
		{&types.Optional{Elem: types.Int}, "none"},
		{&types.Enum{Name: "Tier", Values: []string{"critical", "standard"}}, "Tier.critical"},
		{&types.Enum{Name: "Empty"}, ""},
		{&types.Struct{Name: "Ticket"}, ""},
	}
	for _, tt := range tests {
		got, ok := zeroOf(tt.typ)
		if got != tt.want || ok != (tt.want != "") {
			t.Errorf("zeroOf(%s) = %q, %v, want %q", tt.typ, got, ok, tt.want)
		}
	}
}
