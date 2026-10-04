package stub_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/stub"
)

// FuzzStubs binds any stubs document to the deploy kind: Validate and
// Bind report the same problems, Bind leaves the binding it was given as
// it was, and a call with the args of one of a stub's call entries
// matches an entry rather than failing as unmatched.
func FuzzStubs(f *testing.F) {
	k, errs := check.LoadKind("kind.sigil", []byte(deployKind))
	if errs != nil {
		f.Fatal(errs)
	}
	b := gokind.Synthesize(k)
	for _, src := range []string{"", "~", "{}", "[owner]",
		"remove_requestor:\n  calls:\n    - args: [{name: kevin, roles: [user]}, [cedric, alice, bob]]\n      returns: [cedric, alice, bob]\n    - args: [{name: bob}, [cedric, bob]]\n      error: bob can't approve\n  returns: []\nlookup_owner:\n  error: directory unavailable\ndouble:\n  calls:\n    - {args: [2], returns: 4}\nnow:\n  returns: 2026-09-30T12:00:00Z\n",
		"lookup_owner:\n  calls:\n    - {args: [\"1.10\"], returns: a}\n    - {args: [1.10], returns: b}\n    - {args: [2026-01-01], returns: c}\n    - {args: [!!str 5], returns: d}\n",
		"double: {calls: [{args: [0x10], returns: 32}, {args: [-1], error: negative}, {args: [1.0], returns: 2}]}\n",
		"remove_requestor: {calls: [{args: [&u {name: a, roles: ~}, []], returns: ~}, {args: [*u, [a]], returns: [a]}, {args: [{}, ~], returns: []}]}\n",
		`{"double": {"calls": [{"args": [9223372036854775807], "returns": -9223372036854775808}]}, "now": {"returns": "2026-09-30T12:00:00+02:00"}}`,
	} {
		f.Add([]byte(src))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		set, errs := stub.ParseDocument(src)
		if errs != nil {
			return
		}
		bound, errs := set.Bind(k, b)
		if !reflect.DeepEqual(errs, set.Validate(k)) {
			t.Fatalf("Validate and Bind disagree: %v", stub.Errors(errs))
		}
		err, _ := reflect.TypeAssert[error](b.Funcs["double"].Call([]reflect.Value{reflect.ValueOf(int64(1))})[1])
		if _, unbound := errors.AsType[*gokind.ErrUnbound](err); !unbound {
			t.Fatal("Bind changed the binding it was given")
		}
		if errs != nil {
			return
		}
		for _, name := range slices.Sorted(maps.Keys(set)) {
			fn, impl := k.Func(name), bound.Funcs[name]
		calls:
			for i, c := range set[name].Calls {
				// The decoded args are those of the entry unless the stub
				// reads one by its type, as a string written as a number;
				// such an entry isn't one these args can name.
				args := make([]reflect.Value, len(c.Args))
				for j, a := range c.Args {
					args[j] = reflect.New(impl.Type().In(j)).Elem()
					if b.Decode(fn.Params[j], a.Raw, args[j], "arg") != nil {
						continue calls
					}
				}
				err, _ := reflect.TypeAssert[error](impl.Call(args)[1])
				if _, unmatched := errors.AsType[*stub.ErrUnmatched](err); unmatched {
					t.Fatalf("%s: call %d doesn't match its own args: %v", name, i+1, err)
				}
			}
		}
	})
}
