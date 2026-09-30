package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/spechtlabs/sigil/internal/lint"
)

var update = flag.Bool("update", false, "rewrite the configuration file's JSON Schema the docs site publishes")

// published is the copy of the schema the docs site serves at SchemaURL.
var published = filepath.Join("..", "..", "..", "..", "docs", ".vuepress", "public", "schema", "config.json")

// TestSchemaPublished fails when the schema the docs site serves isn't
// the one Schema builds; -update rewrites it.
func TestSchemaPublished(t *testing.T) {
	got, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(published, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, rerr := os.ReadFile(published)
	if rerr != nil {
		t.Fatalf("%v (run with -update to create it)", rerr)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale; run go test ./cmd/sigil/internal/config -run TestSchemaPublished -update", published)
	}
}

// TestSchemaKeys checks that the schema describes the keys the parser
// accepts, no more and no fewer, the lints and their levels, and that
// every property has a description.
func TestSchemaKeys(t *testing.T) {
	src, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(src, &s); err != nil {
		t.Fatal(err)
	}
	if s["$id"] != SchemaURL || s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$id = %v, $schema = %v", s["$id"], s["$schema"])
	}
	top := s["properties"].(map[string]any)
	item := top["require"].(map[string]any)["items"].(map[string]any)
	lints := top["lints"].(map[string]any)["properties"].(map[string]any)
	tests := []struct {
		name string
		obj  map[string]any
		want []string
	}{
		{name: "top level", obj: top, want: append(slices.Clone(topKeys), schemaKey)},
		{name: "require entry", obj: item["properties"].(map[string]any), want: requireKeys},
		{name: "lints", obj: lints, want: lint.Names()},
	}
	for _, tt := range tests {
		if got, want := keys(tt.obj), sorted(tt.want); !slices.Equal(got, want) {
			t.Errorf("%s: schema properties %v, parser keys %v", tt.name, got, want)
		}
	}
	if req := item["required"].([]any); len(req) != 1 || req[0] != "policy" {
		t.Errorf("require entry required = %v, want [policy]", req)
	}
	for name, l := range lints {
		if enum := l.(map[string]any)["enum"].([]any); len(enum) != 3 || enum[0] != "off" || enum[1] != "warn" || enum[2] != "error" {
			t.Errorf("lint %s enum = %v", name, enum)
		}
		for _, lv := range []string{"off", "warn", "error"} {
			if _, ok := lint.ParseLevel(lv); !ok {
				t.Errorf("the parser rejects the schema's level %q", lv)
			}
		}
	}
	for _, obj := range []map[string]any{s, item, top["lints"].(map[string]any)} {
		if obj["additionalProperties"] != false {
			t.Errorf("additionalProperties = %v, want false", obj["additionalProperties"])
		}
	}
	described(t, "", s)
}

// TestExampleConfiguration parses the example repository's
// configuration, whose keys the schema describes.
func TestExampleConfiguration(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "examples", "deploy-gates", "policies", "sigil.yaml")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(path, src); err != nil {
		t.Fatalf("Parse(%s) = %v", path, err)
	}
}

// described checks that every property below s has a description.
func described(t *testing.T, at string, s map[string]any) {
	t.Helper()
	props, _ := s["properties"].(map[string]any)
	for name, p := range props {
		p := p.(map[string]any)
		if d, _ := p["description"].(string); d == "" {
			t.Errorf("%s/%s has no description", at, name)
		}
		described(t, at+"/"+name, p)
		if items, ok := p["items"].(map[string]any); ok {
			described(t, at+"/"+name+"[]", items)
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	sort.Strings(out)
	return out
}
