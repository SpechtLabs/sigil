// Package compile implements the `sigil compile` command. It checks a
// bundle of policies exactly as `sigil check` does, then writes a copy of
// the running binary with the bundle compiled into its reserved area, so
// the copy evaluates the policies without any file. [Compiled] is the
// record it prints as JSON and YAML.
package compile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/diagnose"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/buildinfo"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/payload"
)

// shortDigest is how many hex digits of the digest the text output shows.
const shortDigest = 12

// Compiled is what compile wrote, as JSON and YAML print it.
type Compiled struct {
	Out      string              `json:"out" yaml:"out"`                               // the --out file, as given
	Root     string              `json:"root" yaml:"root"`                             // the policy eval evaluates by default; empty when the bundle holds several and --policy named none
	Digest   string              `json:"digest" yaml:"digest"`                         // the bundle's digest, as the compiled binary's version reports it
	Files    int                 `json:"files" yaml:"files"`                           // the files compiled in, kind files and trusted files included
	Bytes    int                 `json:"bytes" yaml:"bytes"`                           // the size of the encoded bundle in the binary's reserved area
	Warnings []output.Diagnostic `json:"warnings,omitempty" yaml:"warnings,omitempty"` // what the check warned about
}

// checked is a bundle that checks, ready to compile.
type checked struct {
	warnings diag.ErrorList  // what the check of the bundle found: warnings only, naming the files as they were read
	source   diag.Sources    // finds the source of a file by the name it was read under
	bundle   *payload.Bundle // the files, by the names the binary keeps, with the root and the requirements
	policies int             // how many policies the bundle holds
}

// request is what one run of compile was asked for on the command line.
type request struct {
	src      project.Sources
	out      string   // --out: the binary to write
	policy   string   // --policy: the default policy; empty for the bundle's only one
	config   string   // --config: the configuration file; the nearest one when empty
	requires []string // --require: the policies every root must invoke, instead of the configuration's
}

// NewCommand returns the compile command, configured by opts. Without
// [WithOutput] it prints text, without [WithKinds] the kinds come from
// the paths and --kind, and without [WithExecutable] it copies the
// running binary.
func NewCommand(opts ...Option) *cobra.Command {
	o := defaultOptions()
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "compile --out FILE [PATH...]",
		SuggestFor: []string{"build", "bundle", "package"},
		Short:      "Build a standalone binary with the policies compiled in",
		Long: `Checks a bundle of policies the way sigil check does, then writes a copy of
this sigil binary with the bundle compiled in to the --out file. The copy
evaluates the policies on its own: its eval, explain, test and version commands
need no policy file, kind file or configuration file.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents; with no PATH, compile reads the current directory. A directory
contributes every .sigil file below it. --kind, --config, --require and
--trusted work as they do for check, and so do the kinds:, require: and lints:
of the configuration file. A bundle with an error, including a lint set to
error, isn't compiled; warnings are printed, and compile goes on.

--policy names the policy the compiled binary's eval evaluates by default, and
narrows the bundle to it, as it narrows check: compile checks the policy and
the documents it uses, required ones included, and compiles in only the files
that hold them and the kind files of their kinds. The other paths, kind files
and trusted paths stay out, so one policy of a repository whose other kinds
need host functions compiles with the stock binary. A file goes in whole, so
any other document in it goes in too. Without --policy, the whole bundle is
checked and compiled in; a bundle with one policy has that one as its default,
and one with several has none, so the compiled eval needs --policy.

Before it writes anything, compile checks what goes into the binary once more,
on its own and every policy in it, with the same lints and requirements: the
binary's bundle passes sigil check by itself. Files are named relative to the
configuration file's directory, or to the current directory without one, so
a repository compiles to the same bundle digest from any directory in it.

The stock sigil binary implements no host functions, so it refuses a bundle
whose kinds declare any. Build a host binary with the pkg/cli package, which
links the kind and its functions in, and run its compile instead: the binary it
writes calls the host's functions.

The output runs on the platform of the binary that wrote it. On macOS, compile
updates its ad-hoc signature; a sigil signed with a Developer ID can't compile,
so compile with an unsigned or ad-hoc-signed build and sign the output
afterwards. The output records when it was compiled: SOURCE_DATE_EPOCH, when
set, or the current time. --out can't be this binary, a file compile reads, or
- for stdout.`,
		Example: `# Compile the policies in the current directory into ./policies, then
# evaluate the only policy with nothing but that binary
sigil compile --out policies
./policies eval < release.json

# Compile two teams' policies with payments.production as the default
sigil compile --out payments --policy payments.production deploy_approval.sigil deploy/ payments/

# Compile in CI, with the commit's time as the build time
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct) sigil compile --out dist/policies`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.OutOrStdout(), o, newRequest(cmd, args))
		},
	}

	addFlags(cmd)
	return cmd
}

