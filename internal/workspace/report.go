package workspace

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/result"
)

// Failure kinds, the values of [Failure.Kind].
const (
	FailAssertion = "assertion" // one or more asserts failed
	FailConflict  = "conflict"  // the winning candidates conflict
	FailRuntime   = "runtime"   // a runtime error stopped the evaluation
	FailCanceled  = "canceled"  // the evaluation's context was done before it finished, such as past its deadline
)

// Assert phases, the values of [Failure.Phase]: an input assert checks
// the input before any rule runs, and an outcome assert the outcome the
// rules formed.
const (
	PhaseInput   = "input"
	PhaseOutcome = "outcome"
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
	// OnConflict says the outcome is the kind's conflict outcome, not its
	// default, so the text names which of the two the host falls back to.
	OnConflict bool `json:"-" yaml:"-"`
}

// Entry is one candidate or outcome entry.
type Entry struct { //nolint:govet // the field order is the JSON's
	Payload    map[string]any `json:"payload,omitempty" yaml:"payload,omitempty"` //nolint:emptyinterface // payload values are the host's, of any Sigil type
	Decision   string         `json:"decision" yaml:"decision"`
	Reason     string         `json:"reason" yaml:"reason"`
	Policy     string         `json:"policy,omitempty" yaml:"policy,omitempty"`         // the policy whose rule produced it; empty for the default
	Position   string         `json:"position,omitempty" yaml:"position,omitempty"`     // of the rule; empty for the default
	Chain      []string       `json:"chain,omitempty" yaml:"chain,omitempty"`           // the invocations it was reached through, outermost first
	Conditions []string       `json:"conditions,omitempty" yaml:"conditions,omitempty"` // the `when` conditions that held, for a candidate of a winning decision
	Outcome    bool           `json:"outcome,omitempty" yaml:"outcome,omitempty"`       // in the outcome the host acts on
	Fields     []Field        `json:"-" yaml:"-"`                                       // the payload in declaration order, as canonical values, for text
}

// Field is one payload field's canonical value.
type Field struct {
	Value any //nolint:emptyinterface // canonical values are dynamically typed
	Name  string
}

// Failure is why an evaluation didn't produce an outcome.
type Failure struct {
	Kind       string   `json:"kind" yaml:"kind"`                       // FailAssertion, FailConflict, FailRuntime or FailCanceled
	Phase      string   `json:"phase,omitempty" yaml:"phase,omitempty"` // for FailAssertion, the phase the asserts failed in: PhaseInput or PhaseOutcome
	Message    string   `json:"message" yaml:"message"`
	Help       string   `json:"help" yaml:"help"`                                 // what to do about it
	Asserts    []Assert `json:"asserts,omitempty" yaml:"asserts,omitempty"`       // the failing asserts, for FailAssertion
	Candidates []Entry  `json:"candidates,omitempty" yaml:"candidates,omitempty"` // the conflicting candidates, for FailConflict
}

// Assert is one failing assert.
type Assert struct {
	Reason   string  `json:"reason" yaml:"reason"`                       // the assert's name
	Policy   string  `json:"policy" yaml:"policy"`                       // the policy or module it's in
	Position string  `json:"position" yaml:"position"`                   // its position, after the invocations that reached it
	Cause    string  `json:"cause,omitempty" yaml:"cause,omitempty"`     // the runtime error its condition raised
	Help     string  `json:"help,omitempty" yaml:"help,omitempty"`       // what to do about the cause, when it knows better than the failure's help
	Outcome  []Entry `json:"outcome,omitempty" yaml:"outcome,omitempty"` // for an outcome assert, the candidates that formed the outcome it read
}

// NewReport builds the report for one evaluation of a policy of kind k:
// the record `sigil eval` prints as JSON and YAML.
func NewReport(k *Kind, res *result.Result) *Report {
	r := &Report{Policy: res.Policy, Collect: res.Collect, Outcome: []Entry{}, Trace: []Entry{}}
	r.OnConflict = res.Failure != nil && res.Failure.Conflict != nil && k.Model.Conflict != nil
	if !r.Collect && len(res.Outcome) == 1 {
		e := res.Outcome[0]
		r.Decision, r.Reason = e.Decision, e.Reason
		r.Payload, _ = payload(k, e.Decision, e.Payload)
	}
	inOutcome := map[string]bool{}
	for _, e := range res.Outcome {
		entry := Entry{Decision: e.Decision, Reason: e.Reason, Policy: e.Policy}
		entry.Payload, entry.Fields = payload(k, e.Decision, e.Payload)
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

// help says what to do about a failure of kind.
func help(kind string) string {
	switch kind {
	case FailAssertion:
		return "the input breaks an assert of the policy; if the input is right, the policy's assumption is wrong"
	case FailConflict:
		return "a conflict is a defect in the policy: rank the reasons with precedence, or keep the exclusive outcomes' conditions apart"
	case FailCanceled:
		return "the evaluation ran past its deadline or was canceled, so nothing it found counts; give it more time, or look for a loop over a large input"
	}
	return "fix the expression the runtime error points at, or the input it read"
}

// failure describes why the evaluation failed.
func failure(k *Kind, fl *result.Failure) *Failure {
	switch {
	case fl.Runtime != nil:
		h := fl.Runtime.Help
		if h == "" {
			h = help(FailRuntime)
		}
		return &Failure{Kind: FailRuntime, Message: runtimeText(fl.Runtime), Help: h}
	case fl.Canceled != nil:
		return &Failure{Kind: FailCanceled, Message: "the evaluation was stopped: " + fl.Canceled.Error(), Help: help(FailCanceled)}
	case fl.Conflict != nil:
		f := &Failure{Kind: FailConflict, Message: fl.Conflict.Msg, Help: help(FailConflict)}
		for _, c := range fl.Conflict.Candidates {
			f.Candidates = append(f.Candidates, candidate(k, c))
		}
		return f
	}
	f := &Failure{Kind: FailAssertion, Phase: PhaseInput, Help: help(FailAssertion)}
	if fl.OutcomeAsserts {
		f.Phase = PhaseOutcome
	}
	reasons := make([]string, len(fl.Asserts))
	for i, a := range fl.Asserts {
		reasons[i] = strconv.Quote(a.Reason)
		as := Assert{Reason: a.Reason, Policy: a.Policy, Position: result.Chain(append(append([]result.Position{}, a.Chain...), a.Position))}
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

func candidate(k *Kind, c result.Candidate) Entry {
	e := Entry{Decision: c.Decision, Reason: c.Reason, Policy: c.Policy, Position: c.Position.String()}
	e.Payload, e.Fields = payload(k, c.Decision, c.Payload)
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
func payload(k *Kind, decision string, p map[string]any) (map[string]any, []Field) { //nolint:emptyinterface // payload values are the host's Go values
	d := k.Model.Decision(decision)
	if d == nil || len(p) == 0 {
		return nil, nil
	}
	plain := make(map[string]any, len(p))
	fields := make([]Field, 0, len(p))
	for _, f := range d.Fields {
		if v, ok := p[f.Name]; ok {
			c := k.Binding.Canonical(f.Type, reflect.ValueOf(v))
			plain[f.Name] = Plain(c)
			fields = append(fields, Field{Name: f.Name, Value: c})
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
