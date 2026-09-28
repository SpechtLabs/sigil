package types_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/types"
)

func TestCandidate(t *testing.T) {
	review := types.NewCandidate("review", []*types.Field{{Name: "approvers", Type: &types.List{Elem: types.String}}})

	if got, want := review.String(), "review candidate"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := review.FieldNames(), "approvers, reason"; got != want {
		t.Errorf("FieldNames() = %q, want %q", got, want)
	}
	if f := review.Field("reason"); f == nil || f.Type != types.Decision {
		t.Errorf("Field(reason) = %v, want a decision field", f)
	}
	if f := review.Field("approvers"); f == nil || f.Type.String() != "list<string>" {
		t.Errorf("Field(approvers) = %v, want list<string>", f)
	}
	if f := review.Field("ticket"); f != nil {
		t.Errorf("Field(ticket) = %v, want nil", f)
	}

	tests := []struct {
		a, b types.Type
		want bool
	}{
		{review, types.NewCandidate("review", nil), true}, // by decision, like a struct by name
		{review, types.NewCandidate("approve", nil), false},
		{review, &types.Struct{Name: "review"}, false},
		{&types.List{Elem: review}, &types.List{Elem: types.NewCandidate("review", nil)}, true},
	}
	for _, tt := range tests {
		if got := types.Identical(tt.a, tt.b); got != tt.want {
			t.Errorf("Identical(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIsCandidates(t *testing.T) {
	review := types.NewCandidate("review", nil)
	tests := []struct {
		t    types.Type
		want bool
	}{
		{review, true},
		{&types.List{Elem: review}, true},
		{&types.List{Elem: &types.List{Elem: review}}, true},
		{&types.Map{Key: types.String, Value: review}, true},
		{&types.Optional{Elem: review}, true},
		{types.Decision, false},
		{&types.List{Elem: types.Decision}, false},
		{&types.Struct{Name: "Actor"}, false},
	}
	for _, tt := range tests {
		if got := types.IsCandidates(tt.t); got != tt.want {
			t.Errorf("IsCandidates(%s) = %v, want %v", tt.t, got, tt.want)
		}
	}
	if types.IsComparable(review) || types.IsEquatable(review) || types.IsKey(review) {
		t.Error("a candidate has no equality")
	}
}
