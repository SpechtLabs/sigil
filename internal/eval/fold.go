package eval

import (
	"hash/maphash"
	"math"
	"reflect"
	"time"
)

// printSeed keys the payload fingerprints. They're only compared within
// one evaluation, so one seed per process is enough.
var printSeed = maphash.MakeSeed()

// fold drops every candidate equal to an earlier one in decision, reason
// and payload: they're one outcome, from several branches.
//
// Payloads are compared with [reflect.DeepEqual], and only when their
// fingerprints agree. When many rules produce distinct payloads for one
// decision and reason, a deep comparison between every pair of them
// costs more than evaluating the rules: about nine times as much at 64
// rules.
func fold(cands []*Candidate) []*Candidate {
	if len(cands) < 2 {
		return append([]*Candidate(nil), cands...)
	}
	out := make([]*Candidate, 0, len(cands))
	prints := make([]uint64, 0, len(cands)) // prints[i] is out[i]'s fingerprint
next:
	for _, c := range cands {
		p := fingerprint(c.Payload)
		for i, kept := range out {
			if prints[i] == p && kept.Decision == c.Decision && kept.Reason == c.Reason && reflect.DeepEqual(kept.Payload, c.Payload) {
				continue next
			}
		}
		out = append(out, c)
		prints = append(prints, p)
	}
	return out
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
