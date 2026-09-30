package testsuite

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/stub"
)

// Suffixes are the name endings that make a file a test file.
var Suffixes = []string{"_test.yaml", "_test.yml"}

// Suite is one test file.
type Suite struct {
	Stubs  stub.Set `yaml:"stubs"`  // the host functions every case stubs; nil for none
	File   string   `yaml:"-"`      // the file's path, which input files are read relative to
	Policy string   `yaml:"policy"` // the root policy the cases evaluate
	Cases  []*Case  `yaml:"cases"`
}

// Case is one test case.
type Case struct {
	Input     any      `yaml:"input"` //nolint:emptyinterface // the input document, as YAML decoded it
	Stubs     stub.Set `yaml:"stubs"` // the case's own stubs, each replacing the suite's for its function; nil for none
	Name      string   `yaml:"name"`
	InputFile string   `yaml:"input_file"` // a JSON or YAML input file, relative to the test file
	Expect    Expect   `yaml:"expect"`
	Line      int      `yaml:"-"` // the case's line in the test file, 0 when unknown
}

// Expect is what a case expects. Exactly one of the four forms is set:
// Decision and Reason, with an optional Payload; Outcome; Asserts; or
// Error.
type Expect struct {
	Error    *string        `yaml:"error"`   // text the runtime error's message contains; a pointer, so `error: ""` is caught
	Payload  map[string]any `yaml:"payload"` //nolint:emptyinterface // payload values, as YAML decoded them
	Outcome  *[]Entry       `yaml:"outcome"` // a pointer, so `outcome: []` expects an empty outcome
	Decision string         `yaml:"decision"`
	Reason   string         `yaml:"reason"`
	Asserts  []string       `yaml:"asserts"` // the reasons of the asserts that must fail, in any order
}

// Entry is one expected outcome entry of a collecting kind.
type Entry struct {
	Payload  map[string]any `yaml:"payload"` //nolint:emptyinterface // payload values, as YAML decoded them
	Decision string         `yaml:"decision"`
	Reason   string         `yaml:"reason"`
}

