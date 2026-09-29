// Package report turns an evaluation's result.Result into what `sigil eval`
// prints: a text summary with the full trace, or the same content as JSON
// or YAML.
//
// [New] builds a [Report] from the result. The Report is the JSON and YAML
// record, with every payload value in the form [Plain] gives it, and
// [Report.Text] renders it as text.
package report

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/result"
)

// Failure kinds, the values of [Failure.Kind].
const (
	FailAssertion = "assertion" // one or more asserts failed
	FailConflict  = "conflict"  // the winning candidates conflict
	FailRuntime   = "runtime"   // a runtime error stopped the evaluation
)

// Report is one evaluation. For a kind that returns one decision,
// Decision, Reason and Payload repeat the outcome's single entry, which is
// the kind's default when no rule fired or the evaluation failed, or its
// conflict outcome after a conflict when the kind declares one.
type Report struct {
	Payload  map[string]any `json:"payload,omitempty" yaml:"payload,omitempty"` //nolint:emptyinterface // payload values are the host's, of any Sigil type
	Error    *Failure       `json:"error,omitempty" yaml:"error,omitempty"`     // why the evaluation failed; nil when it didn't
	Policy   string         `json:"policy" yaml:"policy"`                       // the root policy
	Decision string         `json:"decision,omitempty" yaml:"decision,omitempty"`
	Reason   string         `json:"reason,omitempty" yaml:"reason,omitempty"`
	Outcome  []Entry        `json:"outcome" yaml:"outcome"`                     // what the host acts on; never nil
	Trace    []Entry        `json:"trace" yaml:"trace"`                         // every candidate the rules produced, winners first; never nil
	Collect  bool           `json:"collect,omitempty" yaml:"collect,omitempty"` // the kind collects every candidate
	// onConflict says the outcome is the kind's conflict outcome, not its
	// default, so the text names which of the two the host falls back to.
	onConflict bool
}

// Entry is one candidate or outcome entry.
type Entry struct {
	Payload    map[string]any `json:"payload,omitempty" yaml:"payload,omitempty"` //nolint:emptyinterface // payload values are the host's, of any Sigil type
	Decision   string         `json:"decision" yaml:"decision"`
	Reason     string         `json:"reason" yaml:"reason"`
	Policy     string         `json:"policy,omitempty" yaml:"policy,omitempty"`         // the policy whose rule produced it; empty for the default
	Position   string         `json:"position,omitempty" yaml:"position,omitempty"`     // of the rule; empty for the default
	Chain      []string       `json:"chain,omitempty" yaml:"chain,omitempty"`           // the invocations it was reached through, outermost first
	Conditions []string       `json:"conditions,omitempty" yaml:"conditions,omitempty"` // the `when` conditions that held, for a candidate of a winning decision
	Outcome    bool           `json:"outcome,omitempty" yaml:"outcome,omitempty"`       // in the outcome the host acts on
	values     []field        // the payload in declaration order, for text
}

// field is one payload field's canonical value.
type field struct {
	value any //nolint:emptyinterface // canonical values are dynamically typed
	name  string
}

// Failure is why an evaluation didn't produce an outcome.
type Failure struct {
	Kind       string   `json:"kind" yaml:"kind"` // FailAssertion, FailConflict or FailRuntime
	Message    string   `json:"message" yaml:"message"`
	Help       string   `json:"help" yaml:"help"`                                 // what to do about it
	Asserts    []Assert `json:"asserts,omitempty" yaml:"asserts,omitempty"`       // the failing asserts, for FailAssertion
	Candidates []Entry  `json:"candidates,omitempty" yaml:"candidates,omitempty"` // the conflicting candidates, for FailConflict
}

// Assert is one failing assert.
type Assert struct {
	Reason   string  `json:"reason" yaml:"reason"`                       // the assert's name
	Position string  `json:"position" yaml:"position"`                   // its position, after the invocations that reached it
	Cause    string  `json:"cause,omitempty" yaml:"cause,omitempty"`     // the runtime error its condition raised
	Help     string  `json:"help,omitempty" yaml:"help,omitempty"`       // what to do about the cause, when it knows better than the failure's help
	Outcome  []Entry `json:"outcome,omitempty" yaml:"outcome,omitempty"` // for an outcome assert, the candidates that formed the outcome it read
}

