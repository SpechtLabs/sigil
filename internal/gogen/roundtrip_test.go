package gogen_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gogen/internal/golden/accessgrant"
	"github.com/spechtlabs/sigil/internal/gogen/internal/golden/alertrouting"
	"github.com/spechtlabs/sigil/internal/gogen/internal/golden/deployapproval"
	"github.com/spechtlabs/sigil/internal/gogen/internal/golden/name_clash"
	"github.com/spechtlabs/sigil/internal/gogen/internal/golden/rankedaccess"
	"github.com/spechtlabs/sigil/internal/gogen/internal/golden/shapes"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Each golden package's kind, built through its generated constructor.
// The functions are never called by the round trip; the eval tests below
// call deployapproval's split.
var (
	deploy = deployapproval.NewKind(deployapproval.Funcs{
		Split: func(s, sep string) ([]string, error) { return strings.Split(s, sep), nil },
	})
	shapesKind = shapes.NewKind(shapes.Funcs{
		Lookup: func(shapes.Tier, []shapes.Region, *string) (map[string]int64, error) { return nil, nil },
		Now:    func() (time.Time, error) { return time.Time{}, nil },
	})
	clash = name_clash.NewKind(name_clash.Funcs{
		GetURL:  func(s string) (string, error) { return s, nil },
		GetURL2: func(s string) (string, error) { return s, nil },
	})
	access  = accessgrant.NewKind()
	routing = alertrouting.NewKind()
	ranked  = rankedaccess.NewKind()
)

// TestRoundTrip is the generator's oracle: the kind each golden package
// builds exports exactly the kind file it was generated from, in the
// form the kind loader prints it back, which is what Kind.Load compares
// a kind document in a bundle against.
func TestRoundTrip(t *testing.T) {
	tests := []struct {
		file   string
		schema string
	}{
		{file: "access_grant.sigil", schema: access.Schema()},
		{file: "alert_routing.sigil", schema: routing.Schema()},
		{file: "deploy_approval.sigil", schema: deploy.Schema()},
		{file: "name_clash.sigil", schema: clash.Schema()},
		{file: "ranked_access.sigil", schema: ranked.Schema()},
		{file: "shapes.sigil", schema: shapesKind.Schema()},
	}
	paths, err := filepath.Glob(filepath.Join("testdata", "*.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(tests) {
		t.Fatalf("%d testdata kind files but %d round trips; add the new golden package here", len(paths), len(tests))
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			src := read(t, filepath.Join("testdata", tt.file))
			k, errs := check.LoadKind(tt.file, src)
			if errs != nil {
				t.Fatal(errs)
			}
			if want := k.Source(); tt.schema != want {
				t.Errorf("generated kind exports\n%s\nwant\n%s", tt.schema, want)
			}
			// The kind files are written in canonical form, so the round
			// trip holds byte for byte against the file itself too.
			if tt.schema != string(src) {
				t.Errorf("generated kind exports\n%s\nwant the file\n%s", tt.schema, src)
			}
		})
	}
}

// TestRoundTripLoadsTheKindFile loads a bundle that holds the kind file
// next to a policy, which Kind.Load accepts only when the kind document
// matches the generated kind.
func TestRoundTripLoadsTheKindFile(t *testing.T) {
	fsys := policy.MapFS(map[string]string{
		"deploy_approval.sigil": string(read(t, filepath.Join("testdata", "deploy_approval.sigil"))),
		"deploy.sigil":          "policy deploy: DeployApproval@2\n\nwhen environment == \"staging\" {\n  approve(reason: release_manager)\n}\n",
	})
	if _, err := deploy.Load(fsys, "deploy"); err != nil {
		t.Fatal(err)
	}
}

