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
- `sigil test` finds test files below its paths, the way every command reads [directories](/reference/cli/#inputs); `policytest.Run` in every directory of its `fs.FS`, skipping entries whose names start with `.`.

| Key | Holds |
| --- | --- |
| `policy` | The root policy the cases evaluate. Required |
| `stubs` | Host functions every case stubs; see [Stubs](#stubs) |
| `cases` | The list of cases |
| `cases[].name` | The case's name, which `--run` matches and failures report. Required and unique in the file |
| `cases[].input` | The input, inline, in YAML; see [Inputs](#inputs) |
| `cases[].input_file` | A file holding the input; see [Inputs](#inputs) |
| `cases[].expect` | What the case expects; see [Expectations](#expectations) |
| `cases[].stubs` | The case's own stubs, each replacing the file's stub of the same function; see [Stubs](#stubs) |

Everything a test file names is checked against the kind before anything runs, its [stubs](#stubs) included. An unknown key, decision, reason or payload field is an error with a did-you-mean hint:

```text
FAIL  access/main_test.yaml
      access/main_test.yaml:9: case "a payments member reads and deploys for payments with the default ttl": decision reader has no reason "team_membr"
        = help: did you mean "team_member"? declared: team_member, everyone_in_staging
✗ 1 of 1 test files couldn't run
```

## Expectations

A case's `expect` holds exactly one of four forms.

| Form | Keys | For | Passes when |
| --- | --- | --- | --- |
| One outcome | `decision`, `reason`, optional `payload` | A `collect one` kind | The outcome is that decision with that reason, and every payload field listed has that value |
| Whole outcome | `outcome`: a list of `decision`, `reason` and optional `payload` entries | A `collect all` kind | The outcome holds exactly those entries, in any order |
| Failing asserts | `asserts`: a list of assert reasons | Either | The evaluation fails exactly those asserts |
| Runtime error | `error`: text | Either | The evaluation fails with a runtime error whose message contains the text |

- `decision` and `reason` are both required.
- `payload` lists the fields to compare, in the same encoding as the input, so an enum value is its name: `tier: critical`. Fields it leaves out aren't checked.
- `outcome: []` expects nothing to fire.
- Under `asserts`, any other failing assert, or none, fails the case.
- `error` matches the runtime error's message without its position, case-sensitively: `error: region directory unavailable` matches `host function split failed: region directory unavailable`. It's how a case tests a [stub](#stubs) that fails.
- A case can't expect a [conflict](/reference/evaluation/#resolution): a conflict fails every one of the four forms, and a test file has no key that names the conflicting candidates. To test one, see [Test a conflict](/guides/test-policies/#test-a-conflict).

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

## Stubs

A stub replaces a host function with results the test file gives, so a case runs in a binary that doesn't link the function, or pins one whose result changes from run to run.

```yaml
policy: payments.production
stubs:
  split:
    calls:
      - args: ["eu,us", ","]
        returns: [eu, us]
      - args: ["eu,us,ap", ","]
        returns: [eu, us, ap]
cases:
  - name: an owner's deploy goes to review
    input_file: testdata/owner.json
    expect:
      decision: review
      reason: service_owner
  - name: a failing region lookup fails the evaluation
    input_file: testdata/owner.json
    stubs:
      split:
        error: region directory unavailable
    expect:
      error: region directory unavailable
```

| Key | Holds |
| --- | --- |
| `<name>` | The stub of the host function `<name>` the kind declares |
| `<name>.returns` | The result of a call no entry of `calls` matches |
| `<name>.error` | The message a call no entry of `calls` matches fails with |
| `<name>.calls` | Results for particular args, matched in order; the first match wins |
| `<name>.calls[].args` | The args the entry answers, one per param |
| `<name>.calls[].returns` | The result of a call with those args |
| `<name>.calls[].error` | The message a call with those args fails with |

- A stub gives exactly one of `returns` and `error`, or neither when it has `calls`. Each entry of `calls` gives `args` and exactly one of `returns` and `error`.
- Args are compared as values: an entry matches a call whose every arg equals the entry's, lists in order and structs field by field.
- Values are written as in [inputs](#inputs): durations and timestamps are strings, and a struct is an object with its fields. Where the type is a `string`, a value is the text as written, so an unquoted `2026-01-01` or `1.10` is a string there, unlike in an input.
- `null`, or `--stub NAME=` with nothing after it, is a value only where the type is an optional, a list or a map.
- A call no entry matches, to a stub without `returns` or `error`, is a runtime error that names the call: `no stubbed call matches split("eu", ",")`.
- `error` fails the call as the host function returning that error would: a runtime error, `host function split failed: region directory unavailable`, which a case expects with [`error`](#expectations).
- A case's `stubs` replace the file's per function: a function the case stubs gets the case's stub, whole, and the others keep the file's.
- A stub replaces the function even where the real one is linked: in a [host binary](/reference/cli/#host-functions-and-host-binaries) and in `policytest.Run`.
- `sigil eval` takes the same stubs from a file with `--stubs`, and one at a time with `--stub`; see [`sigil eval`](/reference/cli/#sigil-eval).

Every stub is checked against the kind before anything runs:

| Problem | Message |
| --- | --- |
| A function the kind doesn't declare | `the kind has no host function splt to stub`, with a did-you-mean |
| An entry with the wrong number of args | `stub split: call 1 passes 1 arg, but split takes 2`, with the signature |
| A value that doesn't fit its type | `stub split: returns: expected a list<string>, found a string` |
| A stub with both or neither of `returns` and `error` | `stub split gives both returns and error`, `stub split gives no result` |

```text
FAIL  teams/payments/stubs_test.yaml
      teams/payments/stubs_test.yaml:3: the kind has no host function splt to stub
        = help: did you mean "split"? the kind declares: split
      teams/payments/stubs_test.yaml:11: case "a split with one arg": stub split: call 1 passes 1 arg, but split takes 2
        = help: the kind declares `fn split(string, string) -> list<string>`
✗ 1 of 1 test files couldn't run
```