// New builds the report for one evaluation.
func New(k *project.Kind, res *result.Result) *Report {
	r := &Report{Policy: res.Policy, Collect: res.Collect, Outcome: []Entry{}, Trace: []Entry{}}
	r.onConflict = res.Failure != nil && res.Failure.Conflict != nil && k.Model.Conflict != nil
	if !r.Collect && len(res.Outcome) == 1 {
		e := res.Outcome[0]
		r.Decision, r.Reason = e.Decision, e.Reason
		r.Payload, _ = payload(k, e.Decision, e.Payload)
	}
	inOutcome := map[string]bool{}
	for _, e := range res.Outcome {
		entry := Entry{Decision: e.Decision, Reason: e.Reason, Policy: e.Policy}
		entry.Payload, entry.values = payload(k, e.Decision, e.Payload)
		if e.Position.IsValid() {
			entry.Position = e.Position.String()
			inOutcome[entry.Position+" "+e.Decision+" "+e.Reason] = true
		}
		r.Outcome = append(r.Outcome, entry)
	}
	for _, c := range res.Trace {
		e := candidate(k, c)
		e.Outcome = res.Failure == nil && inOutcome[e.Position+" "+c.Decision+" "+c.Reason]
		r.Trace = append(r.Trace, e)
	}
	if res.Failure != nil {
		r.Error = failure(k, res.Failure)
	}
	return r
}

// Text renders the report for a human, styled by t. Candidates read the
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
func (r *Report) Text(t pretty.Theme) string {
	var b strings.Builder
	width := r.width()
	switch {
	case r.Error != nil:
		fmt.Fprintf(&b, "%s: %s, the host falls back to %s\n", t.Accent(r.Policy), t.Fail(r.Error.headline()), r.summary(t))
	default:
		fmt.Fprintf(&b, "%s: %s\n", t.Accent(r.Policy), r.summary(t))
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
		b.WriteString("\n" + r.Error.text(t, width))
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
		b.WriteString(mark + row(t, width, e, e.location(), e.Outcome) + "\n")
		writeConditions(&b, t, "      ", e.Conditions)
		writePayload(&b, t, "      ", e)
	}
	return b.String()
}

// width is the widest candidate head in the report, so their locations
// line up in one column.
func (r *Report) width() int {
	w := 0
	for _, e := range r.Outcome {
		w = max(w, len(e.head()))
	}
	for _, e := range r.Trace {
		w = max(w, len(e.head()))
	}
	if r.Error != nil {
		for _, e := range r.Error.Candidates {
			w = max(w, len(e.head()))
		}
		for _, a := range r.Error.Asserts {
			for _, e := range a.Outcome {
				w = max(w, len(e.head()))
			}
		}
	}
	return w
}