// addFlags declares the compile command's flags.
func addFlags(cmd *cobra.Command) {
	// -o is the root's --output, so --out has no shorthand, as for export.
	cmd.Flags().String("out", "", "File to write the compiled binary to; required")
	cmd.Flags().StringP("policy", "p", "", "Policy the compiled binary evaluates by default, compiled in with only what it uses; without it, the whole bundle, and its only policy as the default")
	cmd.Flags().StringSliceP("kind", "k", nil, "Kind file the paths don't hold; the policies' kinds are found among the paths and the kinds linked in (repeatable)")
	cmd.Flags().String("config", "", "Configuration file with kind files, requirements and lint levels; the nearest "+config.Names+" when omitted")
	cmd.Flags().StringSlice("require", nil, "Policy that every root policy must invoke unconditionally, instead of the configuration's require: (repeatable)")
	cmd.Flags().StringSlice("trusted", nil, "File or directory to read the --require policies from, as policy.From does; needs --require (repeatable)")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("config", "yaml")
	_ = cmd.MarkFlagFilename("trusted", "sigil")
	_ = cmd.RegisterFlagCompletionFunc("policy", complete.Policies)
	_ = cmd.RegisterFlagCompletionFunc("require", complete.Required)
}

// newRequest reads what the command line asks for from its flags and
// args.
func newRequest(cmd *cobra.Command, args []string) request {
	out, _ := cmd.Flags().GetString("out")
	name, _ := cmd.Flags().GetString("policy")
	kindFiles, _ := cmd.Flags().GetStringSlice("kind")
	configFile, _ := cmd.Flags().GetString("config")
	requires, _ := cmd.Flags().GetStringSlice("require")
	trusted, _ := cmd.Flags().GetStringSlice("trusted")
	return request{
		src:      project.Sources{Paths: args, Trusted: trusted, Kinds: kindFiles, Stdin: cmd.InOrStdin()},
		out:      out,
		policy:   name,
		config:   configFile,
		requires: requires,
	}
}

// run checks the bundle as check does, builds its payload, writes the
// binary with the payload compiled in to req.out, and reports what it
// wrote.
func run(w io.Writer, o *options, req request) humane.Error {
	exe, built, err := prepare(o, req)
	if err != nil {
		return err
	}
	c, err := checkBundle(w, o, req)
	if err != nil {
		return err
	}
	data, err := payload.Encode(&payload.Payload{Bundle: *c.bundle, Sigil: sigilVersion(o.version), Built: built})
	if err != nil {
		return err
	}
	if err = build(exe, req.out, data); err != nil {
		return err
	}
	rec := Compiled{Out: req.out, Root: c.bundle.Root, Digest: c.bundle.Digest(), Files: files(c.bundle), Bytes: len(data)}
	return report(w, c, rec, *o.output)
}

