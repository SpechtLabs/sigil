package kind_test

import (
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

var (
	strList = &types.List{Elem: types.String}
	strMap  = &types.Map{Key: types.String, Value: types.String}
)

func TestLookups(t *testing.T) {
	k := deploy()
	if k.Type("Service") == nil || k.Type("Ticket") != nil {
		t.Error("Type lookup")
	}
	if k.Input("actor") == nil || k.Input("split") != nil {
		t.Error("Input lookup")
	}
	if k.Func("split") == nil || k.Func("actor") != nil {
		t.Error("Func lookup")
	}
	if k.Decision("approve") == nil || k.Decision("escalate") != nil {
		t.Error("Decision lookup")
	}
	if d := k.Decision("approve"); d.Field("bake") == nil || d.Field("bak") != nil {
		t.Error("Field lookup")
	}
}

// deploy builds the DeployApproval kind from the README, fresh for each
// test so a table entry can mutate it.
func deploy() *kind.Kind {
	release := &types.Struct{Name: "Release", Fields: []*types.Field{
		{Name: "soak", Type: types.Duration},
		{Name: "hotfix", Type: types.Bool},
	}}
	service := &types.Struct{Name: "Service", Fields: []*types.Field{
		{Name: "name", Type: types.String},
		{Name: "tier", Type: types.String},
		{Name: "owners", Type: strList},
		{Name: "labels", Type: strMap},
	}}
	actor := &types.Struct{Name: "Actor", Fields: []*types.Field{
		{Name: "name", Type: types.String},
		{Name: "teams", Type: strList},
		{Name: "roles", Type: strList},
		{Name: "regions", Type: strList},
	}}
	return &kind.Kind{
		Name:    "DeployApproval",
		Version: 1,
		Types:   []*types.Struct{release, service, actor},
		Inputs: []*kind.Input{
			{Name: "release", Type: release},
			{Name: "service", Type: service},
			{Name: "actor", Type: actor},
			{Name: "environment", Type: types.String},
		},
		Funcs: []*kind.Func{
			{Name: "split", Params: []*kind.Param{{Name: "s", Type: types.String}, {Name: "sep", Type: types.String}}, Result: strList},
		},
		Decisions: []*kind.Decision{
			{Name: "deny"},
			{Name: "review", Fields: []*kind.Field{{Name: "approvers", Type: strList}}},
			{Name: "approve", Fields: []*kind.Field{{Name: "bake", Type: types.Duration, Default: time.Hour, HasDefault: true}}},
		},
		Precedence: []string{"deny", "review", "approve"},
		Default:    &kind.Default{Decision: "deny", Reason: "no_rule_matched"},
	}
}

// access builds the collecting AccessGrant kind from the kind-files page.
func access() *kind.Kind {
	actor := &types.Struct{Name: "Actor", Fields: []*types.Field{
		{Name: "name", Type: types.String},
		{Name: "groups", Type: strList},
		{Name: "clearance", Type: types.String},
	}}
	return &kind.Kind{
		Name:    "AccessGrant",
		Version: 1,
		Types:   []*types.Struct{actor},
		Inputs:  []*kind.Input{{Name: "actor", Type: actor}},
		Decisions: []*kind.Decision{
			{Name: "read"},
			{Name: "write"},
			{Name: "admin", Fields: []*kind.Field{{Name: "ttl", Type: types.Duration, Default: 8 * time.Hour, HasDefault: true}}},
			{Name: "customer_data_writer"},
			{Name: "development_environment_writer"},
		},
		Collect: true,
	}
}
