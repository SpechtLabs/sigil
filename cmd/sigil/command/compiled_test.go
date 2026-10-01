package command

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/payload"
)

// compiled is the bundle under testdata, as sigil compile reads it: the
// kind file, the teams' policies, and the platform's guardrails, which
// the teams' policies are required to invoke, as trusted.
const compiled = "testdata/compiled"

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestCompiled runs the commands of a binary with the testdata bundle
// compiled in, and compares what each prints, and the error it fails
// with, with the golden files under testdata/compiled/golden.
func TestCompiled(t *testing.T) {
	const (
		owner    = compiled + "/inputs/owner.json"
		unnamed  = compiled + "/inputs/unnamed.json"
		root     = "payments.production"
		freshYML = "actor: {name: ada, teams: [payments, sre]}\nsoak: 1h\n"
	)
	tests := []struct {
		name  string
		root  string // the bundle's root policy
		args  []string
		stdin string
	}{
		{name: "eval", root: root, args: []string{"eval", "--input", owner}},
		{name: "eval_json", root: root, args: []string{"eval", "-o", "json", "-i", owner}},
		{name: "eval_yaml", root: root, args: []string{"eval", "-o", "yaml", "-i", owner}},
		{name: "eval_stdin", root: root, args: []string{"eval"}, stdin: freshYML},
		{name: "eval_stdin_dash", root: root, args: []string{"evaluate", "-i", "-"}, stdin: freshYML},
		{name: "eval_policy", root: root, args: []string{"eval", "--policy", "checkout.production", "-i", owner}},
		{name: "eval_assert", root: root, args: []string{"eval", "-i", unnamed}},
		{name: "eval_assert_json", root: root, args: []string{"eval", "-o", "json", "-i", unnamed}},
		{name: "eval_no_root", args: []string{"eval", "-i", owner}},
		{name: "eval_no_root_policy", args: []string{"eval", "-p", root, "-i", owner}},
		{name: "eval_unknown_policy", root: root, args: []string{"eval", "-p", "payments.staging", "-i", owner}},
		{name: "eval_trusted_policy", root: root, args: []string{"eval", "-p", "deploy.guardrails", "-i", owner}},
		{name: "eval_paths", root: root, args: []string{"eval", owner}},
		{name: "eval_bad_input", root: root, args: []string{"eval"}, stdin: `{"actor": {"nam": "ada"}}`},
		{name: "eval_missing_input", root: root, args: []string{"eval", "-i", compiled + "/inputs/nope.json"}},
		{name: "explain", root: root, args: []string{"explain"}},
		{name: "explain_json", root: root, args: []string{"explain", "-o", "json"}},
		{name: "explain_pattern", root: root, args: []string{"explain", "--policy", "*.production"}},
		{name: "explain_no_root", args: []string{"explain"}},
		{name: "explain_unknown_policy", root: root, args: []string{"explain", "-p", "nope.*"}},
		{name: "explain_paths", root: root, args: []string{"explain", "teams/"}},
		{name: "test", root: root, args: []string{"test", compiled + "/tests"}},
		{name: "test_verbose", root: root, args: []string{"test", "-v", compiled + "/tests"}},
		{name: "test_json", root: root, args: []string{"test", "-o", "json", compiled + "/tests/payments_test.yaml"}},
		{name: "test_run", root: root, args: []string{"test", "-v", "--run", "soak", compiled + "/tests"}},
		{name: "test_failing", root: root, args: []string{"test", compiled + "/failing"}},
		{name: "test_ignores_sigil_files", root: root, args: []string{"test", "-v", compiled + "/stray"}},
		{name: "test_skips_other_policies", root: root, args: []string{"test", compiled + "/mixed"}},
		{name: "test_skips_other_policies_verbose", root: root, args: []string{"test", "-v", compiled + "/mixed"}},
		{name: "test_skips_other_policies_json", root: root, args: []string{"test", "-o", "json", compiled + "/mixed"}},
		{name: "test_skips_other_policies_yaml", root: root, args: []string{"test", "-o", "yaml", compiled + "/mixed/staging_test.yaml", compiled + "/mixed/payments_test.yaml"}},
		{name: "test_only_other_policies", root: root, args: []string{"test", compiled + "/mixed/staging_test.yaml", compiled + "/mixed/release_test.yaml"}},
		{name: "test_other_policy_broken", root: root, args: []string{"test", "-v", compiled + "/foreign"}},
		{name: "test_no_test_files", root: root, args: []string{"test", compiled + "/inputs"}},
		{name: "test_invalid_run", root: root, args: []string{"test", "--run", "(", compiled + "/tests"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(WithPayload(compiledPayload(t, tt.root)))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			golden(t, tt.name, render(out.String(), err))
		})
	}
}