// prepare checks what doesn't need the bundle, before anything is read:
// --out, the binary to copy, which it returns, and the build time, which
// it returns too.
func prepare(o *options, req request) (string, time.Time, humane.Error) {
	if err := checkOut(req.out); err != nil {
		return "", time.Time{}, err
	}
	exe, err := executable(o.executable, req.out)
	if err != nil {
		return "", time.Time{}, err
	}
	built, err := buildTime()
	return exe, built, err
}

// checkBundle checks the bundle as check does and returns it, ready to
// compile: with --policy, as `check --policy` does, only the root's scope,
// and the files that hold it; without, all of it. It fails when the check
// finds an error, when --out is a file it read, when --policy doesn't
// name one of its policies, when a kind of a document compiled in
// declares host functions this binary doesn't link, and when what goes
// into the binary doesn't check on its own.
func checkBundle(w io.Writer, o *options, req request) (*checked, humane.Error) {
	var patterns []string
	if req.policy != "" {
		patterns = []string{req.policy}
	}
	r, err := diagnose.Run(req.config, req.src, patterns, req.requires, o.kinds)
	if err != nil {
		return nil, err
	}
	p := r.Project
	if diags := p.Resolve(r.Errs); failed(diags) {
		return nil, fail(w, *o.output, diags, p.SourceOf, diagnose.Advice(p, diags, "each diagnostic above says where the problem is and how to fix it"))
	}
	if err = notInput(req.out, r); err != nil {
		return nil, err
	}
	root, err := pickRoot(p.Policies(), req.policy)
	if err != nil {
		return nil, err
	}
	c := everything(p, r.Files)
	if req.policy != "" {
		c = scoped(p, r.Files, root)
	}
	if err = pure(p, c, root); err != nil {
		return nil, err
	}
	return seal(w, o, r, c, root)
}

// checkOut fails for a missing --out, for `-`, since a binary never goes
// to stdout, and for one that's a directory, before anything is read.
func checkOut(out string) humane.Error {
	if out == "" || out == "-" {
		return humane.New("--out is required: compile writes a binary, and never to stdout", "name the file to write, such as `sigil compile --out policies`")
	}
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		return humane.New("--out "+out+" is a directory", "name the file to write the binary to, such as "+filepath.Join(out, "policies"))
	}
	return nil
}

// failed reports whether any diagnostic is an error.
func failed(diags diag.ErrorList) bool {
	return slices.ContainsFunc(diags, func(d *diag.Error) bool { return d.Severity == diag.SeverityError })
}

// fail reports the diagnostics of a bundle that doesn't check, which src
// finds the source lines of, and returns the error that stops the
// compile. As text, the error carries them, as eval's does; as JSON or
// YAML, they're printed as check prints them, so what parses compile's
// output parses them too.
func fail(w io.Writer, format output.Format, diags diag.ErrorList, src diag.Sources, advice []string) humane.Error {
	const msg = "the bundle doesn't check, so nothing was compiled"
	if format == output.Text {
		return pretty.Diagnose(diags, src, msg, advice...)
	}
	records := make([]output.Diagnostic, 0, len(diags))
	for _, d := range diags {
		records = append(records, output.NewDiagnostic(d))
	}
	if err := output.Encode(w, format, records); err != nil {
		return err
	}
	return pretty.Fail(msg, advice...)
}

