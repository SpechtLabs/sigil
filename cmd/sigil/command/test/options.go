package test

// Option configures the test command.
type Option func(*options)

// options holds the dependencies of the test command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
