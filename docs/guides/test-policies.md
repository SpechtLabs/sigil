---
title: Test your policies
icon: mdi:test-tube
createTime: 2026/09/29 12:00:00
permalink: /guides/test-policies/
---

By the end of this guide, every decision and reason your policy can reach has a test case, the cases run with `sigil test` in the policy repository and with `go test` in the host, and a conflict the kind is meant to catch has a Go test of its own.

The examples test `payments.production` from the [tour](/getting-started/tour/#the-team-policy). The exact format of a test file is in [Test files](/reference/test-files/).

## Write test cases

Put the cases for a policy in a YAML file named `*_test.yaml` next to it, one file per policy. For `payments/production.sigil`, that's `payments/production_test.yaml`:

```yaml
policy: payments.production
cases:
  - name: an owner's deploy goes to review
    input_file: testdata/owner.json
    expect:
      decision: review
      reason: service_owner
      payload:
        approvers: [payments-leads]
  - name: a short soak is denied
    input_file: testdata/short-soak.json
    expect:
      decision: deny
      reason: soak_too_short
  - name: an unnamed actor fails the assert
    input:
      environment: production
      actor: {name: ""}
    expect:
      asserts: [named_actor]
```

`policy` names the root the cases evaluate. Each case gives an input and says what the evaluation must produce:

1. **Give the input.** Put it in a JSON file under `testdata/`, relative to the test file, and name it with `input_file`, or write it inline under `input` when it's short. A missing key reads as its zero value, and a key the kind doesn't declare fails the case with a did-you-mean hint, so a typo in a fixture can't quietly test the zero value. The decoding rules are those of [`sigil eval`](/reference/cli/#input-documents).
2. **Expect the decision and the reason.** Always name both. A deploy denied for the wrong reason is a common way a policy regression hides, and a case that checks only `decision: deny` passes right through it.
3. **Pin the payload fields that matter.** `payload` compares only the fields it lists. The first case pins `approvers`, which is what tells a non-PCI owner deploy apart from a PCI one: both are `review(service_owner)`.
4. **Expect failing asserts by their reasons.** `asserts: [named_actor]` passes only when exactly that assert fails. An input that should break an assert gets a case like the third one.

Write a case for every decision and reason the policy can reach, including the kind's default, `deny(no_rule_matched)`, for an input no rule covers. Keep each fixture close to a common one and change only what the case is about, so a failure points at one rule.

A `collect all` kind, like the example service's `AccessGrant`, returns every candidate that fired, so its cases expect the whole outcome under `outcome`, in any order:

```yaml
policy: access.main
cases:
  - name: a payments member reads and deploys for payments with the default ttl
    input_file: testdata/team-member.json
    expect:
      outcome:
        - decision: reader
          reason: team_member
        - decision: deployer
          reason: team_member
          payload:
            ttl: 8h
  - name: an actor in none of the groups gets no grants
    input_file: testdata/outsider.json
    expect:
      outcome: []
```

The case passes only if the outcome holds exactly those entries. `outcome: []` expects nothing to fire.

A case can't expect a conflict. Test one from Go, as [Test a conflict](#test-a-conflict) shows.

## Run them with sigil test

Run `sigil test` from the root of the policy repository with the exported kind file:

```text
sigil test --kind deploy_approval.sigil
```

It searches the current directory recursively, reads every `.sigil` file it finds into one bundle, and runs every test file against it. To run a subset, pass the directories, and include the ones the tested policies import: `sigil test --kind deploy_approval.sigil deploy/ payments/`. A bundle without `deploy/` fails, because `payments.production` invokes `deploy.guardrails`.

A failing case prints what it wanted and what it got. Had the second case above expected `not_eligible`:

```text
$ sigil test --kind deploy_approval.sigil
--- FAIL: payments/production_test.yaml:10: a short soak is denied
      want deny(not_eligible)
      got  deny(soak_too_short)
FAIL  payments/production_test.yaml  1 of 3 cases failed
✗ 1 of 3 test cases failed in 1 file
```

The command exits 1, so a CI step fails with it. While you work on one case, `--run 'soak'` runs only the cases whose name matches the regular expression. `-v` lists the passing cases too:

```text
$ sigil test --kind deploy_approval.sigil -v
--- PASS: payments/production_test.yaml:3: an owner's deploy goes to review
--- PASS: payments/production_test.yaml:10: a short soak is denied
--- PASS: payments/production_test.yaml:15: an unnamed actor fails the assert
ok    payments/production_test.yaml  3 cases
✓ 3 cases passed in 1 file
```

`deploy.common` calls the host function `split`, and the stock `sigil` binary has only its signature. Every case whose evaluation reaches the call fails with a runtime error instead of a decision:

```text
--- FAIL: payments/production_test.yaml:3: an owner's deploy goes to review
      want review(service_owner)
      got  a runtime error (deploy/common.sigil:5:3: host function split failed: no implementation in this sigil binary; build a host binary with split linked in (see sigil's pkg/cli))
```

Run the tests with the host team's own build of the CLI, which links the real functions in. The transcripts on this page come from one. [Build a host binary](/guides/host-binary/) shows how the host team builds it, and [`sigil test`](/reference/cli/#sigil-test) lists every flag.

## Run them from go test

The host can run the same test files from `go test`, with its own Go types and its real host functions, so it needs no separate binary. Call `policytest.Run` with the kind, the directory that holds the policies and test files, and the load options the service passes to `Load`:

```go
func TestPolicies(t *testing.T) {
	policytest.Run(t, deploy.Kind, os.DirFS("../../policies/teams"),
		policy.Require("deploy.guardrails", policy.From(os.DirFS("../../policies/platform/deploy"))))
}
```

This is the example service's test. The team policies are the bundle, and the platform's documents are the trusted source of the guardrails, as in the service. Pass the same options the service does: a test that loads without `Require` passes a team policy the service would refuse to load.

Every test file becomes a subtest named after its path, and every case a subtest of that, so `go test -run` picks them out:

```text
go test -run 'TestPolicies/payments/production_test.yaml/soak' ./internal/deploy/
```

A test file whose policy doesn't load fails its subtest, and each case that doesn't get what it expects fails its own. [Package policytest](/reference/go-api/#package-policytest) has the full API. Next to `TestPolicies`, a host usually keeps `policytest.Schema`, which fails when the exported kind file is stale; see [Keep the export current](/guides/host-binary/#keep-the-export-current).

## Test a conflict

A `*_test.yaml` case can't expect a conflict today, so a conflict the kind is meant to catch gets its test in Go, next to the host's [`policytest`](/reference/go-api/#package-policytest) run. The [example service](/guides/example-service/)'s `AccessGrant` kind declares `exclusive admin, release_manager`, and `access.main` grants both to a break-glass member who is also in `platform`. `Eval` then returns a `*policy.ConflictError`: find it with `errors.As`, and check its `Candidates`, which hold every candidate of the exclusive set's members and nothing else. The result that comes with the error has an empty outcome, as every failed evaluation of a collecting kind like `AccessGrant` does. Both tests load the bundle the way the service does:

::: tabs

@tab Go testing

```go
func TestConflicts(t *testing.T) {
	p, err := access.Kind.Load(os.DirFS("../../policies/access"), "access.main",
		policy.Require("access.guardrails", policy.From(os.DirFS("../../policies/platform/access"))))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input access.Input
		want  []string // the conflicting candidates as decision(reason), sorted
	}{
		{
			name: "break-glass in platform is admin and release_manager",
			input: access.Input{
				Actor:       access.Actor{Name: "margaret", Groups: []string{"break-glass", "platform"}},
				Team:        "payments",
				Environment: "production",
			},
			want: []string{"admin(break_glass)", "release_manager(platform_member)"},
		},
		{
			name: "grants outside the exclusive set aren't in the conflict",
			input: access.Input{
				Actor:       access.Actor{Name: "grace", Groups: []string{"break-glass", "platform", "payments"}},
				Team:        "payments",
				Environment: "production",
			},
			want: []string{"admin(break_glass)", "release_manager(platform_member)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := p.Eval(t.Context(), tt.input)

			var conflict *policy.ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("Eval() error = %v, want a *policy.ConflictError", err)
			}
			var got []string
			for _, c := range conflict.Candidates {
				got = append(got, c.Decision+"("+c.Reason+")")
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("conflicting candidates = %v, want %v", got, tt.want)
			}
			// AccessGrant collects, and a collecting kind's failed
			// evaluation has an empty outcome.
			if len(res.Outcome) != 0 {
				t.Errorf("outcome = %v, want it empty", res.Outcome)
			}
		})
	}
}
```

@tab Ginkgo

```go
var _ = Describe("access.main", func() {
	var p *policy.Policy[access.Input]

	BeforeEach(func() {
		var err error
		p, err = access.Kind.Load(os.DirFS("../../policies/access"), "access.main",
			policy.Require("access.guardrails", policy.From(os.DirFS("../../policies/platform/access"))))
		Expect(err).NotTo(HaveOccurred())
	})

	It("fails with a conflict for a break-glass member in platform", func(ctx SpecContext) {
		res, err := p.Eval(ctx, access.Input{
			Actor:       access.Actor{Name: "margaret", Groups: []string{"break-glass", "platform"}},
			Team:        "payments",
			Environment: "production",
		})

		var conflict *policy.ConflictError
		Expect(errors.As(err, &conflict)).To(BeTrue(), "Eval() error = %v, want a *policy.ConflictError", err)
		Expect(conflict.Candidates).To(ConsistOf(
			And(HaveField("Decision", "admin"), HaveField("Reason", "break_glass")),
			And(HaveField("Decision", "release_manager"), HaveField("Reason", "platform_member")),
		))
		// AccessGrant collects, and a collecting kind's failed
		// evaluation has an empty outcome.
		Expect(res.Outcome).To(BeEmpty())
	})
})
```

:::

`ConsistOf` matches in any order, as the sorted slice does in the table test. A `collect one` kind's conflict has the same shape, with the candidates tied at the top rank; there the result holds the kind's [`conflict`](/reference/kind-files/#conflict) outcome, or its `default` decision when it declares none, instead of an empty outcome. [Resolution](/reference/evaluation/#resolution) says when candidates conflict.

## Further reading

- [Test files](/reference/test-files/) is the complete format: every key, and the rules for each expectation.
- [Check policies in CI](/guides/ci/) runs these tests on every pull request.
- [Asserts and decisions](/understanding/asserts/) explains when an input deserves an assert rather than a deny.