// IsTestFile reports whether name is a test file's name.
func IsTestFile(name string) bool {
	for _, s := range Suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// Parse reads a test file. Keys the format doesn't define are errors,
// so a misspelled `expect` doesn't silently expect nothing. A null case
// or a missing `policy:` is an error too. Stubs of the wrong shape (see
// [stub.Parse]) are reported together, the file's and every case's, as
// [Errors] sorted by line; any other error is an *[Error]. Parse checks
// only the file's shape; [Suite.Validate] checks it against the kind.
func Parse(file string, src []byte) (*Suite, humane.Error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	s := &Suite{File: file}
	if err := dec.Decode(s); err != nil {
		if _, ok := errors.AsType[stub.Errors](err); ok {
			// The decoder stops at the first stubs that don't parse; read
			// them all from the node tree, which parsed.
			var root yaml.Node
			_ = yaml.Unmarshal(src, &root)
			return nil, stubErrors(file, &root)
		}
		return nil, &Error{File: file, Msg: "not a valid test file: " + yamlMessage(err), Help: "a test file holds `policy:` and a list of `cases:`, each with `name`, `input` or `input_file`, and `expect`"}
	}
	for i, c := range s.Cases {
		if c == nil {
			return nil, &Error{File: file, Msg: fmt.Sprintf("case %d is null", i+1), Help: "each case must be an object with name, input or input_file, and expect"}
		}
	}
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err == nil {
		lines(&root, s)
	}
	if s.Policy == "" {
		return nil, &Error{File: file, Msg: "the test file names no policy", Help: "add `policy: <name>` for the policy its cases evaluate"}
	}
	return s, nil
}

// Validate checks the suite's expectations against the kind: declared
// decisions, reasons and payload fields, the form the kind's collect mode
// calls for, and exactly one input per case, with a unique name. Stubs,
// the suite's and each case's own, must stub host functions the kind
// declares, with args and results of their types (see [stub.Set.Bind]).
// It returns every problem found, or nil.
func (s *Suite) Validate(k *kind.Kind) []*Error {
	var errs []*Error
	var synth *gokind.Binding // what stub values are checked against, synthesized on first use
	stubs := func(set stub.Set, c *Case) {
		if len(set) == 0 {
			return
		}
		if synth == nil {
			synth = gokind.Synthesize(k)
		}
		_, serrs := set.Bind(k, synth)
		for _, e := range serrs {
			err := &Error{File: s.File, Line: e.Line, Msg: e.Msg, Help: e.Help}
			if c != nil {
				err.Case = c.Name
			}
			errs = append(errs, err)
		}
	}
	stubs(s.Stubs, nil)
	names := map[string]int{}
	for _, c := range s.Cases {
		errorf := func(help, format string, args ...any) {
			errs = append(errs, &Error{File: s.File, Line: c.Line, Case: c.Name, Msg: fmt.Sprintf(format, args...), Help: help})
		}
		if c.Name == "" {
			errorf("every case needs a name, which `--run` matches and failures report", "case has no name")
		} else if line, dup := names[c.Name]; dup {
			errorf(fmt.Sprintf("the first is on line %d; give each case its own name", line), "case %q is defined twice", c.Name)
		}
		names[c.Name] = c.Line
		if (c.Input == nil) == (c.InputFile == "") {
			errorf("give the input inline with `input:`, or name a JSON file with `input_file:`", "case needs exactly one of input and input_file")
		}
		for _, e := range c.Expect.check(k) {
			errorf(e.Help, "%s", e.Msg)
		}
		stubs(c.Stubs, c)
	}
	return errs
}

// ReadInput returns the case's input document: inline, or from its
// input_file, read relative to the test file from fsys. A file whose name
// ends in .yaml or .yml is decoded as YAML, any other as JSON with its
// numbers kept as [encoding/json.Number].
func (s *Suite) ReadInput(fsys fs.FS, c *Case) (any, *Error) { //nolint:emptyinterface // the input document, decoded
	if c.InputFile == "" {
		return c.Input, nil
	}
	name := path.Join(path.Dir(s.File), c.InputFile)
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, &Error{File: s.File, Line: c.Line, Case: c.Name, Msg: "input_file " + c.InputFile + " couldn't be read", Help: "input_file is relative to the test file"}
	}
	var raw any
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		err = yaml.Unmarshal(data, &raw)
	} else {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		err = dec.Decode(&raw)
	}
	if err != nil {
		return nil, &Error{File: s.File, Line: c.Line, Case: c.Name, Msg: "input_file " + c.InputFile + " isn't valid: " + err.Error(), Help: "an input file is a JSON object, or YAML when its name ends in .yaml"}
	}
	return raw, nil
}

// check validates one expectation against the kind.
func (e *Expect) check(k *kind.Kind) []*Error {
	var errs []*Error
	errorf := func(help, format string, args ...any) {
		errs = append(errs, &Error{Msg: fmt.Sprintf(format, args...), Help: help})
	}
	forms := 0
	if e.Decision != "" || e.Reason != "" || e.Payload != nil {
		forms++
	}
	if e.Outcome != nil {
		forms++
	}
	if e.Asserts != nil {
		forms++
	}
	if e.Error != nil {
		forms++
	}
	collect := k.Collect == kind.CollectAll
	switch {
	case forms != 1:
		errorf("expect a decision with its reason, an outcome for a collect all kind, the asserts that fail, or the runtime error the evaluation fails with", "case must expect exactly one of decision, outcome, asserts and error")
		return errs
	case e.Outcome != nil && !collect:
		errorf("a collect one kind decides once; expect `decision:` and `reason:`", "outcome is for collect all kinds, and %s collects one", k.Name)
	case e.Decision != "" && collect:
		errorf("a collect all kind returns every decision that fired; list them under `outcome:`", "%s collects all, so expect an outcome", k.Name)
	case e.Asserts != nil && len(e.Asserts) == 0:
		errorf("name the reasons of the asserts that should fail", "asserts is empty")
	case e.Error != nil && *e.Error == "":
		errorf("give text the runtime error's message contains, like `error: host function owner failed`", "error is empty")
	}
	if e.Error != nil {
		return errs
	}
	if e.Outcome == nil && e.Asserts == nil {
		errs = append(errs, entryErrors(k, Entry{Decision: e.Decision, Reason: e.Reason, Payload: e.Payload})...)
	}
	if e.Outcome != nil {
		for _, en := range *e.Outcome {
			errs = append(errs, entryErrors(k, en)...)
		}
	}
	return errs
}

