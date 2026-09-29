package policy

import "github.com/spechtlabs/sigil/internal/eval"

// Policy is a compiled policy of the kind with input type In, from
// [Kind.Load] or [Kind.Compile]. It is immutable and safe to evaluate from
// any number of goroutines at once, so replacing one at run time is a
// pointer swap, for example through a [sync/atomic.Pointer].
type Policy[In any] struct {
	kind *Kind[In]
	prog *eval.Policy
	name string
}

// Name returns the name of the root policy, as its header declares it,
// for example "payments.production".
func (p *Policy[In]) Name() string { return p.name }
