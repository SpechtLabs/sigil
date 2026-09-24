package golang

// Option configures the gen go command.
type Option func(*options)

// options holds the dependencies of the gen go command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