// TestCompiledTree checks what a compiled binary holds: the commands that
// work on its bundle, named after the binary, and none of the commands
// that read policy files. Help names the root policy and the bundle's
// digest.
func TestCompiledTree(t *testing.T) {
	p := compiledPayload(t, "payments.production")
	name := filepath.Base(os.Args[0])
	tests := []struct {
		name   string
		opts   []Option
		listed []string // in the root's help
		absent []string
	}{
		{
			name: "compiled",
			opts: []Option{WithPayload(p)},
			listed: []string{
				"Evaluates payments.production, a Sigil policy compiled into this binary",
				p.Bundle.Digest(), name + " version describes",
				"POLICY COMMANDS", "eval", "explain", "test [PATH...]", "OTHER COMMANDS", "version",
				name + " eval < input.json",
			},
			absent: []string{"check [PATH", "compile --out", "fmt [PATH", "export", "lsp", "breaking", "gen ", "--policy NAME", "--kind"},
		},
		{
			name:   "compiled without a root",
			opts:   []Option{WithPayload(&payload.Payload{Bundle: payload.Bundle{Paths: p.Bundle.Paths}})},
			listed: []string{"Evaluates the Sigil policies compiled into this binary", name + " eval --policy NAME < input.json"},
		},
		{
			name:   "a nil payload builds the stock tree",
			opts:   []Option{WithPayload(nil)},
			listed: []string{"Sigil evaluates host-provided input", "check [PATH...]", "compile --out FILE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(tt.opts...)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			if code := Execute(context.Background(), cmd, []string{"--color=never", "--help"}); code != 0 {
				t.Fatalf("Execute() = %d", code)
			}
			help := out.String()
			for _, s := range tt.listed {
				if !strings.Contains(help, s) {
					t.Errorf("help doesn't contain %q:\n%s", s, help)
				}
			}
			for _, s := range tt.absent {
				if strings.Contains(help, s) {
					t.Errorf("help contains %q:\n%s", s, help)
				}
			}
		})
	}
}

// TestCompiledCommandsAreDocumented holds the compiled binary's commands
// to the stock ones' standard: each has a short and long description and
// examples, which call the binary by its name, and a help group.
func TestCompiledCommandsAreDocumented(t *testing.T) {
	for _, root := range []string{"payments.production", ""} {
		cmd := NewCommand(WithPayload(compiledPayload(t, root)))
		for _, c := range cmd.Commands() {
			t.Run(root+"/"+c.Name(), func(t *testing.T) {
				if c.Short == "" || c.Long == "" || c.GroupID == "" {
					t.Errorf("Short = %q, Long = %q, GroupID = %q, want them all", c.Short, c.Long, c.GroupID)
				}
				if !strings.Contains(c.Example, cmd.Name()+" "+c.Name()) {
					t.Errorf("Example doesn't call %s %s:\n%s", cmd.Name(), c.Name(), c.Example)
				}
				if strings.Contains(c.Example, "sigil ") {
					t.Errorf("Example calls sigil, not the binary:\n%s", c.Example)
				}
			})
		}
	}
}

