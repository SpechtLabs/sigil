// Package engine is the stock sigil CLI's engine without a filesystem or
// a terminal: it takes JSON requests, runs them over virtual files, and
// answers with the records `sigil check`, `sigil eval`, `sigil explain`
// and `sigil fmt` print as JSON. cmd/sigil-wasm exports it from a
// WebAssembly module; this package holds everything but that glue, so
// it's tested natively.
//
// # Requests and responses
//
// [Engine.Call] handles one request, a JSON object whose "op" names what
// to do, and returns one response, a JSON object whose "ok" says whether
// the op succeeded. A request's optional numeric "id" is echoed back.
//
//	{"op": "compile", "id": 7, "files": [{"path": "kind.sigil", "source": "..."}, ...]}
//	{"ok": true, "id": 7, "handle": 1, "policy": "access.main", "diagnostics": []}
//
// A failed op answers with an error, and with the diagnostics that
// stopped it when there are any:
//
//	{"ok": false, "error": {"message": "...", "help": "..."}, "diagnostics": [...]}
//
// The ops:
//
//	version  → the build, as `sigil version -o json` prints it
//	check    {files, trusted_files?, policies?, require?, lints?} → {diagnostics}
//	compile  {files, trusted_files?, policy?, require?, stubs?, functions?} → {handle, policy, diagnostics}
//	eval     {handle, input, timeout_ms?} → the `sigil eval -o json` record
//	explain  {handle} or {files, trusted_files?, policy?} → {explanations}
//	format   {source, path?} → {source, formatted}
//	release  {handle} → {}
//
// Files are virtual: a path, used in positions as the CLI prints it for
// the same relative path, and a source. They play the part of the CLI's
// paths, in the order given, and hold the kind documents too, as a kind
// file among the paths does. Every request is JSON today; a request that
// doesn't start with `{` is reserved for another encoding, such as CBOR,
// which would answer in kind.
//
// trusted_files are the host's own documents, such as a platform's
// guardrails: they load as the trusted source policy.From reads, so no
// other document may take one of their names, or one of their paths.
// check's require is the configuration file's, {policy, trusted?,
// roots?}, with trusted paths among all the files. compile's require is
// [{policy}], the host's policy.Require: the compiled policy must invoke
// each unconditionally, and with trusted_files each must be one of
// theirs, as policy.From(trusted) requires.
//
// # Compiled policies
//
// compile returns a handle, an opaque number, to the compiled policy,
// which stays in the engine until release; eval and explain take the
// handle, so per evaluation only the input and the result cross the
// boundary. An evaluation is safe to run while others do. eval's
// timeout_ms bounds it: past the deadline it stops, and the result is
// the fallback with an error of kind "canceled". A failed evaluation,
// by a runtime error, a conflict, a failing assert or the deadline, is
// still ok: its record says why, as the CLI's does. So is a check that
// finds errors: they're its diagnostics.
//
// # Host functions
//
// A kind file declares its host functions' signatures. compile's
// functions names those the host implements: a call to one becomes a
// request to the host, through [WithHost], of the form
//
//	{"function": "split", "args": ["a,b", ","]}
//
// which the host answers with {"result": ...} or {"error": "..."}. Args
// are plain values, as the eval record prints them: durations in Sigil's
// syntax, 1h30m, timestamps as RFC 3339 strings, enum values as their
// names, structs as objects. A result is read as an input's value of the
// function's result type. An error, or an answer that breaks these rules,
// fails the call with a runtime error. stubs answers calls without the
// host, in the format of a test file's `stubs:`, and replaces a host
// function of the same name. A call to a function neither implements
// fails the way it does in the stock sigil binary.
package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/buildinfo"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// malformedHelp says what a request is, in the answer to one that isn't.
const malformedHelp = `send one JSON object of an op and its fields, such as {"op": "version"}`

