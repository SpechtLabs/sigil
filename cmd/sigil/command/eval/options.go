package eval

// Option configures the eval command.
type Option func(*options)

// options holds the dependencies of the eval command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
