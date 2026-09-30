// Package report renders what `sigil eval` prints as text: the decision,
// with the full trace. The JSON and YAML record is package workspace's
// [workspace.Report], which the WebAssembly module prints too.
//
// [New] builds a [Report] from the result, and [Text] renders it.
package report

import (
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// Failure kinds, the values of [Failure.Kind].
const (
	FailAssertion = workspace.FailAssertion
	FailConflict  = workspace.FailConflict
	FailRuntime   = workspace.FailRuntime
	FailCanceled  = workspace.FailCanceled
)

type (
	// Report is one evaluation, as JSON and YAML print it.
	Report = workspace.Report
	// Entry is one candidate or outcome entry.
	Entry = workspace.Entry
	// Failure is why an evaluation didn't produce an outcome.
	Failure = workspace.Failure
	// Assert is one failing assert.
	Assert = workspace.Assert
)

// New builds the report for one evaluation, as [workspace.NewReport]
// does.
func New(k *workspace.Kind, res *result.Result) *Report { return workspace.NewReport(k, res) }

// Text renders r for a human, styled by t. Candidates read the
// way a policy writes them, `allow(reason: admin)`, with the conditions
// that held and the payload beneath, and the ones in the outcome marked
// `*`:
//
//	access.main: allow(reason: admin)
//	  ttl = 8h
//
//	trace: 2 candidates
//	  * allow(reason: admin)      access/main.sigil:6:3
//	      when user.admin
//	      ttl = 8h
//	    deny(reason: too_old)     access/main.sigil:14:3
//
// A failed evaluation leads with why, then the fallback the host acts on.
func Text(r *Report, t pretty.Theme) string {
	var b strings.Builder
	width := columnWidth(r)
	switch {
	case r.Error != nil:
		fmt.Fprintf(&b, "%s: %s, the host falls back to %s\n", t.Accent(r.Policy), t.Fail(headline(r.Error)), summary(r, t))
	default:
		fmt.Fprintf(&b, "%s: %s\n", t.Accent(r.Policy), summary(r, t))
	}
	if !r.Collect && r.Error == nil {
		for _, e := range r.Outcome {
			writePayload(&b, t, "  ", e)
		}
	}
	if r.Collect && r.Error == nil {
		for _, e := range r.Outcome {
			b.WriteString("  " + row(t, width, e, e.Position, true) + "\n")
			writePayload(&b, t, "    ", e)
		}
	}
	if r.Error != nil {
		b.WriteString("\n" + failureText(r.Error, t, width))
	}
	b.WriteString("\n")
	if len(r.Trace) == 0 {
		b.WriteString(t.Accent("trace:") + " no rule fired\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%s %d %s\n", t.Accent("trace:"), len(r.Trace), plural(len(r.Trace), "candidate", "candidates"))
	for _, e := range r.Trace {
		mark := "    "
		if e.Outcome {
			mark = "  " + t.Ok("*") + " "
		}
		b.WriteString(mark + row(t, width, e, location(e), e.Outcome) + "\n")
		writeConditions(&b, t, "      ", e.Conditions)
		writePayload(&b, t, "      ", e)
	}
	return b.String()
}

// columnWidth is the widest candidate head in the report, so their locations
// line up in one column.
func columnWidth(r *Report) int {
	w := 0
	for _, e := range r.Outcome {
		w = max(w, len(head(e)))
	}
	for _, e := range r.Trace {
		w = max(w, len(head(e)))
	}
	if r.Error != nil {
		for _, e := range r.Error.Candidates {
			w = max(w, len(head(e)))
		}
		for _, a := range r.Error.Asserts {
			for _, e := range a.Outcome {
				w = max(w, len(head(e)))
			}
		}
	}
	return w
}

// summary names the outcome: the decision and reason, or how many
// decisions a collecting kind returned.
func summary(r *Report, t pretty.Theme) string {
	if !r.Collect {
		s := t.Bold(r.Decision + "(reason: " + r.Reason + ")")
		if len(r.Outcome) == 1 && r.Outcome[0].Position == "" {
			note := "the kind's default"
			if r.OnConflict {
				note = "the kind's conflict outcome"
			}
			s += ", " + t.Muted(note)
		}
		return s
	}
	switch n := len(r.Outcome); n {
	case 0:
		return t.Bold("no decisions")
	default:
		return t.Bold(fmt.Sprintf("%d %s", n, plural(n, "decision", "decisions")))
	}
}

// headline says in a few words how the evaluation failed.
func headline(f *Failure) string {
	switch {
	case f.Kind == FailConflict:
		return "the candidates conflict"
	case f.Kind == FailAssertion && len(f.Asserts) == 1:
		return "an assert failed"
	case f.Kind == FailAssertion:
		return fmt.Sprintf("%d asserts failed", len(f.Asserts))
	case f.Kind == FailCanceled:
		return "the evaluation was stopped"
	}
	return "a runtime error stopped the evaluation"
}

// failureText details the failure, ending with what to do about it.
func failureText(f *Failure, t pretty.Theme, width int) string {
	var b strings.Builder
	switch f.Kind {
	case FailAssertion:
		for _, a := range f.Asserts {
			fmt.Fprintf(&b, "%s %s failed at %s\n", t.Fail("assert"), t.Bold(a.Reason), t.Location(a.Position))
			if a.Cause != "" {
				b.WriteString("  " + a.Cause + "\n")
			}
			if a.Help != "" {
				b.WriteString("    " + t.Help("= help:") + " " + a.Help + "\n")
			}
			if len(a.Outcome) > 0 {
				b.WriteString("  " + t.Muted("the outcome it read:") + "\n")
			}
			for _, c := range a.Outcome {
				b.WriteString("    " + row(t, width, c, location(c), false) + "\n")
				writePayload(&b, t, "      ", c)
			}
		}
	case FailConflict:
		b.WriteString(t.Fail("conflict:") + " " + f.Message + "\n")
		for _, c := range f.Candidates {
			b.WriteString("    " + row(t, width, c, location(c), false) + "\n")
		}
	case FailCanceled:
		b.WriteString(t.Fail("stopped:") + " " + f.Message + "\n")
	default:
		b.WriteString(t.Fail("runtime error:") + " " + f.Message + "\n")
	}
	if f.Help != "" {
		b.WriteString("  " + t.Help("= help:") + " " + f.Help + "\n")
	}
	return b.String()
}

// head names a candidate the way a policy writes it: `allow(reason: admin)`.
func head(e Entry) string {
	return e.Decision + "(reason: " + e.Reason + ")"
}

// row lays out a candidate: its head padded to width, then where it
// came from. Padding comes before styling, so the column lines up on a
// terminal too.
func row(t pretty.Theme, width int, e Entry, location string, emphasize bool) string {
	h := fmt.Sprintf("%-*s", width, head(e))
	if emphasize {
		h = t.Bold(h)
	}
	return h + "  " + t.Location(location)
}

func location(e Entry) string {
	return strings.Join(append(append([]string{}, e.Chain...), e.Position), " → ")
}

// writeConditions writes the conditions that held, `when` the first and
// `and` each one after, aligned on the expressions.
func writeConditions(b *strings.Builder, t pretty.Theme, indent string, conds []string) {
	for i, c := range conds {
		if i == 0 {
			b.WriteString(indent + t.Muted("when") + " " + c + "\n")
			continue
		}
		b.WriteString(indent + " " + t.Muted("and") + " " + c + "\n")
	}
}

// writePayload writes one line per payload field, in declaration order,
// each value as a Sigil literal.
func writePayload(b *strings.Builder, t pretty.Theme, indent string, e Entry) {
	for _, f := range e.Fields {
		b.WriteString(indent + t.Key(f.Name+" =") + " " + constant.Format(f.Value) + "\n")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