// TestGeneratedTypesEvaluate evaluates policies against the generated
// kinds and reads the results with type switches on the generated
// payload types and reason switches on the generated handles.
func TestGeneratedTypesEvaluate(t *testing.T) {
	const deployPolicy = `policy deploy: DeployApproval@2

when service.tier == critical and any t in actor.teams: "payments" in split(t, "/") {
  approve(reason: payments_sre, bake: 2h)
}
when release.hotfix {
  review(reason: service_owner, approvers: service.owners)
}
`
	p, err := deploy.Compile(deployPolicy, "deploy")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input deployapproval.Input
		want  string
	}{
		{
			name: "approve",
			input: deployapproval.Input{
				Actor:   deployapproval.Actor{Teams: []string{"payments/sre"}},
				Service: deployapproval.Service{Tier: deployapproval.TierCritical},
			},
			want: "approve payments_sre bake=2h0m0s",
		},
		{
			name: "review",
			input: deployapproval.Input{
				Release: deployapproval.Release{Hotfix: true},
				Service: deployapproval.Service{Tier: deployapproval.TierStandard, Owners: []string{"ana"}},
			},
			want: "review service_owner approvers=[ana]",
		},
		{name: "default", input: deployapproval.Input{Service: deployapproval.Service{Tier: deployapproval.TierInternal}}, want: "deny no_rule_matched"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := p.Eval(context.Background(), tt.input)
			if err != nil {
				t.Fatal(err)
			}
			var got string
			switch d := res.Value().(type) {
			case deployapproval.ApproveData:
				got = "approve " + res.Reason + " bake=" + d.Bake.String()
			case deployapproval.ReviewData:
				got = "review " + res.Reason + " approvers=[" + strings.Join(d.Approvers, ",") + "]"
			case deployapproval.DenyData:
				got = "deny " + res.Reason
			default:
				t.Fatalf("Value() = %T, want a generated payload type", d)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			switch res.Why() {
			case deployapproval.ApprovePaymentsSre, deployapproval.ReviewServiceOwner, deployapproval.DenyNoRuleMatched:
			default:
				t.Errorf("Why() = %v, want one of the generated reason handles", res.Why())
			}
		})
	}
}

// TestGeneratedCollectingKindEvaluates reads a collecting kind's outcome
// entry by entry, with the generated payload types.
func TestGeneratedCollectingKindEvaluates(t *testing.T) {
	const grants = `policy grants: AccessGrant@1

when team in actor.groups {
  deployer(reason: team_member)
  reader(reason: team_member)
}
`
	p, err := access.Compile(grants, "grants")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Eval(context.Background(), accessgrant.Input{Team: "pay", Actor: accessgrant.Actor{Groups: []string{"pay"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range res.Outcome {
		switch d := e.Value().(type) {
		case accessgrant.ReaderData:
			got = append(got, "reader")
		case accessgrant.DeployerData:
			got = append(got, "deployer "+d.TTL.String())
		default:
			t.Fatalf("Value() = %T, want reader or deployer", d)
		}
		if e.Why() != accessgrant.ReaderTeamMember && e.Why() != accessgrant.DeployerTeamMember {
			t.Errorf("Why() = %v, want a team_member handle", e.Why())
		}
	}
	if want := []string{"reader", "deployer 8h0m0s"}; strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("outcome = %v, want %v", got, want)
	}
}

// TestNewKindWithoutFuncs checks that a generated constructor refuses a
// Funcs with an unset field, naming every one.
func TestNewKindWithoutFuncs(t *testing.T) {
	tests := []struct {
		name  string
		build func()
		want  string
	}{
		{
			name:  "one missing",
			build: func() { deployapproval.NewKind(deployapproval.Funcs{}) },
			want:  "deployapproval.NewKind: no implementation for host functions: Funcs.Split",
		},
		{
			name:  "two missing",
			build: func() { shapes.NewKind(shapes.Funcs{}) },
			want:  "shapes.NewKind: no implementation for host functions: Funcs.Lookup, Funcs.Now",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tt.want {
					t.Errorf("panic = %v, want %q", got, tt.want)
				}
			}()
			tt.build()
		})
	}
}

// TestNewKindPassesOptions checks that a generated constructor applies
// the host behavior it's given after the declaring options, and that the
// contract stays the kind file's.
func TestNewKindPassesOptions(t *testing.T) {
	k := deployapproval.NewKind(deployapproval.Funcs{
		Split: func(string, string) ([]string, error) { panic("split broke") },
	}, policy.WithRecoverHostPanics())
	if k.Schema() != deploy.Schema() {
		t.Fatalf("Schema() with a host option =\n%s\nwant\n%s", k.Schema(), deploy.Schema())
	}
	p, err := k.Compile("policy deploy: DeployApproval@2\n\nwhen \"x\" in split(environment, \"/\") {\n  approve(reason: release_manager)\n}\n", "deploy")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Eval(context.Background(), deployapproval.Input{Environment: "a/x"})
	if _, ok := errors.AsType[*policy.RuntimeError](err); !ok {
		t.Fatalf("Eval() error = %v, want a *policy.RuntimeError from the recovered panic", err)
	}
	if res.Why() != deployapproval.DenyNoRuleMatched {
		t.Errorf("Why() = %v, want the default", res.Why())
	}
}
