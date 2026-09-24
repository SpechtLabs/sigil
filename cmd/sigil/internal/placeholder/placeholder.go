// Package placeholder backs the commands that are part of the documented CLI
// surface but not implemented yet. Delete it once the last command is real.
package placeholder

import (
	"fmt"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
)

// NotImplemented reports that cmd isn't implemented yet.
func NotImplemented(cmd *cobra.Command) humane.Error {
	return humane.New(
		fmt.Sprintf("%q is not implemented yet", cmd.CommandPath()),
		"sigil is in its design phase; track progress at https://github.com/SpechtLabs/sigil/blob/main/roadmap.yml",
	)
}