// pure fails when a kind that one of c's documents, the documents
// compiled in, is written against declares host functions and isn't
// linked into this binary: the copy compile writes wouldn't implement
// them either. With --policy, the root's files go in whole, so a kind can
// come in with a document beside the root, which the advice names.
func pure(p *project.Project, c *contents, root string) humane.Error {
	used := map[*project.Group][]string{}
	for name := range c.docs {
		g := p.Group(name)
		used[g] = append(used[g], name)
	}
	var kinds, riders []string
	for _, g := range p.Groups() {
		k := g.Kind
		if used[g] == nil || k.Host || len(k.Model.Funcs) == 0 {
			continue
		}
		names := make([]string, len(k.Model.Funcs))
		for i, f := range k.Model.Funcs {
			names[i] = f.Name
		}
		kinds = append(kinds, fmt.Sprintf("%s@%d declares %s", k.Model.Name, k.Model.Version, strings.Join(names, ", ")))
		var outside []string
		for _, name := range used[g] {
			if c.scope != nil && !c.scope[name] {
				outside = append(outside, name)
			}
		}
		if outside != nil {
			slices.Sort(outside)
			riders = append(riders, fmt.Sprintf("%s of kind %s", strings.Join(outside, ", "), k.Model.Name))
		}
	}
	if kinds == nil {
		return nil
	}
	advice := []string{
		"build a host binary with cli.Main(cli.WithKind(...)) from the pkg/cli package, which links the kind and its functions in, and compile with its compile command",
	}
	switch {
	case c.scope == nil:
		advice = append(advice, "or name a policy of a kind without host functions with --policy, which compiles in only that policy and what it uses")
	case riders != nil:
		advice = append(advice, fmt.Sprintf("or move %s out of the files that hold %s and what it uses: those files go into the binary whole", strings.Join(riders, "; "), root))
	}
	return humane.New("the bundle needs host functions this binary doesn't implement, so nothing was compiled: "+strings.Join(kinds, "; "), advice...)
}

// pickRoot returns the policy the compiled binary evaluates by default:
// the one name names, or the bundle's only policy, or none when it holds
// several and name is empty.
func pickRoot(policies []string, name string) (string, humane.Error) {
	if name == "" && len(policies) > 1 {
		return "", nil
	}
	return project.Root(policies, name)
}

// buildTime returns when the binary was built, in UTC:
// SOURCE_DATE_EPOCH, the seconds since 1970, when it's set, so a
// reproducible build records the commit's time, and otherwise now, to
// the second.
func buildTime() (time.Time, humane.Error) {
	v := os.Getenv("SOURCE_DATE_EPOCH")
	if v == "" {
		return time.Now().UTC().Truncate(time.Second), nil
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil || secs < 0 {
		return time.Time{}, humane.New(
			fmt.Sprintf("SOURCE_DATE_EPOCH is %q, not a number of seconds since 1970", v),
			"set it to a Unix time, such as `git log -1 --format=%ct`, or unset it to record the current time",
		)
	}
	return time.Unix(secs, 0).UTC(), nil
}

// sigilVersion is the version the payload records as the sigil that
// compiled it, as `sigil version` reports it.
func sigilVersion(version string) string {
	bi, _ := debug.ReadBuildInfo()
	return buildinfo.New(version, bi).Version
}

// report prints the warnings and a line that says what compile wrote:
//
//	✓ compiled payments.production from 12 files into policies (sha256:9f2c1e3a4b5c…)
//
// As JSON or YAML it prints the record, with the warnings in it.
func report(w io.Writer, c *checked, rec Compiled, format output.Format) humane.Error {
	if format != output.Text {
		for _, d := range c.warnings {
			rec.Warnings = append(rec.Warnings, output.NewDiagnostic(d))
		}
		return output.Encode(w, format, rec)
	}
	pr := pretty.New(w)
	if err := pr.Diagnostics(c.warnings, c.source); err != nil {
		return err
	}
	what, details := rec.Root, []string(nil)
	if what == "" {
		what = fmt.Sprintf("%d policies", c.policies)
		details = []string{"no policy is the default, so its eval needs --policy"}
	}
	digest := rec.Digest
	if hex := strings.TrimPrefix(digest, "sha256:"); len(hex) > shortDigest {
		digest = "sha256:" + hex[:shortDigest] + "…"
	}
	return pr.Ok(fmt.Sprintf("compiled %s from %d %s into %s (%s)", what, rec.Files, plural(rec.Files, "file", "files"), rec.Out, digest), details...)
}

// plural returns one for n == 1, and many otherwise.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
