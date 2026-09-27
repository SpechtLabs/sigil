package kind

// Default is the kind's default decision: the constructor call that
// applies when no rule fires. Args holds the constant payload values it
// passes by field name.
type Default struct {
	Args     map[string]any //nolint:emptyinterface // constants are typed by their Sigil type; see Conforms
	Decision string
	Reason   string
}
