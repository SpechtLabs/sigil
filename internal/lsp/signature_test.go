package lsp

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// TestSignature checks the signature of the call at the marker: its
// label, the parameter the cursor is on, and that one's documentation.
func TestSignature(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		label  string // "" for no signature
		active string // the active parameter's span of the label; "" for none
		doc    string // a substring of the active parameter's documentation
	}{
		{name: "a host function", src: "when split(<|>", label: "split(string, string) -> list<string>", active: "string"},
		{name: "a host function's second argument", src: "when split(environment, <|>", label: "split(string, string) -> list<string>", active: "string"},
		{name: "past a host function's last argument", src: `when split(environment, ",", <|>`, label: "split(string, string) -> list<string>"},
		{name: "a nested call", src: `when split(split(environment, ",")[0], <|>`, label: "split(string, string) -> list<string>", active: "string"},
		{name: "a list inside a call", src: `when split(environment, [<|>`, label: "split(string, string) -> list<string>", active: "string"},
		{name: "a constructor", src: "when cleared {\n  review(<|>", label: "review(reason: service_owner, approvers: list<string>, tier: Tier = standard)", active: "reason: service_owner", doc: "one of the decision's"},
		{name: "a constructor's reason", src: "when cleared {\n  deny(reason: <|>", label: "deny(reason: not_eligible | soak_too_short | no_rule_matched)", active: "reason: not_eligible | soak_too_short | no_rule_matched"},
		{name: "a constructor's next field", src: "when cleared {\n  review(reason: service_owner, <|>", label: "review(reason: service_owner, approvers: list<string>, tier: Tier = standard)", active: "approvers: list<string>", doc: "Required."},
		{name: "a constructor's field being typed", src: "when cleared {\n  review(reason: service_owner, ti<|>", label: "review(reason: service_owner, approvers: list<string>, tier: Tier = standard)", active: "tier: Tier = standard"},
		{name: "a constructor's field value", src: "when cleared {\n  review(reason: service_owner, tier: <|>", label: "review(reason: service_owner, approvers: list<string>, tier: Tier = standard)", active: "tier: Tier = standard"},
		{name: "every field given", src: "when cleared {\n  approve(reason: open, bake: 1h, <|>", label: "approve(reason: release_manager | payments_sre, bake: duration = 1h)"},
		{name: "an invocation", src: "guardrails(<|>", label: "guardrails(min_soak: duration = 24h)", active: "min_soak: duration = 24h", doc: "At least `1h`. At most `48h`."},
		{name: "an invocation's value", src: "guardrails(min_soak: 4<|>", label: "guardrails(min_soak: duration = 24h)", active: "min_soak: duration = 24h"},
		{name: "an assert", src: `assert("x", <|>`},
		{name: "parentheses", src: "when (<|>"},
		{name: "outside a call", src: "when split(environment, \",\") any in <|>"},
		{name: "a method-like call", src: "when service.name(<|>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, "production.sigil", head+tt.src)
			sig := v.signatureAt(offset)
			if tt.label == "" {
				if sig != nil {
					t.Fatalf("signature = %q, want none", sig.label)
				}
				return
			}
			if sig == nil {
				t.Fatalf("no signature, want %q", tt.label)
			}
			if sig.label != tt.label {
				t.Errorf("label = %q, want %q", sig.label, tt.label)
			}
			active := ""
			doc := ""
			if sig.active >= 0 && sig.active < len(sig.params) {
				p := sig.params[sig.active]
				active, doc = sig.label[p.from:p.to], p.doc
			}
			if active != tt.active {
				t.Errorf("active parameter = %q, want %q", active, tt.active)
			}
			if !strings.Contains(doc, tt.doc) {
				t.Errorf("documentation = %q, want it to hold %q", doc, tt.doc)
			}
		})
	}
}

// TestSignatureUnits checks that the protocol's parameter spans count
// in the encoding agreed on: UTF-16 code units, or bytes for UTF-8.
func TestSignatureUnits(t *testing.T) {
	sig := &signature{label: "f(ä: 𝄞, b)", params: []parameter{{from: 2, to: 10}, {from: 12, to: 13}}, active: -1}
	help := sig.protocol(protocol.EncodingUTF16)
	got := help.Signatures[0].Parameters
	if got[0].Label != [2]uint32{2, 7} || got[1].Label != [2]uint32{9, 10} {
		t.Errorf("spans = %v, want [2 7] and [9 10]", got)
	}
	if bytes := sig.protocol(protocol.EncodingUTF8).Signatures[0].Parameters; bytes[0].Label != [2]uint32{2, 10} || bytes[1].Label != [2]uint32{12, 13} {
		t.Errorf("spans in UTF-8 = %v, want [2 10] and [12 13]", bytes)
	}
	if *help.ActiveParameter != 2 {
		t.Errorf("active parameter = %d, want 2, past the last, for none", *help.ActiveParameter)
	}
}
