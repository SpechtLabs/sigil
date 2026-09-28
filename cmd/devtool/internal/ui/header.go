package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// Plan is what a run is about to do. Header shows it before the slow part
// starts, with the same rows in the same order for every command.
type Plan struct {
	Title    string
	Checkout gotool.Checkout
	// Base is the revision a comparison measures against, e.g.
	// "c4695b6 (main)"; empty when there's none.
	Base     string
	Packages []string
	Filter   string
	// Runs says what runs and for how long, e.g. "10 samples of 200ms
	// each per revision".
	Runs     string
	CPU      int
	Estimate string
	Results  string
}

// Header prints the plan in a box.
func Header(p *pretty.Printer, plan Plan) humane.Error {
	head := plan.Checkout.Short()
	if plan.Checkout.Dirty {
		head += " with uncommitted changes"
	}
	rows := []pretty.KV{{Key: "Head", Value: head}}
	if plan.Base != "" {
		rows = append(rows, pretty.KV{Key: "Base", Value: plan.Base})
	}

	packages := strings.Join(plan.Packages, ", ")
	if len(plan.Packages) > 3 {
		packages = strconv.Itoa(len(plan.Packages)) + " packages"
	}
	rows = append(rows, pretty.KV{Key: "Packages", Value: packages})
	if plan.Filter != "" {
		rows = append(rows, pretty.KV{Key: "Filter", Value: plan.Filter})
	}
	rows = append(rows,
		pretty.KV{Key: "Runs", Value: plan.Runs},
		pretty.KV{Key: "CPUs", Value: strconv.Itoa(plan.CPU)},
	)
	if plan.Estimate != "" {
		rows = append(rows, pretty.KV{Key: "Estimate", Value: plan.Estimate})
	}
	rows = append(rows,
		pretty.KV{Key: "Go", Value: plan.Checkout.Go},
		pretty.KV{Key: "Results", Value: plan.Results},
	)
	return p.KeyValues(plan.Title, rows...)
}

// Interrupted reports a run the user stopped, and returns the error that
// fails the command without printing anything more.
func Interrupted(p *pretty.Printer, done, total int, noun, nouns string) humane.Error {
	msg := fmt.Sprintf("Finished %d of %d %s before the interrupt; the results are incomplete.", done, total, Plural(total, noun, nouns))
	if err := p.Warning("Interrupted", msg); err != nil {
		return err
	}
	return pretty.Fail("interrupted")
}

// Plural returns one when n is 1, and many otherwise.
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
