package lsp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// policy is the body of the policy the hover and definition tests point
// into, with the marker at the cursor: it reads a name of every sort.
const policy = `policy payments.production: DeployApproval@2

use deploy.common.{cleared, owns_service as owns}
use deploy.common as shared
use deploy.guardrails

param limit: int = 3, min: 1
param tiers: list<Tier> = [Tier.critical]

guardrails(min_soak: 4h)

let critical_change = service.tier == critical and release.risk == Risk.high
let has_ticket = ticket?.approved ?? false

assert("named_actor", actor.name != "" and all r in outcome.review: r.tier in tiers)
assert("one_approval", outcome.approve.release_manager == [] or approve.payments_sre in outcome)

when cleared and owns and shared.owns_service and limit > 0 {
  let reviewers = filter o in service.owners: o != actor.name
  review(reason: service_owner, approvers: reviewers, tier: critical)
}

when critical_change and any c in changes: (c.hotfix and has_ticket) {
  approve(reason: release_manager, bake: 2h)
}

when split(environment, "-") == [] {
  deny(reason: not_eligible)
}
`

// TestTarget hovers over and goes to the definition of the name at each
// cursor, the text the marker follows: hover must hold every
// string in hover, and definition go to def, as file:line:column, or
// nowhere when it's empty.
func TestTarget(t *testing.T) {
	tests := []struct {
		at    string // the text before the marker, which policy holds once
		back  int    // how far before the end of at the marker goes
		hover []string
		def   string
	}{
		{at: "DeployApproval", hover: []string{"kind DeployApproval version 2", "input release: Release"}, def: "deploy_approval.sigil:1:6"},
		{at: "common as", back: 3, hover: []string{"module deploy.common: DeployApproval@2", "pub let cleared: bool", "pub let owns_service: bool"}, def: "common.sigil:1:8"},
		{at: "{cleared", hover: []string{"pub let cleared: bool", "From `deploy.common`."}, def: "common.sigil:4:9"},
		{at: "owns_service as owns", hover: []string{"pub let owns_service: bool"}, def: "common.sigil:3:9"},
		{at: "as shared", hover: []string{"module deploy.common"}, def: "common.sigil:1:8"},
		{at: "use deploy.guardrails", hover: []string{"policy deploy.guardrails: DeployApproval@2", "param min_soak: duration = 24h, min: 1h, max: 48h"}, def: "guardrails.sigil:1:8"},
		{at: "param limit", hover: []string{"param limit: int = 3, min: 1"}, def: "production.sigil:7:7"},
		{at: "list<Tier", hover: []string{"enum Tier: critical | standard | internal"}, def: "deploy_approval.sigil:3:6"},
		{at: "[Tier.critical", hover: []string{"`critical`, a value of `Tier`."}, def: "deploy_approval.sigil:3:12"},
		{at: "\nguardrails", hover: []string{"policy deploy.guardrails"}, def: "guardrails.sigil:1:8"},
		{at: "guardrails(min_soak", hover: []string{"param min_soak: duration = 24h, min: 1h, max: 48h", "A param of `deploy.guardrails`."}, def: "guardrails.sigil:3:7"},
		{at: "let critical_change", hover: []string{"let critical_change: bool"}, def: "production.sigil:12:5"},
		{at: "= service", hover: []string{"input service: Service", "type Service {\n  name: string"}, def: "deploy_approval.sigil:32:7"},
		{at: "service.tier", hover: []string{"tier: Tier", "A field of `Service`.", "enum Tier"}, def: "deploy_approval.sigil:20:3"},
		{at: "== critical", hover: []string{"`critical`, a value of `Tier`."}, def: "deploy_approval.sigil:3:12"},
		{at: "Risk.high", hover: []string{"`high`, a value of `Risk`."}, def: "deploy_approval.sigil:5:29"},
		{at: "== Risk", hover: []string{"enum Risk: low | standard | high"}, def: "deploy_approval.sigil:5:6"},
		{at: "ticket?.approved", hover: []string{"approved: bool", "A field of `Ticket`."}, def: "deploy_approval.sigil:15:3"},
		{at: "= ticket", hover: []string{"input ticket: ?Ticket", "type Ticket {"}, def: "deploy_approval.sigil:35:7"},
		{at: "all r", hover: []string{"r: review candidate"}, def: "production.sigil:15:48"},
		{at: "outcome.review", hover: []string{"decision review {"}, def: "deploy_approval.sigil:44:10"},
		{at: "r.tier", hover: []string{"tier: Tier = standard", "A payload field of `review`."}, def: "deploy_approval.sigil:47:3"},
		{at: "in tiers", hover: []string{"param tiers: list<Tier> = [Tier.critical]"}, def: "production.sigil:8:7"},
		{at: "outcome.approve.release_manager", hover: []string{"`release_manager`, a reason of `approve`."}, def: "deploy_approval.sigil:51:11"},
		{at: "or approve", hover: []string{"decision approve {"}, def: "deploy_approval.sigil:50:10"},
		{at: "approve.payments_sre", hover: []string{"`payments_sre`, a reason of `approve`."}, def: "deploy_approval.sigil:51:29"},
		{at: "when cleared", hover: []string{"pub let cleared: bool"}, def: "common.sigil:4:9"},
		{at: "and owns", hover: []string{"pub let owns_service: bool"}, def: "common.sigil:3:9"},
		{at: "shared.owns_service", hover: []string{"pub let owns_service: bool", "From `deploy.common`."}, def: "common.sigil:3:9"},
		{at: "and shared", hover: []string{"module deploy.common"}, def: "common.sigil:1:8"},
		{at: "and limit", hover: []string{"param limit: int = 3, min: 1"}, def: "production.sigil:7:7"},
		{at: "filter o", hover: []string{"o: string"}, def: "production.sigil:19:26"},
		{at: "o != actor.name", hover: []string{"name: string", "A field of `Actor`."}, def: "deploy_approval.sigil:26:3"},
		{at: "o !", hover: nil},
		{at: "filter o in service.owners: o", hover: []string{"o: string"}, def: "production.sigil:19:26"},
		{at: "  review", hover: []string{"decision review {", "approvers: list<string>"}, def: "deploy_approval.sigil:44:10"},
		{at: "reason: service_owner", hover: []string{"`service_owner`, a reason of `review`."}, def: "deploy_approval.sigil:45:11"},
		{at: "review(reason", hover: []string{"reason: service_owner", "The reason of `review`."}, def: "deploy_approval.sigil:45:3"},
		{at: "approvers: reviewers", hover: []string{"let reviewers: list<string>"}, def: "production.sigil:19:7"},
		{at: "approvers", hover: []string{"approvers: list<string>", "A payload field of `review`."}, def: "deploy_approval.sigil:46:3"},
		{at: "tier: critical", hover: []string{"`critical`, a value of `Tier`."}, def: "deploy_approval.sigil:3:12"},
		{at: "any c", hover: []string{"c: Release", "type Release {"}, def: "production.sigil:23:30"},
		{at: "c.hotfix", hover: []string{"hotfix: bool", "A field of `Release`."}, def: "deploy_approval.sigil:9:3"},
		{at: "and has_ticket", hover: []string{"let has_ticket: bool"}, def: "production.sigil:13:5"},
		{at: "when split", hover: []string{"fn split(string, string) -> list<string>"}, def: "deploy_approval.sigil:38:4"},
		{at: "bake", hover: []string{"bake: duration = 1h"}, def: "deploy_approval.sigil:52:3"},
		{at: "2h", hover: nil},
		{at: "\"-\"", hover: nil},
		{at: "pol", hover: nil},
	}
	for _, tt := range tests {
		t.Run(tt.at, func(t *testing.T) {
			if strings.Count(policy, tt.at) != 1 {
				t.Fatalf("the policy holds %q %d times", tt.at, strings.Count(policy, tt.at))
			}
			at := strings.Index(policy, tt.at) + len(tt.at) - tt.back
			v, offset := viewOf(t, "production.sigil", policy[:at]+marker+policy[at:])
			got := v.targetAt(offset)
			if tt.hover == nil {
				if got != nil && got.hover != "" {
					t.Errorf("hover = %q, want none", got.hover)
				}
				return
			}
			if got == nil {
				t.Fatal("no target")
			}
			for _, want := range tt.hover {
				if !strings.Contains(got.hover, want) {
					t.Errorf("hover = %q, want it to hold %q", got.hover, want)
				}
			}
			if def := where(v, got.defs); def != tt.def {
				t.Errorf("definition = %q, want %q", def, tt.def)
			}
		})
	}
}

