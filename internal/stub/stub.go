package stub

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/diag"
)

// The keys a stub and a call entry take, in the order help lists them.
var (
	stubKeys = []string{"returns", "error", "calls"}
	callKeys = []string{"args", "returns", "error"}
)

// Set is the stubs of one level, by host function name: a test file's
// `stubs:`, one case's, or what sigil eval reads from its flags.
type Set map[string]*Func

// Func is the stub of one host function. A call whose args match an entry of
// Calls gets that entry's result, the first match winning. Any other call
// gets Returns, or fails with Error; when neither is given, it fails with
// an [*ErrUnmatched].
type Func struct {
	Returns *Value  // the result of a call no entry matches; nil when not given
	Name    string  // the host function's name
	Error   string  // the message a call no entry matches fails with; empty when not given
	Calls   []*Call // results for particular args, matched in order
	Line    int     // the stub's line in its document; 0 for a flag
}

// Call is the result of a call with particular args: exactly one of
// Returns and Error is given.
type Call struct {
	Returns *Value   // nil when not given
	Error   string   // empty when not given
	Args    []*Value // one per param, compared with the call's args as values
	Line    int
}

// Value is a value as YAML or JSON wrote it, with where it is.
type Value struct {
	Raw  any        //nolint:emptyinterface // the value as YAML decodes it without a type, for a value built in Go
	Node *yaml.Node // the value's node, which [Set.Bind] reads against the type it must have; nil for a value built in Go
	Line int        // 0 for a flag
}

// Error is a problem with a stub. It implements [humane.Error], with Help
// as its advice.
type Error struct {
	Msg  string
	Help string // how to fix it
	Line int    // the line in the stubs' document; 0 when unknown
}

// Errors is every problem [Set.UnmarshalYAML] found. It is the error
// yaml.Decode returns for a stubs object that doesn't parse.
type Errors []*Error

// Parse reads a stubs object: a mapping from host function names to
// stubs. It checks each stub's shape and returns every problem, with its
// line, sorted by it: a key a stub or call doesn't take, a level that gives both
// `returns:` and `error:`, or neither (a stub may give neither when it
// has calls), a call without `args:`, and a function stubbed twice. A
// null or missing node is an empty set. Whether the functions exist and
// the values fit is [Set.Bind]'s to check.
func Parse(n *yaml.Node) (Set, []*Error) {
	n = resolve(n)
	if n == nil || n.Kind == 0 || null(n) {
		return nil, nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil, nil
		}
		return Parse(n.Content[0])
	}
	if n.Kind != yaml.MappingNode {
		return nil, []*Error{{Line: n.Line, Msg: "stubs must be an object", Help: "map each host function's name to its stub, like `owner: {returns: ada}`"}}
	}
	set := Set{}
	var errs []*Error
	lines := map[string]int{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := resolve(n.Content[i]), n.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			errs = append(errs, &Error{Line: key.Line, Msg: "a stub's key must be a host function's name", Help: "map each host function's name to its stub, like `owner: {returns: ada}`"})
			continue
		}
		if line, dup := lines[key.Value]; dup {
			errs = append(errs, &Error{Line: key.Line, Msg: fmt.Sprintf("%s is stubbed twice", key.Value), Help: fmt.Sprintf("the first stub is on line %d; give each host function one", line)})
			continue
		}
		lines[key.Value] = key.Line
		st, serrs := parseStub(key.Value, key.Line, val)
		errs = append(errs, serrs...)
		set[key.Value] = st
	}
	if errs != nil {
		return nil, byLine(errs)
	}
	return set, nil
}

// ParseDocument reads a stubs file, YAML or JSON, holding one stubs
// object; an empty file is an empty set. The problems are those of
// [Parse], or the one that the file isn't YAML.
func ParseDocument(src []byte) (Set, []*Error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, []*Error{{Msg: "not valid YAML or JSON: " + strings.TrimPrefix(err.Error(), "yaml: "), Help: "a stubs file maps each host function's name to its stub, like `owner: {returns: ada}`"}}
	}
	return Parse(&doc)
}