// summary names the outcome: the decision and reason, or how many
// decisions a collecting kind returned.
func (r *Report) summary(t pretty.Theme) string {
	if !r.Collect {
		s := t.Bold(r.Decision + "(reason: " + r.Reason + ")")
		if len(r.Outcome) == 1 && r.Outcome[0].Position == "" {
			note := "the kind's default"
			if r.onConflict {
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
func (f *Failure) headline() string {
	switch {
	case f.Kind == FailConflict:
		return "the candidates conflict"
	case f.Kind == FailAssertion && len(f.Asserts) == 1:
		return "an assert failed"
	case f.Kind == FailAssertion:
		return fmt.Sprintf("%d asserts failed", len(f.Asserts))
	}
	return "a runtime error stopped the evaluation"
}

// text details the failure, ending with what to do about it.
func (f *Failure) text(t pretty.Theme, width int) string {
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
				b.WriteString("    " + row(t, width, c, c.location(), false) + "\n")
				writePayload(&b, t, "      ", c)
			}
		}
	case FailConflict:
		b.WriteString(t.Fail("conflict:") + " " + f.Message + "\n")
		for _, c := range f.Candidates {
			b.WriteString("    " + row(t, width, c, c.location(), false) + "\n")
		}
	default:
		b.WriteString(t.Fail("runtime error:") + " " + f.Message + "\n")
	}
	if f.Help != "" {
		b.WriteString("  " + t.Help("= help:") + " " + f.Help + "\n")
	}
	return b.String()
}

// help says what to do about a failure of kind.
func help(kind string) string {
	switch kind {
	case FailAssertion:
		return "the input breaks an assert of the policy; if the input is right, the policy's assumption is wrong"
	case FailConflict:
		return "a conflict is a defect in the policy: rank the reasons with precedence, or keep the exclusive outcomes' conditions apart"
	}
	return "fix the expression the runtime error points at, or the input it read"
}

// head names a candidate the way a policy writes it: `allow(reason: admin)`.
func (e Entry) head() string {
	return e.Decision + "(reason: " + e.Reason + ")"
}

// row lays out a candidate: its head padded to width, then where it
// came from. Padding comes before styling, so the column lines up on a
// terminal too.
func row(t pretty.Theme, width int, e Entry, location string, emphasize bool) string {
	h := fmt.Sprintf("%-*s", width, e.head())
	if emphasize {
		h = t.Bold(h)
	}
	return h + "  " + t.Location(location)
}

func (e Entry) location() string {
	return strings.Join(append(append([]string{}, e.Chain...), e.Position), " → ")
}

// failure describes why the evaluation failed.
func failure(k *project.Kind, fl *result.Failure) *Failure {
	switch {
	case fl.Runtime != nil:
		h := fl.Runtime.Help
		if h == "" {
			h = help(FailRuntime)
		}
		return &Failure{Kind: FailRuntime, Message: runtimeText(fl.Runtime), Help: h}
	case fl.Conflict != nil:
		f := &Failure{Kind: FailConflict, Message: fl.Conflict.Msg, Help: help(FailConflict)}
		for _, c := range fl.Conflict.Candidates {
			f.Candidates = append(f.Candidates, candidate(k, c))
		}
		return f
	}
	f := &Failure{Kind: FailAssertion, Help: help(FailAssertion)}
	reasons := make([]string, len(fl.Asserts))
	for i, a := range fl.Asserts {
		reasons[i] = strconv.Quote(a.Reason)
		as := Assert{Reason: a.Reason, Position: result.Chain(append(append([]result.Position{}, a.Chain...), a.Position))}
		if a.Cause != nil {
			as.Cause, as.Help = runtimeText(a.Cause), a.Cause.Help
		}
		for _, c := range a.Outcome {
			as.Outcome = append(as.Outcome, candidate(k, c))
		}
		f.Asserts = append(f.Asserts, as)
	}
	f.Message = "assertions failed: " + strings.Join(reasons, ", ")
	return f
}

func runtimeText(r *result.Runtime) string {
	return r.Position.String() + ": " + r.Msg
}

func candidate(k *project.Kind, c result.Candidate) Entry {
	e := Entry{Decision: c.Decision, Reason: c.Reason, Policy: c.Policy, Position: c.Position.String()}
	e.Payload, e.values = payload(k, c.Decision, c.Payload)
	for _, s := range c.Chain {
		e.Chain = append(e.Chain, s.String())
	}
	for _, cond := range c.Conditions {
		e.Conditions = append(e.Conditions, cond.Text)
	}
	return e
}

// payload returns a payload's fields in declaration order as canonical
// values, and as plain ones for JSON and YAML.
func payload(k *project.Kind, decision string, p map[string]any) (map[string]any, []field) { //nolint:emptyinterface // payload values are the host's Go values
	d := k.Model.Decision(decision)
	if d == nil || len(p) == 0 {
		return nil, nil
	}
	plain := make(map[string]any, len(p))
	fields := make([]field, 0, len(p))
	for _, f := range d.Fields {
		if v, ok := p[f.Name]; ok {
			c := k.Binding.Canonical(f.Type, reflect.ValueOf(v))
			plain[f.Name] = Plain(c)
			fields = append(fields, field{name: f.Name, value: c})
		}
	}
	return plain, fields
}

// Plain turns a canonical value (see gokind.Binding.Canonical) into one
// encoding/json and YAML print the way a policy author writes it. A
// duration becomes a string in Sigil's syntax, `1h30m`, and a time an
// RFC 3339 string, and an enum value its name. Lists and maps are
// converted element by element, with map keys turned into strings. Any other value is returned as it is.
func Plain(v any) any { //nolint:emptyinterface // canonical values are dynamically typed
	switch v := v.(type) {
	case constant.EnumValue:
		return string(v)
	case time.Duration:
		return constant.FormatDuration(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = Plain(x)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, x := range v {
			ks, ok := Plain(key).(string)
			if !ok {
				ks = fmt.Sprint(Plain(key))
			}
			out[ks] = Plain(x)
		}
		return out
	}
	return v
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
	for _, f := range e.values {
		b.WriteString(indent + t.Key(f.name+" =") + " " + constant.Format(f.value) + "\n")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
