---
title: Test files
icon: mdi:test-tube
createTime: 2026/09/29 12:00:00
permalink: /reference/test-files/
---

The format of the YAML test files that [`sigil test`](/reference/cli/#sigil-test) and [`policytest.Run`](/reference/go-api/#package-policytest) run.

To write and run them, see [Test your policies](/guides/test-policies/).

## Format

The test file for `payments.production`, `payments/production_test.yaml`:

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

- A test file is named `*_test.yaml` or `*_test.yml`, and lives next to the policies it tests, one file per policy.
- `sigil test` finds test files in its paths recursively; `policytest.Run` in every directory of its `fs.FS`, skipping entries whose names start with `.`.

| Key | Holds |
| --- | --- |
| `policy` | The root policy the cases evaluate. Required |
| `cases` | The list of cases |
| `cases[].name` | The case's name, which `--run` matches and failures report. Required and unique in the file |
| `cases[].input` | The input, inline, in YAML; see [Inputs](#inputs) |
| `cases[].input_file` | A file holding the input; see [Inputs](#inputs) |
| `cases[].expect` | What the case expects; see [Expectations](#expectations) |

Everything a test file names is checked against the kind before anything runs. An unknown key, decision, reason or payload field is an error with a did-you-mean hint:

```text
FAIL  access/main_test.yaml
      access/main_test.yaml:9: case "a payments member reads and deploys for payments with the default ttl": decision reader has no reason "team_membr"
        = help: did you mean "team_member"? declared: team_member, everyone_in_staging
✗ 1 of 1 test files couldn't run
```

## Expectations

A case's `expect` holds exactly one of three forms.

| Form | Keys | For | Passes when |
| --- | --- | --- | --- |
| One outcome | `decision`, `reason`, optional `payload` | A `collect one` kind | The outcome is that decision with that reason, and every payload field listed has that value |
| Whole outcome | `outcome`: a list of `decision`, `reason` and optional `payload` entries | A `collect all` kind | The outcome holds exactly those entries, in any order |
| Failing asserts | `asserts`: a list of assert reasons | Either | The evaluation fails exactly those asserts |

- `decision` and `reason` are both required.
- `payload` lists the fields to compare, in the same encoding as the input, so an enum value is its name: `tier: critical`. Fields it leaves out aren't checked.
- `outcome: []` expects nothing to fire.
- Under `asserts`, any other failing assert, or none, fails the case.
- A case can't expect a [conflict](/reference/evaluation/#resolution): a conflict fails every one of the three forms, and a test file has no key that names the conflicting candidates. To test one, see [Test a conflict](/guides/test-policies/#test-a-conflict).

```yaml
expect:
  outcome:
    - decision: reader
      reason: team_member
    - decision: deployer
      reason: team_member
      payload:
        ttl: 8h
```

## Inputs

- A case has exactly one of `input` and `input_file`.
- `input` holds the input inline, in YAML.
- `input_file` names a file relative to the test file. It's decoded as YAML when its name ends in `.yaml` or `.yml`, and as JSON otherwise.
- Either way the input is decoded by the rules of [`eval`'s input documents](/reference/cli/#input-documents): an undeclared key is an error, a missing key reads as its zero value, and durations, timestamps and [enum values](/reference/types/#enums) are strings.

| Input error                | Message                                                                                          |
| -------------------------- | ------------------------------------------------------------------------------------------------ |
| `input_file` can't be read | `input_file nope.json couldn't be read`, with the help `input_file is relative to the test file` |
| `input_file` isn't valid   | `input_file owner.json isn't valid: ...`                                                         |