// ParseFlag reads one `NAME=VALUE` flag: a stub of the function NAME
// that returns VALUE, JSON or YAML, whatever the args. It returns a set
// of that one stub, to [Set.Merge] into the others.
func ParseFlag(flag string) (Set, *Error) {
	name, src, ok := strings.Cut(flag, "=")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return nil, &Error{Msg: fmt.Sprintf("stub %q isn't NAME=VALUE", flag), Help: "name the host function and the JSON or YAML value it returns for any args, like owner=ada or 'teams=[platform]'"}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, &Error{Msg: fmt.Sprintf("stub %s: the value isn't valid JSON or YAML: %s", name, strings.TrimPrefix(err.Error(), "yaml: ")), Help: "quote the value for the shell, like 'teams=[platform]'"}
	}
	v := &Value{}
	if len(doc.Content) > 0 {
		v.Node = resolve(doc.Content[0])
		v.Raw = untyped(v.Node)
	}
	return Set{name: {Name: name, Returns: v}}, nil
}

// Merge returns the stubs of s with those of over replacing them, per
// function: a function over stubs gets over's stub, whole. Neither set
// changes.
func (s Set) Merge(over Set) Set {
	if len(over) == 0 {
		return s
	}
	if len(s) == 0 {
		return over
	}
	out := maps.Clone(s)
	maps.Copy(out, over)
	return out
}

// UnmarshalYAML implements [yaml.Unmarshaler] with [Parse], so a stubs
// object decodes into a Set. It fails with [Errors].
func (s *Set) UnmarshalYAML(n *yaml.Node) error { //nolint:humaneerror // yaml.Unmarshaler fixes the signature
	set, errs := Parse(n)
	if errs != nil {
		return Errors(errs)
	}
	*s = set
	return nil
}

