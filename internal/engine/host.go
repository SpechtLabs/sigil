package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/stub"
	"github.com/spechtlabs/sigil/internal/workspace"
)

var errorType = reflect.TypeFor[error]()

// hostRequest asks the host to run a host function.
type hostRequest struct {
	Function string `json:"function"`
	Args     []any  `json:"args"` //nolint:emptyinterface // args are plain values of any Sigil type
}

// protocolHelp says how the host answers a call.
const protocolHelp = `the host answers a call with {"result": ...} or {"error": "..."}`

// hostCall sends the calls of one host function to the host.
type hostCall struct {
	host    Host
	binding *gokind.Binding // the binding its args and result are Go values of
	fn      *kind.Func
	result  reflect.Type // the Go type of its result
}

// hostFailure is the error of a host function call that went wrong
// between the engine and the host, rather than in the host function: it
// says what to do about it, as sigil's other stand-ins for a host
// function do.
type hostFailure struct {
	gokind.StandIn
	msg  string
	help string
}

// bind returns the binding a policy of kind k compiles with: the kind's,
// with functions, the host functions the host implements, calling the
// host, and then the stubs replacing those they name.
func (e *Engine) bind(k *workspace.Kind, functions []string, stubs json.RawMessage) (*gokind.Binding, humane.Error) {
	b := k.Binding
	if len(functions) > 0 {
		funcs := make(map[string]reflect.Value, len(functions))
		for _, name := range functions {
			f := k.Model.Func(name)
			if f == nil {
				return nil, humane.New(fmt.Sprintf("functions: the kind %s has no host function %s", k.Model.Name, name), funcHint(k.Model, name))
			}
			funcs[name] = e.hostFunc(b, f, b.Funcs[name].Type())
		}
		b = b.WithFuncs(funcs)
	}
	if len(bytes.TrimSpace(stubs)) == 0 || bytes.Equal(bytes.TrimSpace(stubs), []byte("null")) {
		return b, nil
	}
	set, errs := stub.ParseDocument(stubs)
	if errs == nil {
		var bound *gokind.Binding
		if bound, errs = set.Bind(k.Model, b); errs == nil {
			return bound, nil
		}
	}
	problems := make([]string, len(errs))
	for i, err := range errs {
		problems[i] = "stubs: " + err.Msg + " (" + err.Help + ")"
	}
	return nil, humane.New(strings.Join(problems, "\n"), "a stub names a host function the kind declares and gives results of its type, as in a test file's stubs:")
}

// hostFunc builds a function of type ft, a host function's, whose calls
// a [hostCall] sends to the host.
func (e *Engine) hostFunc(b *gokind.Binding, f *kind.Func, ft reflect.Type) reflect.Value {
	in := make([]reflect.Type, ft.NumIn())
	for i := range in {
		in[i] = ft.In(i)
	}
	c := &hostCall{host: e.host, binding: b, fn: f, result: ft.Out(0)}
	return reflect.MakeFunc(reflect.FuncOf(in, []reflect.Type{c.result, errorType}, false), c.call)
}

// call sends one call to the host, its args as plain values, as the eval
// record prints them, and decodes the result as an input's value of the
// function's result type. Anything that goes wrong fails the call, which
// the evaluation reports as a runtime error.
func (c *hostCall) call(args []reflect.Value) []reflect.Value {
	if c.host == nil {
		return c.fail(&hostFailure{msg: "no host is attached to run it", help: "the engine runs without a host; stub " + c.fn.Name + " instead"})
	}
	req := hostRequest{Function: c.fn.Name, Args: make([]any, len(args))}
	for i, a := range args {
		req.Args[i] = workspace.Plain(c.binding.Canonical(c.fn.Params[i], a))
	}
	body, err := json.Marshal(req)
	if err != nil {
		return c.fail(&hostFailure{msg: "its args can't be sent as JSON: " + err.Error(), help: "JSON has no infinite or NaN float; keep them out of a host function's args"})
	}
	var resp map[string]any //nolint:emptyinterface // the host's answer, of any shape
	dec := json.NewDecoder(bytes.NewReader(c.host(body)))
	dec.UseNumber()
	if err := dec.Decode(&resp); err != nil {
		return c.fail(&hostFailure{msg: "the host's answer isn't a JSON object: " + err.Error(), help: protocolHelp})
	}
	if msg, ok := resp["error"]; ok {
		if s, isString := msg.(string); isString {
			return c.fail(hostError(s))
		}
		return c.fail(&hostFailure{msg: "the host answered with an error that isn't a string", help: protocolHelp})
	}
	result, ok := resp["result"]
	if !ok {
		return c.fail(&hostFailure{msg: "the host answered with neither a result nor an error", help: protocolHelp + "; a null result is {\"result\": null}"})
	}
	v := reflect.New(c.result).Elem()
	if err := c.binding.Decode(c.fn.Result, result, v, "result"); err != nil {
		return c.fail(&hostFailure{msg: "the host returned " + err.Error(), help: "the kind declares `" + c.fn.Signature() + "`; return a value of its result type"})
	}
	return []reflect.Value{v, reflect.Zero(errorType)}
}

// fail returns the results of a call that fails with err.
func (c *hostCall) fail(err error) []reflect.Value {
	return []reflect.Value{reflect.Zero(c.result), reflect.ValueOf(&err).Elem()}
}

// Error implements the error interface. It says what went wrong.
func (e *hostFailure) Error() string { return e.msg }

// Help says what to do about it, which the evaluator shows as the
// runtime error's help.
func (e *hostFailure) Help() string { return e.help }

// hostError is the error a host function failed with, as the host
// reported it.
type hostError string

// Error implements the error interface. It returns the host's message.
func (e hostError) Error() string { return string(e) }

// funcHint lists the kind's host functions, with the one nearest name.
func funcHint(k *kind.Kind, name string) string {
	names := make([]string, len(k.Funcs))
	for i, f := range k.Funcs {
		names[i] = f.Name
	}
	if len(names) == 0 {
		return "the kind declares no host functions"
	}
	list := "the kind declares: " + strings.Join(names, ", ")
	if near, ok := diag.Nearest(name, names); ok {
		return fmt.Sprintf("did you mean %q? %s", near, list)
	}
	return list
}
