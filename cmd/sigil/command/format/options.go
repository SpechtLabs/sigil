package format

// Option configures the fmt command.
type Option func(*options)

// options holds the dependencies of the fmt command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
