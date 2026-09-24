package breaking

// Option configures the breaking command.
type Option func(*options)

// options holds the dependencies of the breaking command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