// entryErrors checks that an expected entry names a declared decision,
// reason and payload fields.
func entryErrors(k *kind.Kind, en Entry) []*Error {
	names := make([]string, len(k.Decisions))
	for i, d := range k.Decisions {
		names[i] = d.Name
	}
	d := k.Decision(en.Decision)
	switch {
	case en.Decision == "":
		return []*Error{{Msg: "expected decision has no name", Help: "the kind declares: " + strings.Join(names, ", ")}}
	case d == nil:
		return []*Error{{Msg: fmt.Sprintf("the kind has no decision %q", en.Decision), Help: hint(en.Decision, names)}}
	case en.Reason == "":
		return []*Error{{Msg: fmt.Sprintf("expected %s has no reason", en.Decision), Help: "asserting the reason catches a decision made for the wrong reason; " + d.Name + " declares: " + strings.Join(d.Reasons, ", ")}}
	case !d.HasReason(en.Reason):
		return []*Error{{Msg: fmt.Sprintf("decision %s has no reason %q", d.Name, en.Reason), Help: hint(en.Reason, d.Reasons)}}
	}
	var errs []*Error
	fields := make([]string, len(d.Fields))
	for i, f := range d.Fields {
		fields[i] = f.Name
	}
	for name := range en.Payload {
		if d.Field(name) == nil {
			errs = append(errs, &Error{Msg: fmt.Sprintf("decision %s has no payload field %q", d.Name, name), Help: hint(name, fields)})
		}
	}
	return errs
}

// unknownField matches the YAML decoder's report of a key the format
// doesn't define, which names the Go type it was decoding into.
var unknownField = regexp.MustCompile(`field (\S+) not found in type testsuite\.(\w+)`)

// yamlMessage rewrites a YAML decoding error in the test file's own
// terms: `line 5: unknown key "expcet" in a case` rather than the Go
// type the decoder was filling.
func yamlMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.TrimPrefix(msg, "unmarshal errors:\n")
	msg = unknownField.ReplaceAllStringFunc(msg, func(m string) string {
		parts := unknownField.FindStringSubmatch(m)
		where := map[string]string{"Suite": "the file", "Case": "a case", "Expect": "an expect", "Entry": "an outcome entry"}[parts[2]]
		return fmt.Sprintf("unknown key %q in %s", parts[1], where)
	})
	lines := strings.Split(msg, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "; ")
}

// hint suggests the declared name nearest to name.
func hint(name string, declared []string) string {
	list := "declared: " + strings.Join(declared, ", ")
	if len(declared) == 0 {
		list = "none are declared"
	}
	if near, ok := diag.Nearest(name, declared); ok {
		return fmt.Sprintf("did you mean %q? %s", near, list)
	}
	return list
}

// stubErrors returns the problems of every stubs object in the parsed
// file, the file's and each case's, sorted by line.
func stubErrors(file string, root *yaml.Node) Errors {
	var errs Errors
	add := func(n *yaml.Node, name string) {
		_, serrs := stub.Parse(n)
		for _, e := range serrs {
			errs = append(errs, &Error{File: file, Line: e.Line, Case: name, Msg: e.Msg, Help: e.Help})
		}
	}
	if len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	add(value(doc, "stubs"), "")
	if cases := value(doc, "cases"); cases != nil {
		for _, c := range cases.Content {
			add(value(c, "stubs"), caseName(c))
		}
	}
	slices.SortStableFunc(errs, func(a, b *Error) int { return cmp.Compare(a.Line, b.Line) })
	return errs
}

// caseName returns the name a case's node gives, or "".
func caseName(c *yaml.Node) string {
	if n := value(c, "name"); n != nil {
		return n.Value
	}
	return ""
}

// value returns the value of key in a mapping node, or nil.
func value(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// lines records each case's line from the parsed node tree.
func lines(root *yaml.Node, s *Suite) {
	if len(root.Content) == 0 {
		return
	}
	doc := root.Content[0]
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "cases" {
			continue
		}
		for j, c := range doc.Content[i+1].Content {
			if j < len(s.Cases) {
				s.Cases[j].Line = c.Line
			}
		}
	}
}