// Error implements the error interface. It returns every problem's
// Error, joined by "; ".
func (e Errors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

// Display implements humane.Error. It returns the error's text followed
// by its advice in parentheses.
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

// Cause implements humane.Error. It returns nil: a stub error is where
// the problem starts.
func (e *Error) Cause() error { return nil } //nolint:humaneerror // humane.Error fixes the signature

// Error implements the error interface. It returns the message, after
// the line when it's known: `line 4: stub owner gives no result`.
func (e *Error) Error() string {
	if e.Line > 0 {
		return "line " + strconv.Itoa(e.Line) + ": " + e.Msg
	}
	return e.Msg
}

// parseStub reads one function's stub.
func parseStub(name string, line int, n *yaml.Node) (*Func, []*Error) {
	st := &Func{Name: name, Line: line}
	what := "stub " + name
	fields, errs := object(n, what, "a stub", stubKeys)
	if fields == nil {
		return st, errs
	}
	var ferrs []*Error
	st.Returns, st.Error, ferrs = result(fields, what)
	errs = append(errs, ferrs...)
	calls, hasCalls := fields["calls"]
	var cerrs []*Error
	if hasCalls {
		cerrs = st.parseCalls(calls)
		errs = append(errs, cerrs...)
	}
	switch {
	case st.Returns != nil && st.Error != "":
		errs = append(errs, &Error{Line: line, Msg: what + " gives both returns and error", Help: "a call either returns a value or fails; keep one"})
	case st.Returns == nil && st.Error == "" && len(st.Calls) == 0 && len(ferrs) == 0 && len(cerrs) == 0:
		errs = append(errs, &Error{Line: line, Msg: what + " gives no result", Help: "give `returns:` for the result of every call, `error:` for a call that fails, or `calls:` to answer particular args"})
	}
	return st, errs
}

// parseCalls reads a stub's `calls:` entries.
func (st *Func) parseCalls(n *yaml.Node) []*Error {
	n = resolve(n)
	if n.Kind != yaml.SequenceNode {
		return []*Error{{Line: n.Line, Msg: "stub " + st.Name + ": calls must be a list", Help: "list one entry per set of args, like `- {args: [ada], returns: platform}`"}}
	}
	var errs []*Error
	for i, cn := range n.Content {
		c, cerrs := parseCall(cn, fmt.Sprintf("stub %s: call %d", st.Name, i+1))
		errs = append(errs, cerrs...)
		if c != nil {
			st.Calls = append(st.Calls, c)
		}
	}
	return errs
}

// parseCall reads one entry of `calls:`; nil when it isn't an object.
func parseCall(n *yaml.Node, what string) (*Call, []*Error) {
	fields, errs := object(n, what, "a call", callKeys)
	if fields == nil {
		return nil, errs
	}
	c := &Call{Line: resolve(n).Line}
	var rerrs []*Error
	c.Returns, c.Error, rerrs = result(fields, what)
	errs = append(errs, rerrs...)
	if c.Returns != nil && c.Error != "" || c.Returns == nil && c.Error == "" && len(rerrs) == 0 {
		errs = append(errs, &Error{Line: c.Line, Msg: what + " needs exactly one of returns and error", Help: "give the result of a call with these args, or the message it fails with"})
	}
	args, ok := fields["args"]
	switch {
	case !ok:
		return c, append(errs, &Error{Line: c.Line, Msg: what + " has no args", Help: "list the args the entry answers, one per param, like `args: [ada]`"})
	case resolve(args).Kind != yaml.SequenceNode:
		return c, append(errs, &Error{Line: args.Line, Msg: what + ": args must be a list", Help: "list the args the entry answers, one per param, like `args: [ada]`"})
	}
	c.Args = make([]*Value, 0, len(resolve(args).Content))
	for _, an := range resolve(args).Content {
		v, verr := value(an, what)
		if verr != nil {
			errs = append(errs, verr)
			continue
		}
		c.Args = append(c.Args, v)
	}
	return c, errs
}

// result reads the `returns:` and `error:` of a stub or a call.
func result(fields map[string]*yaml.Node, what string) (*Value, string, []*Error) {
	var returns *Value
	var msg string
	var errs []*Error
	if n, ok := fields["returns"]; ok {
		v, err := value(n, what)
		if err != nil {
			errs = append(errs, err)
		}
		returns = v
	}
	if n, ok := fields["error"]; ok {
		n = resolve(n)
		if n.Kind != yaml.ScalarNode || null(n) || n.Value == "" {
			errs = append(errs, &Error{Line: n.Line, Msg: what + ": error must be a message", Help: "give the text the call fails with, like `error: directory unavailable`"})
		} else {
			msg = n.Value
		}
	}
	return returns, msg, errs
}

// object reads a mapping with the given keys, rejecting any other. It
// returns nil fields when n isn't a mapping.
func object(n *yaml.Node, what, article string, keys []string) (map[string]*yaml.Node, []*Error) {
	n = resolve(n)
	if n.Kind != yaml.MappingNode {
		return nil, []*Error{{Line: n.Line, Msg: fmt.Sprintf("%s must be an object with %s", what, strings.Join(keys, ", ")), Help: "like `{returns: ada}`"}}
	}
	fields := map[string]*yaml.Node{}
	var errs []*Error
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := resolve(n.Content[i])
		_, dup := fields[key.Value]
		switch {
		case key.Kind != yaml.ScalarNode || !slices.Contains(keys, key.Value):
			help := article + " takes " + strings.Join(keys, ", ")
			if near, ok := diag.Nearest(key.Value, keys); ok {
				help = fmt.Sprintf("did you mean %q? %s", near, help)
			}
			errs = append(errs, &Error{Line: key.Line, Msg: fmt.Sprintf("%s: unknown key %q", what, key.Value), Help: help})
		case dup:
			errs = append(errs, &Error{Line: key.Line, Msg: fmt.Sprintf("%s: %s is given twice", what, key.Value), Help: "give each key once"})
		default:
			fields[key.Value] = n.Content[i+1]
		}
	}
	return fields, errs
}

// value decodes a YAML value.
func value(n *yaml.Node, what string) (*Value, *Error) {
	var raw any
	if err := n.Decode(&raw); err != nil {
		return nil, &Error{Line: n.Line, Msg: what + ": " + strings.TrimPrefix(err.Error(), "yaml: "), Help: "write the value as JSON or YAML"}
	}
	return &Value{Raw: raw, Node: resolve(n), Line: resolve(n).Line}, nil
}

// byLine sorts problems by their line, keeping the order of those on
// one line, and returns them.
func byLine(errs []*Error) []*Error {
	slices.SortStableFunc(errs, func(a, b *Error) int { return cmp.Compare(a.Line, b.Line) })
	return errs
}

// untyped decodes a YAML value without a type to read it as, the way
// YAML resolves its scalars: `1.10` is a number and `2026-01-01` a
// timestamp. The node has decoded before, so it can't fail.
func untyped(n *yaml.Node) any { //nolint:emptyinterface // a decoded YAML value
	var raw any
	_ = n.Decode(&raw)
	return raw
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

func null(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }
