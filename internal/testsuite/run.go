package testsuite

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Error is a problem with a test file or one of its cases. It implements
// [humane.Error], with Help as its advice.
type Error struct {
	File string
	Case string // empty for the file as a whole
	Msg  string
	Help string // how to fix it; empty when there's no advice
	Line int    // 0 when unknown
}

// Errors is several problems with a test file, sorted by line, such as
// every stub [Parse] found of the wrong shape. It implements
// [humane.Error].
type Errors []*Error

// Eval evaluates the suite's policy against one input, a value of the
// binding's input struct, and reduces the result to an [Outcome]. The
// caller of [Runner.RunCase] supplies it.
type Eval func(ctx context.Context, input reflect.Value) *Outcome

// Outcome is what an evaluation produced, in a form both the CLI and
// package policytest can give: the entries the host acts on, or why the
// evaluation failed.
type Outcome struct {
	Err     string   // a conflict or runtime error, described; empty otherwise
	Runtime string   // the runtime error's message, without its position, when the evaluation failed with one
	Detail  string   // what the error said, with its position, which a report shows below Err; empty to keep Err on its own
	Help    string   // what to do about the error, when the evaluator knows
	Entries []Got    // the outcome; for a `collect one` kind, the one decision
	Asserts []string // the reasons of the failing asserts, when asserts failed
}

// Got is one entry of an outcome.
type Got struct {
	Payload  map[string]any //nolint:emptyinterface // payload values, as the host's Go values
	Decision string
	Reason   string
	Position string // where the constructor is, as a failure reports it
}

// Result is one case run.
type Result struct {
	Case *Case
	// Err is set when the case couldn't run at all: its input couldn't be
	// read or doesn't fit the kind.
	Err *Error
	// Failures lists how the evaluation differs from what the case
	// expects; empty when it passed.
	Failures []Failure
}

// Failure is one way an evaluation differs from what a case expects.
// Text says so in one sentence; when the difference is between two
// values, Got and Want hold them separately, for a report that lines
// them up. When what the evaluation got is an error, Got names it, and
// Detail and Help hold what it said and what to do about it, for a
// report to show on lines of their own.
type Failure struct {
	Text   string
	Got    string
	Want   string
	Detail string
	Help   string
}

// String implements [fmt.Stringer]. It returns Text.
func (f Failure) String() string { return f.Text }

// diff is a failure between what the evaluation produced and what the
// case wanted.
func diff(got, want string) Failure {
	return Failure{Text: "got " + got + ", want " + want, Got: got, Want: want}
}

// Runner runs cases against one kind.
type Runner struct {
	Kind    *kind.Kind
	Binding *gokind.Binding // decodes inputs and expected payloads into the host's Go types
	FS      fs.FS           // what input files are read from
}

// Passed reports whether the case ran and got what it expected.
func (r *Result) Passed() bool { return r.Err == nil && len(r.Failures) == 0 }

// RunCase runs one case of s with eval: it reads the case's input,
// decodes it into the binding's input struct, evaluates it and compares
// the outcome with what the case expects. An input that can't be read or
// decoded sets the Result's Err, and eval isn't called. The suite should
// have passed [Suite.Validate] first.
func (r *Runner) RunCase(ctx context.Context, s *Suite, c *Case, eval Eval) *Result {
	res := &Result{Case: c}
	raw, err := s.ReadInput(r.FS, c)
	if err != nil {
		res.Err = err
		return res
	}
	in, derr := r.Binding.DecodeInput(r.Kind, raw)
	if derr != nil {
		res.Err = &Error{File: s.File, Line: c.Line, Case: c.Name, Msg: "input " + derr.Error(), Help: strings.Join(derr.Advice(), "; ")}
		return res
	}
	res.Failures = r.compare(&c.Expect, eval(ctx, in))
	return res
}

// Bind returns the binding a case of s evaluates with: the runner's, with
// the suite's stubs and then the case's own replacing host functions, per
// function. With a nil case it is the binding of every case that stubs
// nothing of its own, which is the runner's own when the suite stubs
// nothing either. Host functions are resolved when a policy compiles, so
// the caller compiles the policy once with the suite's binding and again
// for each case with stubs of its own. The suite should have passed
// [Suite.Validate]; a stub that doesn't fit the binding is an error at
// the stub's line.
func (r *Runner) Bind(s *Suite, c *Case) (*gokind.Binding, *Error) {
	set, name := s.Stubs, ""
	if c != nil {
		set, name = set.Merge(c.Stubs), c.Name
	}
	b, errs := set.Bind(r.Kind, r.Binding)
	if errs == nil {
		return b, nil
	}
	return nil, &Error{File: s.File, Line: errs[0].Line, Case: name, Msg: errs[0].Msg, Help: errs[0].Help}
}

// Display implements humane.Error. It returns each problem's Display,
// one per line.
func (e Errors) Display() string {
	lines := make([]string, len(e))
	for i, err := range e {
		lines[i] = err.Display()
	}
	return strings.Join(lines, "\n")
}

// Advice implements humane.Error. It returns each problem's Help,
// once, in order.
func (e Errors) Advice() []string {
	var out []string
	for _, err := range e {
		if err.Help != "" && !slices.Contains(out, err.Help) {
			out = append(out, err.Help)
		}
	}
	return out
}

// Cause implements humane.Error. It returns nil.
func (e Errors) Cause() error { return nil } //nolint:humaneerror // humane.Error fixes the signature

// Error implements the error interface. It returns each problem's
// Error, one per line.
func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i, err := range e {
		lines[i] = err.Error()
	}
	return strings.Join(lines, "\n")
}

// Display implements humane.Error. It returns the error's text followed
// by its advice in parentheses, when it has any.
func (e *Error) Display() string {
	if e.Help == "" {
		return e.Error()
	}
	return e.Error() + " (" + e.Help + ")"
}

// Advice implements humane.Error. It returns Help as the one piece of
// advice, or nil without it.
func (e *Error) Advice() []string {
	if e.Help == "" {
		return nil
	}
	return []string{e.Help}
}

// Cause implements humane.Error. It returns nil: a test file error is
// where the problem starts.
func (e *Error) Cause() error { return nil } //nolint:humaneerror // humane.Error fixes the signature

// Error implements the error interface. It returns the file, line and
// case followed by the message, `a_test.yaml:12: case "denied": ...`,
// leaving out the line and case when unknown.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Line > 0 {
		b.WriteString(":" + strconv.Itoa(e.Line))
	}
	if e.Case != "" {
		fmt.Fprintf(&b, ": case %q", e.Case)
	}
	b.WriteString(": " + e.Msg)
	return b.String()
}

// compare checks an evaluation against the expectation.
func (r *Runner) compare(e *Expect, out *Outcome) []Failure {
	switch {
	case e.Error != nil:
		return compareError(*e.Error, out)
	case e.Asserts != nil:
		return r.compareAsserts(e.Asserts, out)
	case failed(out):
		want := "the outcome " + r.entries(derefEntries(e.Outcome))
		if e.Outcome == nil {
			want = call(e.Decision, e.Reason)
		}
		return []Failure{mismatch(out, want)}
	case e.Outcome != nil:
		return r.compareOutcome(*e.Outcome, out.Entries)
	}
	if len(out.Entries) == 0 {
		return []Failure{diff("no decision", call(e.Decision, e.Reason))}
	}
	got := out.Entries[0]
	if got.Decision != e.Decision || got.Reason != e.Reason {
		return []Failure{diff(call(got.Decision, got.Reason), call(e.Decision, e.Reason))}
	}
	return r.comparePayload(e.Decision, e.Payload, got.Payload)
}

// compareError checks that the evaluation failed with a runtime error
// whose message contains want.
func compareError(want string, out *Outcome) []Failure {
	if out.Runtime != "" && strings.Contains(out.Runtime, want) {
		return nil
	}
	return []Failure{mismatch(out, fmt.Sprintf("a runtime error containing %q", want))}
}

func (r *Runner) compareAsserts(want []string, out *Outcome) []Failure {
	if out.Asserts == nil {
		return []Failure{mismatch(out, "failing asserts "+strings.Join(want, ", "))}
	}
	got := slices.Clone(out.Asserts)
	sort.Strings(got)
	w := slices.Clone(want)
	sort.Strings(w)
	if slices.Equal(slices.Compact(got), slices.Compact(w)) {
		return nil
	}
	return []Failure{{
		Text: "got failing asserts " + strings.Join(got, ", ") + ", want " + strings.Join(w, ", "),
		Got:  "failing asserts " + strings.Join(got, ", "),
		Want: "failing asserts " + strings.Join(w, ", "),
	}}
}

