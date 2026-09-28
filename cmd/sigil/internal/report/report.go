// Package report turns an evaluation's result.Result into what eval
// prints: a text summary with the full
// trace, or the same content as JSON or YAML.
package report

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/result"
)

// Failure kinds.
const (
	FailAssertion = "assertion"
	FailConflict  = "conflict"
	FailRuntime   = "runtime"
)

// Report is one evaluation.
type Report struct {
	Payload  map[string]any `json:"payload,omitempty" yaml:"payload,omitempty"` //nolint:emptyinterface // payload values are the host's, of any Sigil type
	Error    *Failure       `json:"error,omitempty" yaml:"error,omitempty"`
	Policy   string         `json:"policy" yaml:"policy"`
	Decision string         `json:"decision,omitempty" yaml:"decision,omitempty"`
	Reason   string         `json:"reason,omitempty" yaml:"reason,omitempty"`
	Outcome  []Entry        `json:"outcome" yaml:"outcome"`
	Trace    []Entry        `json:"trace" yaml:"trace"`
	Collect  bool           `json:"collect,omitempty" yaml:"collect,omitempty"`
}

// Entry is one candidate or outcome entry.
type Entry struct {
	Payload    map[string]any `json:"payload,omitempty" yaml:"payload,omitempty"` //nolint:emptyinterface // payload values are the host's, of any Sigil type
	Decision   string         `json:"decision" yaml:"decision"`
	Reason     string         `json:"reason" yaml:"reason"`
	Policy     string         `json:"policy,omitempty" yaml:"policy,omitempty"`
	Position   string         `json:"position,omitempty" yaml:"position,omitempty"`
	Chain      []string       `json:"chain,omitempty" yaml:"chain,omitempty"` // the invocations it was reached through, outermost first
	Conditions []string       `json:"conditions,omitempty" yaml:"conditions,omitempty"`
	Outcome    bool           `json:"outcome,omitempty" yaml:"outcome,omitempty"` // in the outcome the host acts on
	values     []field        // the payload in declaration order, for text
}

// field is one payload field's canonical value.
type field struct {
	value any //nolint:emptyinterface // canonical values are dynamically typed
	name  string
}

// Failure is why an evaluation didn't produce an outcome.
type Failure struct {
	Kind       string   `json:"kind" yaml:"kind"`
	Message    string   `json:"message" yaml:"message"`
	Asserts    []Assert `json:"asserts,omitempty" yaml:"asserts,omitempty"`
	Candidates []Entry  `json:"candidates,omitempty" yaml:"candidates,omitempty"`
}

// Assert is one failing assert.
type Assert struct {
	Reason   string `json:"reason" yaml:"reason"`
	Position string `json:"position" yaml:"position"`
	Cause    string `json:"cause,omitempty" yaml:"cause,omitempty"`
}

// New builds the report for one evaluation.
func New(k *project.Kind, res *result.Result) *Report {
	r := &Report{Policy: res.Policy, Collect: res.Collect, Outcome: []Entry{}, Trace: []Entry{}}
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

// Text renders the report for a terminal.
func (r *Report) Text() string {
	var b strings.Builder
	switch {
	case r.Error != nil:
		fmt.Fprintf(&b, "%s: %s, the host falls back to %s\n", r.Policy, r.Error.headline(), r.summary())
	default:
		fmt.Fprintf(&b, "%s: %s\n", r.Policy, r.summary())
	}
	if !r.Collect && r.Error == nil {
		for _, e := range r.Outcome {
			writePayload(&b, "  ", e)
		}
	}
	if r.Collect && r.Error == nil {
		for _, e := range r.Outcome {
			fmt.Fprintf(&b, "  %-8s %-17s %s\n", e.Decision, e.Reason, e.Position)
			writePayload(&b, "           ", e)
		}
	}
	if r.Error != nil {
		b.WriteString("\n" + r.Error.text())
	}
	b.WriteString("\n")
	if len(r.Trace) == 0 {
		b.WriteString("trace: no rule fired\n")
		return b.String()
	}
	fmt.Fprintf(&b, "trace: %d %s\n", len(r.Trace), plural(len(r.Trace), "candidate", "candidates"))
	for _, e := range r.Trace {
		mark := " "
		if e.Outcome {
			mark = "*"
		}
		fmt.Fprintf(&b, "%s %-8s %-17s %s\n", mark, e.Decision, e.Reason, e.location())
		for i, c := range e.Conditions {
			if i > 0 {
				c = "and " + c
			}
			b.WriteString("           " + c + "\n")
		}
		writePayload(&b, "           ", e)
	}
	return b.String()
}

// summary names the outcome: the decision and reason, or how many
// decisions a collecting kind returned.
func (r *Report) summary() string {
	if !r.Collect {
		s := r.Decision + " " + r.Reason
		if len(r.Outcome) == 1 && r.Outcome[0].Position == "" {
			s += " (the kind's default)"
		}
		return s
	}
	switch n := len(r.Outcome); n {
	case 0:
		return "no decisions"
	default:
		return fmt.Sprintf("%d %s", n, plural(n, "decision", "decisions"))
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

func (f *Failure) text() string {
	var b strings.Builder
	switch f.Kind {
	case FailAssertion:
		for _, a := range f.Asserts {
			fmt.Fprintf(&b, "assert %q failed at %s\n", a.Reason, a.Position)
			if a.Cause != "" {
				b.WriteString("  " + a.Cause + "\n")
			}
		}
	case FailConflict:
		b.WriteString("conflict: " + f.Message + "\n")
		for _, c := range f.Candidates {
			fmt.Fprintf(&b, "  %-8s %-17s %s\n", c.Decision, c.Reason, c.location())
		}
	default:
		b.WriteString("runtime error: " + f.Message + "\n")
	}
	return b.String()
}

func (e Entry) location() string {
	return strings.Join(append(append([]string{}, e.Chain...), e.Position), " → ")
}

// failure describes why the evaluation failed.
func failure(k *project.Kind, fl *result.Failure) *Failure {
	switch {
	case fl.Runtime != nil:
		return &Failure{Kind: FailRuntime, Message: runtimeText(fl.Runtime)}
	case fl.Conflict != nil:
		f := &Failure{Kind: FailConflict, Message: fl.Conflict.Msg}
		for _, c := range fl.Conflict.Candidates {
			f.Candidates = append(f.Candidates, candidate(k, c))
		}
		return f
	}
	f := &Failure{Kind: FailAssertion}
	reasons := make([]string, len(fl.Asserts))
	for i, a := range fl.Asserts {
		reasons[i] = strconv.Quote(a.Reason)
		as := Assert{Reason: a.Reason, Position: result.Chain(append(append([]result.Position{}, a.Chain...), a.Position))}
		if a.Cause != nil {
			as.Cause = runtimeText(a.Cause)
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
// encoding/json and YAML print the way a policy author writes it.
func Plain(v any) any { //nolint:emptyinterface // canonical values are dynamically typed
	switch v := v.(type) {
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

// writePayload writes one line per payload field, in declaration order,
// each value as a Sigil literal.
func writePayload(b *strings.Builder, indent string, e Entry) {
	for _, f := range e.values {
		b.WriteString(indent + f.name + " = " + constant.Format(f.value) + "\n")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
