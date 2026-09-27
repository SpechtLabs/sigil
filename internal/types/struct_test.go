package types_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/types"
)

func TestStructFields(t *testing.T) {
	if f := service.Field("tier"); f == nil || f.Type != types.String {
		t.Errorf("Field(tier) = %v, want string field", f)
	}
	if f := service.Field("teir"); f != nil {
		t.Errorf("Field(teir) = %v, want nil", f)
	}
	if got, want := service.FieldNames(), "name, tier, owners, labels"; got != want {
		t.Errorf("FieldNames() = %q, want %q", got, want)
	}
	if got := actor.FieldNames(); got != "" {
		t.Errorf("FieldNames() of a struct without fields = %q, want empty", got)
	}
}