// compareOutcome matches the expected entries against the outcome in any
// order. An expected entry matches an outcome entry with its decision and
// reason and every payload field it lists.
func (r *Runner) compareOutcome(want []Entry, got []Got) []Failure {
	used := make([]bool, len(got))
	var failures []Failure
	for _, w := range want {
		found := false
		for i, g := range got {
			if used[i] || g.Decision != w.Decision || g.Reason != w.Reason {
				continue
			}
			if len(r.comparePayload(w.Decision, w.Payload, g.Payload)) == 0 {
				used[i], found = true, true
				break
			}
		}
		if !found {
			failures = append(failures, Failure{Text: "missing from the outcome: " + r.entry(w)})
		}
	}
	for i, g := range got {
		if !used[i] {
			failures = append(failures, Failure{Text: "not expected in the outcome: " + call(g.Decision, g.Reason) + " at " + g.Position})
		}
	}
	return failures
}

// comparePayload compares the listed fields; fields the case doesn't list
// aren't checked.
func (r *Runner) comparePayload(decision string, want, got map[string]any) []Failure { //nolint:emptyinterface // payload values
	d := r.Kind.Decision(decision)
	var failures []Failure
	for _, f := range d.Fields {
		raw, listed := want[f.Name]
		if !listed {
			continue
		}
		w, err := r.expected(decision, f, raw)
		if err != nil {
			failures = append(failures, Failure{Text: "expected payload " + err.Error()})
			continue
		}
		g := r.Binding.Canonical(f.Type, reflect.ValueOf(got[f.Name]))
		if !reflect.DeepEqual(w, g) {
			failures = append(failures, Failure{
				Text: fmt.Sprintf("payload %s = %s, want %s", f.Name, constant.Format(g), constant.Format(w)),
				Got:  f.Name + " = " + constant.Format(g),
				Want: f.Name + " = " + constant.Format(w),
			})
		}
	}
	return failures
}

// expected decodes an expected payload value into its canonical form.
func (r *Runner) expected(decision string, f *kind.Field, raw any) (any, humane.Error) { //nolint:emptyinterface // canonical values are dynamically typed
	pt := r.Binding.Payloads[decision]
	idx := r.Binding.Fields["decision "+decision+"."+f.Name]
	if pt == nil || idx == nil {
		return nil, humane.New(f.Name+": the payload isn't bound to Go", "the binding and the kind disagree, which is a bug in sigil")
	}
	v := reflect.New(pt.FieldByIndex(idx).Type).Elem()
	if err := r.Binding.Decode(f.Type, raw, v, f.Name); err != nil {
		return nil, err
	}
	return r.Binding.Canonical(f.Type, v), nil
}

func (r *Runner) entry(e Entry) string {
	s := call(e.Decision, e.Reason)
	if len(e.Payload) == 0 {
		return s
	}
	names := make([]string, 0, len(e.Payload))
	for n := range e.Payload {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s: %v", n, e.Payload[n])
	}
	return s + " {" + strings.Join(parts, ", ") + "}"
}

func (r *Runner) entries(es []Entry) string {
	if len(es) == 0 {
		return "[]"
	}
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = r.entry(e)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// mismatch is a failure between what the evaluation produced, which may
// be an error with a detail, and what the case wanted.
func mismatch(out *Outcome, want string) Failure {
	f := diff(describe(out), want)
	if out.Detail != "" {
		f.Text = "got " + f.Got + " (" + out.Detail + "), want " + want
		f.Detail, f.Help = out.Detail, out.Help
	}
	return f
}

// failed reports whether the evaluation failed.
func failed(out *Outcome) bool { return out.Err != "" || out.Asserts != nil }

// describe names what an evaluation produced, for a failure message.
func describe(out *Outcome) string {
	switch {
	case out.Asserts != nil:
		return "failing asserts " + strings.Join(out.Asserts, ", ")
	case out.Err != "":
		return out.Err
	case len(out.Entries) == 1:
		return call(out.Entries[0].Decision, out.Entries[0].Reason)
	}
	parts := make([]string, len(out.Entries))
	for i, e := range out.Entries {
		parts[i] = call(e.Decision, e.Reason)
	}
	return "the outcome [" + strings.Join(parts, ", ") + "]"
}

func derefEntries(es *[]Entry) []Entry {
	if es == nil {
		return nil
	}
	return *es
}

func call(decision, reason string) string { return decision + "(reason: " + reason + ")" }
