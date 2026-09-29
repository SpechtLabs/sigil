package status

import "github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/client"

// Option configures this command.
type Option func(*options)

type options struct {
	client *client.Client
	json   *bool
}

func defaultOptions() *options {
	jsonOutput := false
	return &options{client: client.New(), json: &jsonOutput}
}

// WithClient sets the API client. Its settings are read after flag parsing,
// so the root's --url and --timeout flags reach it. A nil api is ignored.
func WithClient(api *client.Client) Option {
	return func(o *options) {
		if api != nil {
			o.client = api
		}
	}
}

// WithJSON shares the root's --json flag, read after Cobra parses it. A nil
// value is ignored.
func WithJSON(value *bool) Option {
	return func(o *options) {
		if value != nil {
			o.json = value
		}
	}
}
