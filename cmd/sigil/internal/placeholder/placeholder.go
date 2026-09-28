// Package placeholder backs the commands that are part of the documented CLI
// surface but not implemented yet. Delete it once the last command is real.
package placeholder

import (
	"fmt"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// kindNotImplemented is the error kind a planned command reports.
const kindNotImplemented = "not_implemented"

// Result is what a planned command prints in JSON and YAML: the error
// alone, in the shape `sigil eval` reports a failed evaluation's.
type Result struct {
	Error Failure `json:"error" yaml:"error"`
}

// Failure says why the command did nothing.
type Failure struct {
	Kind    string `json:"kind" yaml:"kind"`
	Message string `json:"message" yaml:"message"`
	Help    string `json:"help" yaml:"help"` // what to do about it
}

// NotImplemented reports that cmd isn't implemented yet: as the error in
// text, and as a Result on cmd's output in JSON and YAML.
func NotImplemented(cmd *cobra.Command, f output.Format) humane.Error {
	msg := fmt.Sprintf("%q is not implemented yet", cmd.CommandPath())
	help := "the command is planned for a later milestone; track progress at https://github.com/SpechtLabs/sigil/blob/main/roadmap.yml"
	if f == output.Text {
		return humane.New(msg, help)
	}
	if err := output.Encode(cmd.OutOrStdout(), f, Result{Error: Failure{Kind: kindNotImplemented, Message: msg, Help: help}}); err != nil {
		return err
	}
	return pretty.Fail(msg, help)
}
