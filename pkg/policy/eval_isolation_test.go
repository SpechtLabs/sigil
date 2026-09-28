package policy_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// Shared imports, separate invocations, skipped branches and failed asserts
// must never carry a frame's memoized values into another evaluation.
func TestEvalFrameIsolation(t *testing.T) {
	const source = `module shared: Frames@1
pub let allowed = release.hotfix
---
module unused: Frames@1
pub let bad = actor.roles[999]
---
policy child: Frames@1
use shared.{allowed}
use unused
param delay: duration
when allowed { approve(a, bake: delay) }
---
policy root: Frames@1
use child
use unused
assert("named", actor.name != "")
child(delay: 1h)
when release.soak > 1h { child(delay: 2h) }
assert("granted", not release.hotfix or approve in outcome)
`
	k := policy.NewKind[Input]("Frames", policy.WithVersion(1), policy.WithCollect(Approve))
	p, err := k.Compile(source, "root")
	if err != nil {
		t.Fatal(err)
	}
	inputs := []Input{
		{Actor: Actor{Name: "ada"}, Release: Release{Hotfix: true, Soak: 2 * time.Hour}},
		{Actor: Actor{Name: "ada"}, Release: Release{Hotfix: true}},
		{Actor: Actor{Name: "ada"}},
		{},
	}
	wants := [][]time.Duration{{time.Hour, 2 * time.Hour}, {time.Hour}, nil, nil}
	retained := make([]*policy.Result, len(inputs))
	for i, in := range inputs {
		res, evalErr := p.Eval(context.Background(), in)
		var assertion *policy.AssertionError
		if (i == 3) != errors.As(evalErr, &assertion) || i != 3 && evalErr != nil {
			t.Fatalf("input %d: unexpected error %v", i, evalErr)
		}
		var got []time.Duration
		for _, grant := range Approve.MatchAll(res) {
			got = append(got, grant.Payload.Bake)
		}
		if !reflect.DeepEqual(got, wants[i]) {
			t.Fatalf("input %d: bake durations %v, want %v", i, got, wants[i])
		}
		retained[i] = res
	}

	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			for n := range 32 {
				i := (worker + n) % len(inputs)
				res, _ := p.Eval(context.Background(), inputs[i])
				if !reflect.DeepEqual(res, retained[i]) {
					t.Errorf("input %d changed across evaluations", i)
					return
				}
			}
		})
	}
	wg.Wait()
	for i, in := range inputs {
		res, _ := p.Eval(context.Background(), in)
		if !reflect.DeepEqual(res, retained[i]) {
			t.Errorf("retained result %d changed", i)
		}
	}
}
