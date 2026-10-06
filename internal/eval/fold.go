package eval

import (
	stdcmp "cmp"
	"hash/maphash"
	"math"
	"reflect"
	"slices"
	"time"
)

// foldPairsMax is the most candidates fold compares pairwise. Past it,
// sorting their fingerprints costs less: the two cost the same at 128.
const foldPairsMax = 128

// printSeed keys the payload fingerprints. They're only compared within
// one evaluation, so one seed per process is enough.
var printSeed = maphash.MakeSeed()

// printAt is a candidate's payload fingerprint and its position among the
// candidates fold reads.
type printAt struct {
	hash uint64
	at   int
}

// fold drops every candidate equal to an earlier one in decision, reason
// and payload: they're one outcome, from several branches.
//
// Payloads are compared with [reflect.DeepEqual], and only when their
// fingerprints agree. When many rules produce distinct payloads for one
// decision and reason, a deep comparison between every pair of them
// costs more than evaluating the rules: about nine times as much at 64
// rules. Comparing every pair's fingerprints instead still grows with
// the square of the candidates, a third of the evaluation at 512, so
// past [foldPairsMax] candidates fold sorts the fingerprints and
// compares only candidates that share one.
func fold(cands []*Candidate) []*Candidate {
	switch {
	case len(cands) < 2:
		return append([]*Candidate(nil), cands...)
	case len(cands) <= foldPairsMax:
		return foldPairs(cands)
	}
	return foldSorted(cands)
}

// foldPairs is fold comparing each candidate's fingerprint with every
// kept one's.
func foldPairs(cands []*Candidate) []*Candidate {
	out := make([]*Candidate, 0, len(cands))
	prints := make([]uint64, 0, len(cands)) // prints[i] is out[i]'s fingerprint
next:
	for _, c := range cands {
		p := fingerprint(c.Payload)
		for i, kept := range out {
			if prints[i] == p && same(kept, c) {
				continue next
			}
		}
		out = append(out, c)
		prints = append(prints, p)
	}
	return out
}

// foldSorted is fold sorting the fingerprints, so it compares only the
// candidates that share one.
func foldSorted(cands []*Candidate) []*Candidate {
	prints := make([]printAt, len(cands))
	for i, c := range cands {
		prints[i] = printAt{hash: fingerprint(c.Payload), at: i}
	}
	slices.SortFunc(prints, func(a, b printAt) int {
		return stdcmp.Or(stdcmp.Compare(a.hash, b.hash), stdcmp.Compare(a.at, b.at))
	})

	// kept reuses prints' array: keepDistinct never writes past the
	// entry it's reading.
	kept := prints[:0]
	for start := 0; start < len(prints); {
		end := start + 1
		for end < len(prints) && prints[end].hash == prints[start].hash {
			end++
		}
		kept = keepDistinct(kept, prints[start:end], cands)
		start = end
	}

	slices.SortFunc(kept, func(a, b printAt) int { return stdcmp.Compare(a.at, b.at) })
	out := make([]*Candidate, len(kept))
	for i, k := range kept {
		out[i] = cands[k.at]
	}
	return out
}

// keepDistinct appends to kept each candidate of run, a run of equal
// fingerprints in order of position, that equals none kept before it
// from the same run. Equal candidates have equal fingerprints, so no
// other run can hold one.
func keepDistinct(kept, run []printAt, cands []*Candidate) []printAt {
	first := len(kept)
	for _, p := range run {
		if !keptSame(kept[first:], cands[p.at], cands) {
			kept = append(kept, p)
		}
	}
	return kept
}

// keptSame reports whether c is the same outcome as a kept candidate.
func keptSame(kept []printAt, c *Candidate, cands []*Candidate) bool {
	for _, k := range kept {
		if same(cands[k.at], c) {
			return true
		}
	}
	return false
}

// same reports whether two candidates are one outcome: the same
// decision, reason and payload.
func same(a, b *Candidate) bool {
	return a.Decision == b.Decision && a.Reason == b.Reason && reflect.DeepEqual(a.Payload, b.Payload)
}

// fingerprint hashes a payload so that payloads reflect.DeepEqual finds
// equal hash alike. Payloads that hash alike may still differ: fold
// compares them before it drops one, which lets hashValue skip what
// it can't read cheaply.
func fingerprint(payload map[string]any) uint64 { //nolint:emptyinterface // the untyped payload, as Go values
	var h uint64
	// Map order varies, so the fields combine by a sum, which doesn't
	// depend on it.
	for name, v := range payload {
		h += mix(maphash.String(printSeed, name) ^ mix(hashValue(reflect.ValueOf(v))))
	}
	return h
}

// hashValue hashes a payload value the way fingerprint needs: values
// reflect.DeepEqual finds equal hash alike. That's why a negative zero
// hashes like zero and a pointer hashes by what it points to. A struct
// other than time.Time, which payloads don't hold, hashes as zero.
func hashValue(v Value) uint64 {
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return mix(1)
		}
		return mix(2)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return mix(uint64(v.Int())) //nolint:gosec // reinterpreting the bits is what a hash wants
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return mix(v.Uint())
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if f == 0 {
			f = 0 // -0 == 0, and DeepEqual compares floats with ==
		}
		return mix(math.Float64bits(f))
	case reflect.String:
		return maphash.String(printSeed, v.String())
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return 0
		}
		return mix(hashValue(v.Elem()))
	case reflect.Slice, reflect.Array:
		var h uint64
		for i := range v.Len() {
			h = mix(h ^ hashValue(v.Index(i)))
		}
		return h
	case reflect.Map:
		var h uint64
		for iter := v.MapRange(); iter.Next(); {
			h += mix(hashValue(iter.Key()) ^ mix(hashValue(iter.Value())))
		}
		return h
	case reflect.Struct:
		if v.Type() == timeType {
			// Times DeepEqual finds equal are the same instant.
			t, _ := reflect.TypeAssert[time.Time](v)
			return mix(uint64(t.Unix()) ^ mix(uint64(t.Nanosecond()))) //nolint:gosec // as above
		}
	}
	return 0
}

// mix scrambles x's bits, the finalizer of splitmix64, so that
// fingerprints combining similar values by xor and sum still differ.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}