// Engine handles requests. Its methods are safe for concurrent use.
type Engine struct {
	host      Host
	now       func() time.Time // the clock an evaluation's deadline is read from
	buildInfo *debug.BuildInfo
	handles   *handles
	tag       string // the release version the version op reports
}

// Host runs a host function: it takes a request, {"function", "args"},
// and returns {"result"} or {"error"}.
type Host func(req []byte) []byte

// request is every field any op reads. Each op reads its own and ignores
// the rest; a field no op knows is an error, so a typo doesn't pass for a
// default.
type request struct {
	ID        *json.Number      `json:"id"`
	Input     any               `json:"input"` //nolint:emptyinterface // a JSON document, its numbers kept as json.Number
	Stubs     json.RawMessage   `json:"stubs"`
	Source    *string           `json:"source"`
	Lints     map[string]string `json:"lints"`
	Op        string            `json:"op"`
	Policy    string            `json:"policy"`
	Path      string            `json:"path"`
	Files     []File            `json:"files"`
	Trusted   []File            `json:"trusted_files"` // read as trusted, as policy.From reads its source
	Policies  []string          `json:"policies"`
	Require   []Requirement     `json:"require"`
	Functions []string          `json:"functions"`
	TimeoutMS int64             `json:"timeout_ms"`
	Handle    uint32            `json:"handle"`
}

// File is one virtual file.
type File struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// Requirement is one policy check enforces, as an entry of the
// configuration file's require: lists it.
type Requirement struct {
	Policy  string   `json:"policy"`
	Trusted []string `json:"trusted"` // paths the policy must come from: files, or directories they're below
	Roots   []string `json:"roots"`   // name patterns of the policies it applies to
}

// envelope starts every response.
type envelope struct {
	ID *json.Number `json:"id,omitempty"`
	OK bool         `json:"ok"`
}

// failure is the response of a failed op.
type failure struct {
	envelope
	Error       problem                `json:"error"`
	Diagnostics []workspace.Diagnostic `json:"diagnostics,omitempty"`
}

// problem is why an op failed.
type problem struct {
	Message string `json:"message"`
	Help    string `json:"help,omitempty"`
}

// blocked is the error of an op stopped by diagnostics: the bundle
// doesn't check, the policy doesn't compile, or a source doesn't parse.
type blocked struct {
	msg    string
	advice []string
	diags  []workspace.Diagnostic
}

