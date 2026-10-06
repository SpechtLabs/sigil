package eval

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/kind"
)

// TestFold checks that fold keeps the first of every group of candidates
// equal in decision, reason and payload, in order, whatever the payload
// holds. Each case lists its payloads as one field's values, all from
// allow(reason: member) unless the case says otherwise.
func TestFold(t *testing.T) {
	allow, deny := &kind.Decision{Name: "allow"}, &kind.Decision{Name: "deny"}
	member, oncall := &Rule{Decision: allow, Reason: "member"}, &Rule{Decision: allow, Reason: "oncall"}
	blocked := &Rule{Decision: deny, Reason: "member"}
	zone := func() *time.Location { return time.FixedZone("CEST", 2*60*60) } // a new *Location per call
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	one, otherOne := 1, 1

	tests := []struct {
		name   string
		rules  []*Rule // per value; allow(reason: member) for every value when nil
		values []any   //nolint:emptyinterface // payload field values, as Go values
		want   []int   // the indices of the values fold keeps
	}{
		{name: "no candidates", values: nil, want: nil},
		{name: "one candidate", values: []any{"a"}, want: []int{0}},
		{name: "distinct payloads keep their order", values: []any{"c", "a", "b"}, want: []int{0, 1, 2}},
		{name: "duplicates fold into the first", values: []any{"a", "b", "a", "c", "b"}, want: []int{0, 1, 3}},
		{name: "same payload, other reason", rules: []*Rule{member, oncall}, values: []any{"a", "a"}, want: []int{0, 1}},
		{name: "same payload and reason, other decision", rules: []*Rule{member, blocked}, values: []any{"a", "a"}, want: []int{0, 1}},
		{name: "bools", values: []any{true, false, true}, want: []int{0, 1}},
		{name: "ints", values: []any{int64(1), int64(2), int64(1), 1}, want: []int{0, 1, 3}},
		{name: "uints", values: []any{uint(1), uint(2), uint(1)}, want: []int{0, 1}},
		{name: "durations", values: []any{time.Minute, time.Hour, time.Minute}, want: []int{0, 1}},
		{name: "negative zero equals zero", values: []any{0.0, math.Copysign(0, -1), 1.5}, want: []int{0, 2}},
		{name: "NaN never equals itself", values: []any{math.NaN(), math.NaN()}, want: []int{0, 1}},
		{name: "times with deep-equal zones fold", values: []any{at.In(zone()), at.In(zone())}, want: []int{0}},
		{name: "one instant in two zones doesn't fold", values: []any{at, at.In(zone())}, want: []int{0, 1}},
		{name: "pointers compare what they point to", values: []any{&one, &otherOne, (*int)(nil), (*int)(nil)}, want: []int{0, 2}},
		{name: "absent values", values: []any{nil, nil, "a"}, want: []int{0, 2}},
		{name: "lists", values: []any{[]string{"a", "b"}, []string{"b", "a"}, []string{"a", "b"}}, want: []int{0, 1}},
		{name: "nil and empty lists differ", values: []any{[]string(nil), []string{}}, want: []int{0, 1}},
		{name: "lists of any", values: []any{[]any{"a", nil, int64(1)}, []any{"a", nil, int64(1)}, []any{"a", nil}}, want: []int{0, 2}},
		{name: "arrays", values: []any{[2]int{1, 2}, [2]int{2, 1}, [2]int{1, 2}}, want: []int{0, 1}},
		{name: "maps in any order", values: []any{
			map[string]int{"a": 1, "b": 2}, map[string]int{"b": 2, "a": 1}, map[string]int{"a": 2, "b": 1},
		}, want: []int{0, 2}},
		{name: "structs hash alike and still compare", values: []any{struct{ A int }{1}, struct{ A int }{2}, struct{ A int }{1}}, want: []int{0, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cands []*Candidate
			for i, v := range tt.values {
				r := member
				if tt.rules != nil {
					r = tt.rules[i]
				}
				cands = append(cands, &Candidate{Rule: r, Payload: map[string]any{"v": v}})
			}
			var want []*Candidate
			for _, i := range tt.want {
				want = append(want, cands[i])
			}
			for name, f := range folds(len(cands)) {
				got := f(cands)
				if !slices.Equal(got, want) {
					t.Errorf("%s kept %v, want %v", name, indices(cands, got), tt.want)
				}
				if len(got) > 0 && &got[0] == &cands[0] {
					t.Errorf("%s returned its argument; the outcome must not share the candidates' array", name)
				}
			}
		})
	}
}

// folds returns fold and, for two candidates or more, the two ways it
// folds, so a test covers both whatever the number of candidates.
func folds(n int) map[string]func([]*Candidate) []*Candidate {
	if n < 2 {
		return map[string]func([]*Candidate) []*Candidate{"fold": fold}
	}
	return map[string]func([]*Candidate) []*Candidate{"fold": fold, "foldPairs": foldPairs, "foldSorted": foldSorted}
}

// TestFoldMany checks fold against the comparison of every candidate
// with every one kept before it, on more candidates than fold compares
// pairwise: distinct payloads, repeats far apart, payloads that share a
// fingerprint without being equal, and two reasons.
func TestFoldMany(t *testing.T) {
	allow := &kind.Decision{Name: "allow"}
	member, oncall := &Rule{Decision: allow, Reason: "member"}, &Rule{Decision: allow, Reason: "oncall"}
	var cands []*Candidate
	for i := range 4 * foldPairsMax {
		r := member
		if i%7 == 0 {
			r = oncall
		}
		var v any //nolint:emptyinterface // a payload field value, as a Go value
		switch {
		case i%5 == 0:
			v = struct{ A int }{i % 3} // every struct has the same fingerprint
		case i%3 == 0:
			v = time.Duration(i%40) * time.Minute
		default:
			v = time.Duration(i) * time.Second
		}
		cands = append(cands, &Candidate{Rule: r, Payload: map[string]any{"v": v}})
	}

	var want []*Candidate
next:
	for _, c := range cands {
		for _, k := range want {
			if same(k, c) {
				continue next
			}
		}
		want = append(want, c)
	}
	for name, f := range folds(len(cands)) {
		if got := f(cands); !slices.Equal(got, want) {
			t.Errorf("%s kept %v, want %v", name, indices(cands, got), indices(cands, want))
		}
	}
}

// TestFingerprint checks the property fold relies on: payloads
// reflect.DeepEqual finds equal have equal fingerprints. The pool pairs
// values that are deeply equal without being identical.
func TestFingerprint(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	a, b := int64(7), int64(7)
	pool := []map[string]any{
		{}, {"v": nil},
		{"v": 0.0}, {"v": math.Copysign(0, -1)},
		{"v": at.In(time.FixedZone("CEST", 7200))}, {"v": at.In(time.FixedZone("CEST", 7200))},
		{"v": &a}, {"v": &b},
		{"v": map[string][]int{"x": {1}, "y": {2}}}, {"v": map[string][]int{"y": {2}, "x": {1}}},
		{"v": []any{"s", true, uint8(3)}}, {"v": []any{"s", true, uint8(3)}},
		{"a": "x", "b": "y"}, {"b": "y", "a": "x"},
		{"v": struct{ A []int }{[]int{1}}}, {"v": struct{ A []int }{[]int{1}}},
		{"v": complex(1, 2)}, {"v": complex(1, 2)},
	}
	for i, x := range pool {
		for _, y := range pool[i+1:] {
			if reflect.DeepEqual(x, y) && fingerprint(x) != fingerprint(y) {
				t.Errorf("%v and %v are deeply equal, but their fingerprints differ", x, y)
			}
		}
	}

	// Distinct payloads should mostly hash apart, or the filter does
	// nothing: the benchmark's 128 durations all do.
	seen := map[uint64]bool{}
	for i := range 128 {
		seen[fingerprint(map[string]any{"ttl": time.Duration(i+1) * time.Minute})] = true
	}
	if len(seen) != 128 {
		t.Errorf("128 distinct payloads have %d fingerprints", len(seen))
	}
}

// indices returns the positions in cands of the candidates in got.
func indices(cands, got []*Candidate) []int {
	out := make([]int, 0, len(got))
	for _, c := range got {
		out = append(out, slices.Index(cands, c))
	}
	return out
}
