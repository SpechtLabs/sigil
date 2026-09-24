package check

// Option configures the check command.
type Option func(*options)

// options holds the dependencies of the check command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