// New returns an engine configured by opts.
func New(opts ...Option) *Engine {
	e := &Engine{now: time.Now, handles: newHandles()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		e.buildInfo = bi
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Call handles one request and returns its response. It never fails:
// every problem, even a request that isn't JSON, is a response with ok
// false. So is a panic, a bug in sigil, which would otherwise stop a
// WebAssembly module for good and lose every handle in it.
func (e *Engine) Call(req []byte) (resp []byte) {
	// The id is read on its own first, so a request that names a field no
	// op takes, or gives one the wrong type, still gets its id back; only
	// a request that isn't JSON at all, or whose id isn't a number, has
	// none to echo.
	var head struct {
		ID *json.Number `json:"id"`
	}
	if err := json.Unmarshal(req, &head); err != nil {
		head.ID = nil // an id that isn't a number is left behind half decoded
	}
	defer func() {
		if p := recover(); p != nil {
			resp = encode(fail(head.ID, humane.New(fmt.Sprintf("sigil failed while handling the request: %v", p), "this is a bug in sigil; please report it with the request")))
		}
	}()
	var r request
	dec := json.NewDecoder(bytes.NewReader(req))
	dec.DisallowUnknownFields()
	dec.UseNumber() // an input's numbers, as sigil eval reads them
	if err := dec.Decode(&r); err != nil {
		return encode(fail(head.ID, humane.New("the request isn't a JSON object of the fields an op takes: "+err.Error(), malformedHelp)))
	}
	env := envelope{ID: r.ID, OK: true}
	var out any //nolint:emptyinterface // each op answers with its own record
	var err humane.Error
	switch r.Op {
	case "version":
		out = e.version(env)
	case "check":
		out, err = e.check(env, &r)
	case "compile":
		out, err = e.compile(env, &r)
	case "eval":
		out, err = e.evaluate(env, &r)
	case "explain":
		out, err = e.explain(env, &r)
	case "format":
		out, err = e.format(env, &r)
	case "release":
		out, err = e.release(env, &r)
	default:
		err = humane.New("unknown op "+strconv.Quote(r.Op), "the ops are version, check, compile, eval, explain, format and release")
	}
	if err != nil {
		return encode(fail(r.ID, err))
	}
	return encode(out)
}

// Malformed is the response to a request the engine couldn't read, for
// the glue to answer with when a request doesn't reach [Engine.Call].
func Malformed(msg string) []byte {
	return encode(fail(nil, humane.New(msg, malformedHelp)))
}

// HostMisbehaved is the response of a host function whose host broke the
// protocol, for the glue to answer a host function call with; the call
// then fails with msg.
func HostMisbehaved(msg string) []byte {
	b, _ := json.Marshal(map[string]string{"error": msg}) //nolint:errchkjson // a map of strings always encodes
	return b
}

// version answers the version op.
func (e *Engine) version(env envelope) any { //nolint:emptyinterface // each op answers with its own record
	return struct {
		envelope
		buildinfo.Info
	}{env, buildinfo.New(e.tag, e.buildInfo)}
}

// release answers the release op: the handle's policy is dropped, and the
// handle is invalid from then on.
func (e *Engine) release(env envelope, r *request) (any, humane.Error) { //nolint:emptyinterface // each op answers with its own record
	if err := e.handles.drop(r.Handle); err != nil {
		return nil, err
	}
	return env, nil
}

// fail is the response for err, with the diagnostics that caused it.
func fail(id *json.Number, err humane.Error) failure {
	return failure{ID: id, Error: problem{Message: err.Error(), Help: joinAdvice(err.Advice())}, Diagnostics: diagnosticsOf(err)}
}

// joinAdvice joins pieces of advice into one help string: after one that
// ends a sentence, such as a did-you-mean question, with a space, and
// otherwise with a semicolon, as the pieces are clauses of one sentence.
func joinAdvice(advice []string) string {
	var b strings.Builder
	for i, a := range advice {
		if i > 0 {
			if strings.HasSuffix(advice[i-1], "?") || strings.HasSuffix(advice[i-1], ".") {
				b.WriteString(" ")
			} else {
				b.WriteString("; ")
			}
		}
		b.WriteString(a)
	}
	return b.String()
}

// diagnosticsOf returns the diagnostics that caused err, or nil.
func diagnosticsOf(err humane.Error) []workspace.Diagnostic {
	if b, ok := errors.AsType[*blocked](err); ok {
		return b.diags
	}
	return nil
}

// encode encodes a response. A response is built of strings, numbers,
// maps and slices, which always encode; the canonical values of a
// payload, too.
func encode(v any) []byte { //nolint:emptyinterface // any response
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(fail(nil, humane.Wrap(err, "the response couldn't be encoded", "this is a bug in sigil, please report it"))) //nolint:errchkjson // a failure is strings only
	}
	return b
}

// Error implements the error interface. It returns what the diagnostics
// stopped.
func (b *blocked) Error() string { return b.msg }

// Display implements [humane.Error]. It returns the message and the
// advice; the diagnostics travel as records.
func (b *blocked) Display() string { return humane.New(b.msg, b.advice...).Display() }

// Advice implements [humane.Error]. It returns what to do about it.
func (b *blocked) Advice() []string { return b.advice }

// Cause implements [humane.Error]. It returns nil: the diagnostics are
// the cause, and they're records.
func (b *blocked) Cause() error { return nil } //nolint:humaneerror // humane.Error fixes the signature