// TestCompiledFlags checks that the compiled commands take none of the
// flags that point a stock command at policy files.
func TestCompiledFlags(t *testing.T) {
	tests := []struct {
		args    []string
		wantErr string
	}{
		{args: []string{"eval", "--kind", "k.sigil"}, wantErr: "unknown flag: --kind"},
		{args: []string{"eval", "--config", "sigil.yaml"}, wantErr: "unknown flag: --config"},
		{args: []string{"eval", "--stub", "split=[]"}, wantErr: "unknown flag: --stub"},
		{args: []string{"explain", "-k", "k.sigil"}, wantErr: "unknown shorthand flag: 'k'"},
		{args: []string{"explain", "--config", "sigil.yaml"}, wantErr: "unknown flag: --config"},
		{args: []string{"test", "--kind", "k.sigil"}, wantErr: "unknown flag: --kind"},
		{args: []string{"test", "--config", "sigil.yaml"}, wantErr: "unknown flag: --config"},
		{args: []string{"version", "extra"}, wantErr: "version takes no arguments, got 1"},
		{args: []string{"check"}, wantErr: `unknown command "check"`},
		{args: []string{"compile", "--out", "x"}, wantErr: `unknown command "compile"`},
		{args: []string{"chek"}, wantErr: `unknown command "chek"`},
		{args: []string{"flatten"}, wantErr: "Did you mean this?\n\texplain"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := NewCommand(WithPayload(compiledPayload(t, "payments.production")))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute() = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestCompiledCompletion checks that --policy completes the compiled
// policies, trusted ones apart, and nothing from the disk.
func TestCompiledCompletion(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"eval", "--policy", ""}, want: "checkout.production\npayments.production\n:4\n"},
		{args: []string{"eval", "--policy", "pay"}, want: "payments.production\n:4\n"},
		{args: []string{"explain", "-p", "c"}, want: "checkout.production\n:4\n"},
		{args: []string{"explain", "--policy", "x"}, want: ":4\n"},
		{args: []string{"eval", ""}, want: ":4\n"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := NewCommand(WithPayload(compiledPayload(t, "")))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(append([]string{cobra.ShellCompRequestCmd}, tt.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() = %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("completion = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCompiledBundleDoesNotLoad checks that every compiled command
// reports a bundle that doesn't load, here a kind file that doesn't match
// the kind linked into the binary.
func TestCompiledBundleDoesNotLoad(t *testing.T) {
	stale := strings.Replace(hostAccess.Schema(), "version 1", "version 2", 1)
	p := &payload.Payload{Bundle: payload.Bundle{Kinds: []payload.File{{Name: "access.sigil", Source: stale}}}}
	for _, args := range [][]string{
		{"eval", "-i", compiled + "/inputs/owner.json"},
		{"explain"},
		{"test", compiled + "/tests"},
		{"version"},
	} {
		t.Run(args[0], func(t *testing.T) {
			cmd := NewCommand(WithKind(hostAccess), WithPayload(p))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "doesn't match the kind Access linked into this binary") {
				t.Fatalf("Execute() = %v, want the stale kind file reported", err)
			}
		})
	}
}

// TestCompiledHostBinary runs a host binary's compiled output: the kind
// is linked in, not in the bundle, and eval and test call its real host
// functions.
func TestCompiledHostBinary(t *testing.T) {
	f, err := project.Read(project.Sources{Paths: []string{"test/testdata/access"}})
	if err != nil {
		t.Fatal(err)
	}
	b := f.Bundle()
	b.Root = "access.main"
	p := roundTrip(t, &payload.Payload{Bundle: *b, Sigil: "1.2.0"})
	tests := []struct {
		args  []string
		stdin string
		want  string
	}{
		{args: []string{"eval"}, stdin: `{"user": {"name": "ada"}, "resource": "vault"}`, want: "access.main: allow(reason: team_member)\n  ttl = 15m"},
		{args: []string{"test", "-v", "../../../pkg/policytest/testdata/access"}, want: "--- PASS: ../../../pkg/policytest/testdata/access/main_test.yaml:38: vault owners get fifteen minutes"},
		{args: []string{"version", "-o", "json"}, want: `"kinds":["Access@1"],"files":1,"compiledBy":"1.2.0"}`},
	}
	for _, tt := range tests {
		t.Run(tt.args[0], func(t *testing.T) {
			cmd := NewCommand(WithKind(hostAccess), WithPayload(p))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() = %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("output doesn't contain %q:\n%s", tt.want, out.String())
			}
		})
	}
}

// TestUnreadablePayload checks that when the binary's payload doesn't
// decode, because it's damaged or in a newer format, every command fails
// and says why, each piece of advice once, while help still works. The
// error as Execute prints it is compared with the golden files.
func TestUnreadablePayload(t *testing.T) {
	data, err := payload.Encode(compiledPayload(t, "payments.production"))
	if err != nil {
		t.Fatal(err)
	}
	damaged := bytes.Clone(data)
	damaged[len(damaged)-1] ^= 0xff
	newer := bytes.Clone(data)
	newer[0] = 99 // the format version
	tests := []struct {
		name string
		data []byte
		msg  string // the error's message
	}{
		{name: "payload_damaged", data: damaged, msg: "this binary's compiled policies are damaged"},
		{name: "payload_newer", data: newer, msg: "this binary can't read the policies compiled into it"},
	}
	for _, tt := range tests {
		unreadable := func(o *options) {
			o.embedded = func() (*payload.Payload, humane.Error) { return payload.Decode(tt.data) }
		}
		for _, args := range [][]string{
			{"eval", "-i", compiled + "/inputs/owner.json"},
			{"eval", "some.json"},
			{"explain"},
			{"test", compiled + "/tests"},
			{"version", "-o", "json"},
		} {
			t.Run(tt.name+"/"+strings.Join(args, " "), func(t *testing.T) {
				cmd := NewCommand(unreadable)
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&bytes.Buffer{})
				cmd.SetArgs(args)
				if err := cmd.Execute(); err == nil || err.Error() != tt.msg {
					t.Fatalf("Execute() = %v, want %q", err, tt.msg)
				}
				if out.Len() != 0 {
					t.Errorf("output = %q, want none", out.String())
				}
			})
		}
		t.Run(tt.name+"/rendered", func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "")
			cmd := NewCommand(unreadable)
			var errOut bytes.Buffer
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&errOut)
			if code := Execute(context.Background(), cmd, []string{"--color=never", "explain"}); code != 1 {
				t.Errorf("Execute() = %d, want 1", code)
			}
			golden(t, tt.name, errOut.String())
		})
		t.Run(tt.name+"/help", func(t *testing.T) {
			cmd := NewCommand(unreadable)
			var out bytes.Buffer
			cmd.SetOut(&out)
			if code := Execute(context.Background(), cmd, []string{"--color=never", "--help"}); code != 0 || !strings.Contains(out.String(), "can't be read, so every command fails") {
				t.Errorf("Execute(--help) = %d:\n%s", code, out.String())
			}
		})
	}
}

