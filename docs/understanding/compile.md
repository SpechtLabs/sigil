---
title: How compile works
icon: mdi:package-variant-closed-check
createTime: 2026/10/01 12:00:00
permalink: /understanding/compile/
---

`sigil compile` writes a copy of the binary it runs as, with a bundle of policies inside, and the copy evaluates that bundle and nothing else. This page explains how the bundle gets into the binary, why it's stored the way it is, and where that sets limits. The flags are in [`sigil compile`](/reference/cli/#sigil-compile), and the steps in [Ship policies as a standalone binary](/guides/compile/).

## Why the bundle goes into a reserved area

Every binary built with `cli.Main`, the stock `sigil` included, reserves 1 MiB for a bundle at link time: a package-level array that starts with an eight-byte marker and is otherwise zero. Because the array is initialized with a non-zero byte, the linker puts it into the binary's data rather than into BSS, so its bytes are in the file. `compile` finds the marker, writes the encoded bundle right after it, zero-fills the rest of the area, and writes the result to `--out`. The copy is exactly as large as the binary it came from, and when it starts, it reads its own array, finds a bundle there, and builds the commands of a compiled binary instead of the stock ones.

The obvious alternative is to append the bundle to the end of the file, or to add a section for it. Both change the binary's layout: appended bytes sit outside every segment, where a loader never maps them, and a new section moves or grows the segments after it, which means rewriting load commands and offsets in three object formats. On macOS either one also invalidates the code signature as a whole. Writing into bytes that already exist changes nothing but those bytes. The segments, the load commands and the signature's place in the file stay as they were.

Building a binary per bundle with `go build` and `//go:embed` would avoid the patching, but it needs a Go toolchain and the sources of `sigil` (or of the host binary) on every machine that compiles policies. `compile` needs only the binary that's already there.

## Page hashes and ad-hoc signatures

A Mach-O binary for Apple silicon doesn't run without a valid code signature, so Go's linker signs every darwin/arm64 binary ad hoc. An ad-hoc signature holds a CodeDirectory, a list with one hash per 4 KiB page of the file, and no certificate. The kernel hashes each page as it maps it in and kills the process when a hash doesn't match. A binary that had the bundle written into it and nothing else is killed on its first run.

Since an ad-hoc signature is a pure function of the file's contents, `compile` recomputes the parts that changed: the hashes of the pages the reserved area overlaps, in every CodeDirectory the signature holds, with that CodeDirectory's own page size and hash type. Every other hash stays as it is, and so does the signature's size, so the binary is valid again, and `codesign --verify --strict` agrees.

A signature made with an identity, such as a Developer ID, is different: it signs the CodeDirectory with a private key that `compile` doesn't have. A binary signed that way can't be patched without breaking the signature, so `compile` refuses to copy one, and the identity signature goes onto the compiled binary afterwards. The same holds for an Authenticode signature on Windows. ELF binaries carry no signature over their contents, so on Linux `compile` only writes the bytes.

## Why there's a size limit

The area has to be reserved when the binary is linked, and every copy of `sigil` carries it, compiled or not. 1 MiB is a trade: about seven percent of the 14 MB binary, and room for far more policy than a binary is likely to need. The bundle is stored as JSON compressed with DEFLATE, and the four files of the example service's access policy take 1.4 KB. A bundle that doesn't fit makes `compile` fail with its size and the limit; it can't fall back to growing the area, because that would move everything after it.

## Why the binary carries sources

The bundle holds the policy files as text, the same text `sigil check` read, and the compiled binary parses, checks and compiles them every time it starts. That sounds wasteful until you look at what the alternative would store. A compiled policy is a tree of Go closures over the kind's bindings, and closures can't be serialized; a format for "compiled policies" would be a second representation of the language, with its own encoder, decoder and version number, which would have to stay in step with the checker forever.

Compiling is cheap enough that none of that pays off: a one-rule policy compiles in about 7 µs, a 64-rule one in under 300 µs, and a 512-rule one in under 3 ms (see [Compiling and checking](/reference/performance/#compiling-and-checking)). A compiled binary also never has to ask whether its policies were compiled by a different version of the engine, because the checker that compiles them at startup is the evaluator's own. The sources stay readable too: `explain` prints them back as rules, and diagnostics and traces point at the original files and lines.

The bundle's digest is computed over these sources and their names, its root and its requirements, and nothing else. The names are relative to the configuration file's directory, and the sigil version that compiled the bundle and the build time don't go in, so a repository compiles to the same digest from any directory in it and on any machine, and a deploy can check that it runs the policies that were reviewed.

## Why kinds with host functions need a host binary

A kind file carries each host function's signature, not its code. The stock `sigil` can check and explain a policy that calls `split`, and `sigil eval` can stub the call, but a compiled binary has no stubs: it's meant to make the real decision. So the stock binary refuses a bundle whose documents are written against a kind with host functions, and a host binary, built with [`cli.Main`](/reference/go-api/#package-cli) and the kind linked in, compiles it instead. Since `compile` copies the binary it runs as, the copy carries the host's real functions and its Go types.

## Why there are no cross targets yet

`compile` copies the binary it runs as, so the compiled binary runs on the platform that binary was built for: a Mac compiles Mac binaries. Writing a Linux binary from a Mac only takes a Linux `sigil` to copy, since stamping works the same for every format, but it can't be just any Linux `sigil`. The bundle was checked by the running binary, and the copy will compile and evaluate it with its own engine, so the two have to be the same build, or a bundle that checked could fail to load, or decide differently, on the target. The planned `--from` flag would read both binaries' Go build information and refuse a pair that differs; see [Cross targets for compile](/project/planned/#cross-targets-for-compile).