// TestTargetOutsideDocuments checks the positions with nothing to show:
// a kind document, and a document whose kind isn't known.
func TestTargetOutsideDocuments(t *testing.T) {
	tests := []struct {
		name string
		file string
		src  string
	}{
		{name: "kind document", file: "deploy_approval.sigil", src: "kind DeployApproval<|> version 2\n"},
		{name: "unknown kind", file: "production.sigil", src: "policy p: Nope@1\n\nlet x<|> = 1\n"},
		{name: "before the header", file: "production.sigil", src: "// a comment<|>\npolicy p: DeployApproval@2\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, tt.file, tt.src)
			if got := v.targetAt(offset); got != nil {
				t.Errorf("target = %+v, want none", got)
			}
		})
	}
}

// where renders the first location as file:line:column, columns from 1,
// or "" for none.
func where(v *view, defs []location) string {
	if len(defs) == 0 {
		return ""
	}
	d := defs[0]
	p := newLines(v.proj.SourceOf(d.file), protocol.EncodingUTF8).position(d.from)
	return fmt.Sprintf("%s:%d:%d", strings.TrimPrefix(d.file, root+"/"), p.Line+1, p.Character+1)
}

// TestTargetTypes hovers over the types a param is declared with: a
// struct type, and an enum or struct type inside a map or a list.
func TestTargetTypes(t *testing.T) {
	src := "policy p: DeployApproval@2\n\nparam svc: Service\nparam m: map<Tier, int> = {}\nparam l: list<Ticket> = []\n"
	tests := []struct {
		at    string
		hover string
		def   string
	}{
		{at: "svc: Serv", hover: "type Service {\n  name: string", def: "deploy_approval.sigil:18:6"},
		{at: "map<Ti", hover: "enum Tier: critical | standard | internal", def: "deploy_approval.sigil:3:6"},
		{at: "Tier, in", hover: ""},
		{at: "list<Tick", hover: "type Ticket {", def: "deploy_approval.sigil:13:6"},
		{at: "param m", hover: "param m: map<Tier, int> = {}", def: "production.sigil:4:7"},
	}
	for _, tt := range tests {
		t.Run(tt.at, func(t *testing.T) {
			at := strings.Index(src, tt.at) + len(tt.at)
			v, offset := viewOf(t, "production.sigil", src[:at]+marker+src[at:])
			got := v.targetAt(offset)
			if tt.hover == "" {
				if got != nil {
					t.Errorf("target = %+v, want none", got)
				}
				return
			}
			if got == nil || !strings.Contains(got.hover, tt.hover) {
				t.Fatalf("target = %+v, want a hover holding %q", got, tt.hover)
			}
			if def := where(v, got.defs); def != tt.def {
				t.Errorf("definition = %q, want %q", def, tt.def)
			}
		})
	}
}

// TestLinkedKind hovers over and goes to names of a kind linked into the
// binary, which has no kind document: hover shows them, and definition
// finds nothing.
func TestLinkedKind(t *testing.T) {
	files := testWorkspace(t)
	model, errs := check.LoadKind("kind.sigil", files[root+"/deploy_approval.sigil"])
	if errs != nil {
		t.Fatal(errs)
	}
	src := []byte("policy p: DeployApproval@2\n\nwhen service.tier == critical {\n  deny(reason: not_eligible)\n}\n")
	p := workspace.NewLoader([]workspace.Linked{{Model: model, Binding: gokind.Synthesize(model)}}).Load([]workspace.File{{Name: root + "/p.sigil", Source: src}}, nil)
	p.Check()
	v := &view{proj: p, file: root + "/p.sigil", src: src}
	for _, at := range []string{"when serv", "service.ti", "deny", "== crit", "DeployAppr"} {
		got := v.targetAt(strings.Index(string(src), at) + len(at))
		if got == nil || got.hover == "" {
			t.Errorf("%s: no hover", at)
			continue
		}
		if got.defs != nil {
			t.Errorf("%s: definition = %v, want none", at, got.defs)
		}
	}
}
