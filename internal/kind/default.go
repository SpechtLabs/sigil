package kind

// Default is a decision constructor with constant payload values, the
// shape of the two outcomes a kind declares for the cases no rule
// decides: [Kind.Default], which applies when no rule fires, and
// [Kind.Conflict], which a `collect one` kind returns when resolution
// ends in a conflict. Args holds the constant payload values it passes by
// field name, in the representation [constant.Conforms] describes. Fields
// it leaves out take their declared defaults.
type Default struct {
	Args     map[string]any //nolint:emptyinterface // constants are typed by their Sigil type; see constant.Conforms
	Decision string         // the decision the default constructs
	Reason   string         // one of the decision's reasons
}