// compiledPayload returns the payload sigil compile writes for the
// testdata bundle with root as its root policy, as the binary reads it
// back: encoded and decoded.
func compiledPayload(t *testing.T, root string) *payload.Payload {
	t.Helper()
	f, err := project.Read(project.Sources{
		Kinds:   []string{compiled + "/deploy.sigil"},
		Paths:   []string{compiled + "/teams"},
		Trusted: []string{compiled + "/platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := f.Bundle()
	b.Root = root
	b.Require = []payload.Requirement{{Policy: "deploy.guardrails", Trusted: []string{compiled + "/platform"}, Roots: []string{"payments.*", "checkout.*"}}}
	return roundTrip(t, &payload.Payload{Bundle: *b, Sigil: "1.2.0", Built: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)})
}

// roundTrip encodes p and decodes it again, as a compiled binary reads
// it.
func roundTrip(t *testing.T, p *payload.Payload) *payload.Payload {
	t.Helper()
	data, err := payload.Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := payload.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// render joins what the command printed and the error it returned, with
// a humane error's advice.
func render(out string, err error) string {
	if err == nil {
		return out + "--- ok ---\n"
	}
	s := out + "--- error ---\n" + err.Error() + "\n"
	if herr, ok := errors.AsType[humane.Error](err); ok {
		for _, a := range herr.Advice() {
			s += "advice: " + a + "\n"
		}
	}
	return s
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(compiled, "golden", name+".golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
