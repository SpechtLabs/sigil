package engine

import (
	"bytes"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/format"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// stdinName is what the format op names a source without a path, as
// `sigil fmt -` names stdin.
const stdinName = "<stdin>"

// formatted is the format op's response.
type formatted struct {
	envelope
	Source    string `json:"source"`
	Formatted bool   `json:"formatted"` // the source was already in the canonical style
}

// format answers the format op: the source in sigil fmt's canonical
// style, or the syntax errors that keep it from being formatted.
func (e *Engine) format(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	if r.Source == nil {
		return nil, humane.New("format needs a source", `send {"op": "format", "source": "..."}, with an optional path for its diagnostics`)
	}
	name := r.Path
	if name == "" {
		name = stdinName
	}
	src := []byte(*r.Source)
	out, errs := format.Source(name, src)
	if errs != nil {
		return nil, &blocked{msg: name + " has syntax errors", advice: []string{"fix the syntax errors; fmt only formats files that parse"},
			diags: workspace.Diagnostics(errs),
		}
	}
	return formatted{envelope: env, Source: string(out), Formatted: bytes.Equal(src, out)}, nil
}
