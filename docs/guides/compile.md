---
title: Ship policies as a standalone binary
icon: mdi:package-variant-closed-check
createTime: 2026/10/01 12:00:00
permalink: /guides/compile/
---

`sigil compile` checks your policies and writes a copy of the `sigil` binary with them inside. The copy evaluates, explains and tests those policies with no policy file, kind file or configuration next to it, so a pipeline step, a cron job or another team can run your decision without your repository. By the end of this guide you have such a binary for the example service's access policy, a CI job that tests it, a way to tell which policies a binary carries, and a signed binary for other people's Macs.

The commands run in the example service's `examples/deploy-gates/policies/`: the two kind files at the top, the platform's documents in `platform/`, the teams' in `access/` and `teams/`, and a `sigil.yaml` that requires the guardrails.

## Compile a policy

Name the output file and the policy:

```text
$ sigil compile --out gate --policy access.main
✓ compiled access.main from 4 files into gate (sha256:555f77fa9b73…)
```

`compile` checks first, exactly as `sigil check --policy access.main` would, with the requirements and lint levels of `sigil.yaml`, and then checks what it is about to put into the binary once more, on its own, so the binary's bundle passes `sigil check` by itself. An error stops it with `nothing was compiled`; a warning is printed, and it goes on.

`--policy` picks the policy `./gate eval` evaluates, and it also narrows what goes in: `access.main`, the documents it uses, the required `access.guardrails` from `platform/access`, and the kind file `access_grant.sigil`. The deploy policies in `teams/` stay out, which is why the stock binary can compile this one at all; their kind declares a host function (see [Compile kinds with host functions](#compile-kinds-with-host-functions)). Files go in whole, so a document that shares a file with one of these goes in too, and has to check as well. Leave `--policy` out to compile every file the check reads; the compiled `eval` then needs `--policy` whenever the bundle holds more than one policy.

In CI, pass the commit's time, so the build time the binary records doesn't change between two builds of the same commit:

```sh
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct) sigil compile --out dist/gate --policy access.main
```

Every flag is in [`sigil compile`](/reference/cli/#sigil-compile).

## Run the binary

`./gate eval` takes the input with `--input` or on stdin, and evaluates `access.main`:

```text
$ ./gate eval --input access/testdata/sre.json
access.main: 1 decision
  deployer(reason: oncall)  access/main.sigil:19:3
    ttl = 2h

trace: 1 candidate
  * deployer(reason: oncall)  access/main.sigil:19:3
      when on_call
      ttl = 2h
```

The output, the exit status and `-o json` are those of `sigil eval`. The positions name the files relative to the directory of `sigil.yaml`, although the binary reads none of them. `./gate explain` lists every decision the policy can produce, and `./gate --help` shows the commands under the binary's own name. What each one takes is in [Compiled binaries](/reference/cli/#compiled-binaries).

Copy `gate` anywhere with the same operating system and architecture. It's a copy of the `sigil` that compiled it, so a Mac compiles Mac binaries, and Linux CI compiles Linux ones.

## Test it in CI

`./gate test` runs test files against the policies compiled into it, and ignores every `.sigil` file it finds, so the tests check what the binary evaluates rather than what's in the working tree. It skips the test files of policies that aren't compiled in, here the deploy policies', so it runs in a directory that holds other policies' tests too:

```text
$ ./gate test
ok    access/main_test.yaml  17 cases
skipped 2 test files for policies not compiled in
✓ 17 cases passed in 1 file
```

`-v` lists the skipped files. When none of the test files it finds is for a compiled policy, it fails, so a job that tests the wrong directory doesn't pass by testing nothing.

A job that builds the binary and tests the artifact it will ship:

```sh
set -eu
export SOURCE_DATE_EPOCH="$(git log -1 --format=%ct)"
sigil compile --out dist/gate --policy access.main
dist/gate test
```

Keep the [usual checks](/guides/ci/) next to it. `compile --policy` checks only the files that go into the binary, so a broken policy of another team, in a file of its own, doesn't stop it, and `sigil check` without `--policy` still has to pass.

## Check which policies a binary carries

`version` shows the sigil the binary is built on, and below it what was compiled in:

```text
$ ./gate version
Version:     (devel)
Commit:      unknown
Commit time: unknown
Go version:  go1.27.1
Platform:    darwin/arm64

Bundle:      sha256:555f77fa9b731b1e9fbc2084594b6a09bd7981333ad5366bd984cb23cae828fb
Root policy: access.main
Kinds:       AccessGrant@1
Files:       4
Requires:    access.guardrails for access.main
Compiled by: (devel)
Built:       2026-10-01T12:30:24Z
```

The digest after `Bundle` covers the files' names and contents, the root policy and the requirements, and nothing else. The names are relative to the directory of `sigil.yaml`, so the repository compiles to the same digest from any directory in it, on any machine and with any sigil version, while renaming a file changes it. Record it when you compile, with `sigil compile -o json`, and compare it where the binary runs:

```text
$ ./gate version -o json | jq -r .bundle.digest
sha256:555f77fa9b731b1e9fbc2084594b6a09bd7981333ad5366bd984cb23cae828fb
```

Every decision carries it too. `eval -o json` adds a `bundle` field to the record, so a log of decisions says which reviewed policies made each one:

```text
$ ./gate eval -o json --input access/testdata/sre.json | jq -r .bundle
sha256:555f77fa9b731b1e9fbc2084594b6a09bd7981333ad5366bd984cb23cae828fb
```

## Compile kinds with host functions

The deploy policies call `split`, a host function of `DeployApproval`. The stock binary has only its signature, and a compiled binary has no stubs, so it refuses:

```text
$ sigil compile --out deploy-gate --policy payments.production
Error: the bundle needs host functions this binary doesn't implement, so nothing was compiled: DeployApproval@1 declares split

What you can do
  • build a host binary with cli.Main(cli.WithKind(...)) from the pkg/cli package, which links the kind and its functions in, and compile with its compile command
```

Compile with a host binary instead. Every binary built with `cli.Main` has the `compile` command, and the binary it writes is a copy of itself, with the host's real functions and Go types. The example's host binary is `sigilc`; [Build a host binary](/guides/host-binary/) shows how to build your own:

```text
$ sigilc compile --out deploy-gate --policy payments.production
✓ compiled payments.production from 5 files into deploy-gate (sha256:f2d5dd9e09fc…)
$ ./deploy-gate eval --input teams/payments/testdata/owner.json
payments.production: review(reason: service_owner)
  approvers = ["payments-leads", "security-leads"]

trace: 1 candidate
  * review(reason: service_owner)  teams/payments/production.sigil:10:3 → platform/deploy/production.sigil:16:5
      when service.labels["compliance"] == "pci"
       and cleared
       and service.tier in [standard, internal] and owns_service
      approvers = ["payments-leads", "security-leads"]
```

Build the host binary for the platform the compiled binary will run on, since that's the platform it copies.

## Sign it for other Macs

On your own Mac there's nothing to do. Go signs every Apple silicon binary ad hoc, and `compile` updates that signature for the bytes it changed, so the binary runs and `codesign` accepts it:

```text
$ codesign --verify --strict --verbose ./gate
./gate: valid on disk
./gate: satisfies its Designated Requirement
```

An ad-hoc signature carries no identity, though, and Gatekeeper refuses one on a binary downloaded from the internet. To hand the binary to other people, sign it with your Developer ID after `compile`, and notarize it. With Apple's tools on a Mac:

```sh
codesign --force --options runtime --timestamp --identifier com.example.gate \
  --sign "Developer ID Application: Example Corp (TEAMID)" gate
zip gate.zip gate
xcrun notarytool submit gate.zip --keychain-profile notary --wait
```

Pass `--identifier` with a reverse-DNS name of your own. The binary carries the identifier Go's linker gave it, `a.out`, and `codesign --force` without `--identifier` makes one up from the file name and a hash, such as `gate-55554944a4686eb2…`, which is what Gatekeeper and the notary log then show.

With [quill](https://github.com/anchore/quill), which also runs on Linux, set the `QUILL_SIGN_*` and `QUILL_NOTARY_*` variables to your certificate and notary key, and run:

```sh
quill sign-and-notarize gate
```

Always sign after `compile`, never the `sigil` you compile with. An identity signature can only be made with your private key, so `compile` can't update one, and it refuses a `sigil` that carries one: compile with an unsigned or ad-hoc-signed `sigil`, such as a release binary or one from `go build`, and sign the output. The same goes for Authenticode on Windows. Linux binaries have no signature to keep.

Why `compile` works this way, and why it can't write a binary for another platform yet: [How compile works](/understanding/compile/).
