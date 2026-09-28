package testsuite_test

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

func FuzzTestSuite(f *testing.F) {
	k, errs := check.LoadKind("kind.sigil", []byte(accessKind))
	if errs != nil {
		f.Fatal(errs)
	}
	for _, src := range []string{"", "policy: main\ncases: []", "policy: main\ncases: [null]", "policy: main\ncases:\n  - name: basic\n    input: {}\n    expect: {decision: deny, reason: no_rule_matched}", "policy: main\ncases:\n  - name: file\n    input_file: input.json\n    expect: {asserts: [named]}"} {
		f.Add([]byte(src), []byte(`{}`))
	}
	f.Fuzz(func(t *testing.T, src, input []byte) {
		s, err := testsuite.Parse("fuzz_test.yaml", src)
		if err != nil {
			return
		}
		first := s.Validate(k)
		if !reflect.DeepEqual(first, s.Validate(k)) {
			t.Fatal("suite validation is not deterministic")
		}
		if first != nil {
			return
		}
		fsys := fstest.MapFS{"input.json": &fstest.MapFile{Data: input}, "input.yaml": &fstest.MapFile{Data: input}}
		for _, c := range s.Cases {
			_, _ = s.ReadInput(fsys, c)
		}
	})
}
