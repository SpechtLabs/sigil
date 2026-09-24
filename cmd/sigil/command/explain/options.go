package explain

// Option configures the explain command.
type Option func(*options)

// options holds the dependencies of the explain command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
