package policy

// Params binds the root policy's params from Go, by name. Every value
// is type-checked against the param's declaration when the policy is
// compiled, like an invocation's arguments would be. A value is a Go
// value of the shape NewKind accepts for the param's type: a string for
// `string`, a []string for `list<string>`, a time.Duration for
// `duration`, and so on.
//
//	Deploy.Compile(src, "deploy.gate", policy.Params{
//		"approvers": []string{"payments-leads"},
//		"min_soak":  4 * time.Hour,
//	})
type Params map[string]any //nolint:emptyinterface // values are the host's Go values, checked against the param's type

// LoadOption configures Compile and Load.
type LoadOption interface {
	apply(*loadOptions)
}

type loadOptions struct {
	params Params
}

func (p Params) apply(o *loadOptions) {
	for name, v := range p {
		o.params[name] = v
	}
}
