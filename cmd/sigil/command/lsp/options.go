package lsp

// Option configures the lsp command.
type Option func(*options)

// options holds the dependencies of the lsp command. It is empty until the
// command is implemented; add a With* option for every dependency.
type options struct{}
