package gen

// Option configures the gen command.
type Option func(*options)

// options holds the dependencies of the gen command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
