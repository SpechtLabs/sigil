# Changelog

## [0.2.0](https://github.com/SpechtLabs/sigil/compare/v0.7.2...v0.2.0) (2026-10-05)


### ⚠ BREAKING CHANGES

* **rust:** precompiled module, background pool rebuilds, no wasmtime-wasi, typed host functions ([#149](https://github.com/SpechtLabs/sigil/issues/149))
* **wasm:** build sigil as a WebAssembly module ([#98](https://github.com/SpechtLabs/sigil/issues/98))
* **cli:** read every path the same way, from . by default ([#84](https://github.com/SpechtLabs/sigil/issues/84))
* add enum types and make the decision reason a labeled field ([#82](https://github.com/SpechtLabs/sigil/issues/82))
* **policy:** let a collect one kind name the outcome of a conflict ([#78](https://github.com/SpechtLabs/sigil/issues/78))
* **policy:** WithReasonPrecedence and WithDefault take reason handles from Decision.Reason instead of a decision and reason strings, and Decision.Reason panics on a reason the decision does not declare.
* add a filter expression for lists ([#56](https://github.com/SpechtLabs/sigil/issues/56))
* **kind:** reject tag options on input fields and accepts below 1
* **explain:** name every document by its full name and count modules separately

### Features

* add a filter expression for lists ([#56](https://github.com/SpechtLabs/sigil/issues/56)) ([b5b724e](https://github.com/SpechtLabs/sigil/commit/b5b724ec2417046253299e57dc6d800d33a4e42c))
* add enum types and make the decision reason a labeled field ([#82](https://github.com/SpechtLabs/sigil/issues/82)) ([3f00ca4](https://github.com/SpechtLabs/sigil/commit/3f00ca48dc72ffa10a64c67bf2ccd08292bdfa7f))
* **build:** write Sigil modules and policies in Go, served as trusted vocabulary ([#132](https://github.com/SpechtLabs/sigil/issues/132)) ([e08ffc8](https://github.com/SpechtLabs/sigil/commit/e08ffc8553814949e7c71ea3145c1ccefff69708))
* **cli:** clean up help, fmt output, check --policy and eval input ([#85](https://github.com/SpechtLabs/sigil/issues/85)) ([89103d5](https://github.com/SpechtLabs/sigil/commit/89103d5cbfd3a5e185a1bcc234ac8c8b701ca4d3))
* **cli:** find the configuration as sigil or .sigil in YAML, JSON or TOML, and publish its schema ([#89](https://github.com/SpechtLabs/sigil/issues/89)) ([c6e110d](https://github.com/SpechtLabs/sigil/commit/c6e110d9388d1628b0726d19c3919f0c9cca1f66))
* **cli:** find the kind among the inputs ([#83](https://github.com/SpechtLabs/sigil/issues/83)) ([6ff3f27](https://github.com/SpechtLabs/sigil/commit/6ff3f27b09215cf448fc24dafefafc9d903ecdee))
* **cli:** honor --output in every command ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **cli:** polish every command's output for a terminal ([#38](https://github.com/SpechtLabs/sigil/issues/38)) ([0417435](https://github.com/SpechtLabs/sigil/commit/041743536154cc087db275746a040641194ae83e))
* **cli:** read every path the same way, from . by default ([#84](https://github.com/SpechtLabs/sigil/issues/84)) ([e1c1a47](https://github.com/SpechtLabs/sigil/commit/e1c1a472527a85cf445d423a3b21c830b22566b2))
* **cli:** read kinds and requirements from sigil.yaml ([#86](https://github.com/SpechtLabs/sigil/issues/86)) ([8772b10](https://github.com/SpechtLabs/sigil/commit/8772b10aa215baed44bfe4f8038797b03346843b))
* **cli:** scaffold the sigil command surface with styled output ([f627868](https://github.com/SpechtLabs/sigil/commit/f62786850ccbfe49165b420afe7d475a903c5c3c))
* **cli:** sigil compile builds a standalone binary from a policy bundle ([#119](https://github.com/SpechtLabs/sigil/issues/119)) ([cdec7d0](https://github.com/SpechtLabs/sigil/commit/cdec7d053d87efcb5ef7faa571ec439f290849bb))
* **cli:** stub host functions in eval and test ([#87](https://github.com/SpechtLabs/sigil/issues/87)) ([ab8592f](https://github.com/SpechtLabs/sigil/commit/ab8592fa5e7bf0111b278de0953c8f4eeb4ae7e6))
* **devtool:** keep the fuzz corpus on a fuzz-corpus branch ([#164](https://github.com/SpechtLabs/sigil/issues/164)) ([7ec9d28](https://github.com/SpechtLabs/sigil/commit/7ec9d285dd467a2317023f91cebd2c78f0e24e1a))
* **docs:** a Sigil playground that runs the engine in the browser ([#127](https://github.com/SpechtLabs/sigil/issues/127)) ([4109c71](https://github.com/SpechtLabs/sigil/commit/4109c7179ffa27ca774143d7ecabaf3c26201d0c))
* **eval:** rules, decisions, asserts and the trace ([#27](https://github.com/SpechtLabs/sigil/issues/27)) ([5f6fdf9](https://github.com/SpechtLabs/sigil/commit/5f6fdf9b5cb1c074e940bc5452de0746ea271356))
* **examples:** alertrouter, a TypeScript service on Sigil's WebAssembly build ([#107](https://github.com/SpechtLabs/sigil/issues/107)) ([ed674df](https://github.com/SpechtLabs/sigil/commit/ed674df50ff2caca6ac31a0632571296297de3b8))
* **examples:** deploygate, a Go host service with both kinds, an LGTM stack and ginkgo suites ([#46](https://github.com/SpechtLabs/sigil/issues/46)) ([6e728fe](https://github.com/SpechtLabs/sigil/commit/6e728fe4cef3a24c35287d768b0164f9ffd73b52))
* **examples:** featuregate, an OFREP feature-flag service in Rust on the Sigil crate ([#111](https://github.com/SpechtLabs/sigil/issues/111)) ([bd544ba](https://github.com/SpechtLabs/sigil/commit/bd544ba20fb6dc09778dc54c216d582116b008ea))
* **examples:** k6 load tests, continuous profiling and a demo CLI for deploygate ([#49](https://github.com/SpechtLabs/sigil/issues/49)) ([ab85250](https://github.com/SpechtLabs/sigil/commit/ab8525044253b8c2ae0daca1d29ef2ec0ff176d1))
* **lang:** declared reasons, exclusive outcomes and the resolution rule ([#29](https://github.com/SpechtLabs/sigil/issues/29)) ([3ddb36e](https://github.com/SpechtLabs/sigil/commit/3ddb36e9a42fabc8655ba3f7b81b73f92bc7f0c5))
* **lang:** imports, policy invocation and multi-document bundles ([#8](https://github.com/SpechtLabs/sigil/issues/8)) ([e0af09e](https://github.com/SpechtLabs/sigil/commit/e0af09e94235b82fb4becdda1ea5da181892a2ff))
* **lang:** modules, imports, invocation, required policies and the bundle loader ([#33](https://github.com/SpechtLabs/sigil/issues/33)) ([45bb9a2](https://github.com/SpechtLabs/sigil/commit/45bb9a2261a9f79d8db29e8f342acdd5c31d368a))
* **lang:** pin kind versions, has-only map keys, pin-aware names ([#21](https://github.com/SpechtLabs/sigil/issues/21)) ([7247e21](https://github.com/SpechtLabs/sigil/commit/7247e2191d32507977e8212676946e9ee4d9d410))
* **parser:** lexer, Pratt expression parser and document parser ([#17](https://github.com/SpechtLabs/sigil/issues/17)) ([07f10f1](https://github.com/SpechtLabs/sigil/commit/07f10f14a3d72093bc983e9f63414e5977763cf4))
* **policy:** let a collect one kind name the outcome of a conflict ([#78](https://github.com/SpechtLabs/sigil/issues/78)) ([776a2a2](https://github.com/SpechtLabs/sigil/commit/776a2a2dfdd1c7b36ed7dc42e785cd264e3c3c85))
* **policy:** name reasons through typed handles in Go ([#65](https://github.com/SpechtLabs/sigil/issues/65)) ([957b308](https://github.com/SpechtLabs/sigil/commit/957b308f99880a2dd0698c5bd0c39d9b3b121a17))
* **policy:** stop evaluations on context cancellation, recover host panics on request ([#73](https://github.com/SpechtLabs/sigil/issues/73)) ([0b0dac9](https://github.com/SpechtLabs/sigil/commit/0b0dac9d93b57f85b028481f8d6ea0beaaed3e27))
* **release:** publish sigil to the homebrew tap ([#42](https://github.com/SpechtLabs/sigil/issues/42)) ([0dbacdc](https://github.com/SpechtLabs/sigil/commit/0dbacdc88ebfd39e727134cda3472f8c7d8b48e5))
* **rust:** add spechtlabs-sigil, Rust bindings for the WebAssembly module ([#110](https://github.com/SpechtLabs/sigil/issues/110)) ([44dc458](https://github.com/SpechtLabs/sigil/commit/44dc45873c84e927152406f5d7fcc0e850e5c6d6))
* **rust:** precompiled module, background pool rebuilds, no wasmtime-wasi, typed host functions ([#149](https://github.com/SpechtLabs/sigil/issues/149)) ([9222be3](https://github.com/SpechtLabs/sigil/commit/9222be39e06903a15fdb5cae2f381dac3aea1031))
* **rust:** run test files with Sigil::test ([#126](https://github.com/SpechtLabs/sigil/issues/126)) ([f9a6b11](https://github.com/SpechtLabs/sigil/commit/f9a6b1136885238ed046ba8886af96ade966658c))
* **stamp:** patch a payload into a binary's reserved area, the groundwork for sigil compile ([#118](https://github.com/SpechtLabs/sigil/issues/118)) ([59282a5](https://github.com/SpechtLabs/sigil/commit/59282a56680885cbe339f2e7b1e5110a70de4b41))
* **tooling:** sigil fmt, check, eval, test and export, with lints and policytest ([#35](https://github.com/SpechtLabs/sigil/issues/35)) ([3c033f6](https://github.com/SpechtLabs/sigil/commit/3c033f603d87fd52ddec3d94ff11fe2060b1dccd))
* **typescript:** run test files with Sigil.test ([#125](https://github.com/SpechtLabs/sigil/issues/125)) ([3ed6b4c](https://github.com/SpechtLabs/sigil/commit/3ed6b4caee15b2f4ca9a91364e5a473d2b8549b2))
* **types:** kinds, type checker and expression evaluator ([#18](https://github.com/SpechtLabs/sigil/issues/18)) ([95896bb](https://github.com/SpechtLabs/sigil/commit/95896bb84f4280012f9d9e01e49c1e8a9a09b3f1))
* **wasm:** build sigil as a WebAssembly module ([#98](https://github.com/SpechtLabs/sigil/issues/98)) ([d5bd546](https://github.com/SpechtLabs/sigil/commit/d5bd5469a0f948523b699f15d40f825678480bb7))
* **wasm:** run test files in the WebAssembly module with a test op ([607fa47](https://github.com/SpechtLabs/sigil/commit/607fa47c6a4d19fe1738368ca670e2ad44a60bf4))


### Bug Fixes

* **check:** reject decisions as map values ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **check:** report `==` and `!=` on lists instead of failing to infer an empty literal ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **check:** suggest the quoted string for a bare map key ([#63](https://github.com/SpechtLabs/sigil/issues/63)) ([07cf05f](https://github.com/SpechtLabs/sigil/commit/07cf05fd97e6d2c027da2b9f501d18d68643e9d4))
* **cli:** point an unbound host function at the host's own binary ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **deps:** update docs dependencies (major) ([#99](https://github.com/SpechtLabs/sigil/issues/99)) ([7446bab](https://github.com/SpechtLabs/sigil/commit/7446bab95b47c7f21b5346395fef7fb9c8c98e69))
* **deps:** update module github.com/spf13/cobra to v1.10.2 ([#13](https://github.com/SpechtLabs/sigil/issues/13)) ([882ced4](https://github.com/SpechtLabs/sigil/commit/882ced41ad41deede85335e3ebd334205618901a))
* **deps:** update module github.com/spf13/pflag to v1.0.10 ([#40](https://github.com/SpechtLabs/sigil/issues/40)) ([566f8eb](https://github.com/SpechtLabs/sigil/commit/566f8eb602b7418eccd1c5864812399782891399))
* **deps:** update rust crates ([#141](https://github.com/SpechtLabs/sigil/issues/141)) ([8c6b45f](https://github.com/SpechtLabs/sigil/commit/8c6b45fdc18f32734ef0c1b2136b387dd66f66ce))
* **deps:** update rust crates to 49.0.1 ([#131](https://github.com/SpechtLabs/sigil/issues/131)) ([74aa0cb](https://github.com/SpechtLabs/sigil/commit/74aa0cb67def4f6c1b4e65d4b53baa2066f8283e))
* **devtool:** correct the examples in devtool --help ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **eval:** don't panic on an invocation argument for a param with a single bound ([#90](https://github.com/SpechtLabs/sigil/issues/90)) ([dc526b0](https://github.com/SpechtLabs/sigil/commit/dc526b058a07ac7b90b95ca3532ebc121c6603fe))
* **explain:** name every document by its full name and count modules separately ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **format:** keep a comment after a call's opening paren trailing it ([#162](https://github.com/SpechtLabs/sigil/issues/162)) ([fa30d8b](https://github.com/SpechtLabs/sigil/commit/fa30d8b491680385ab883098fa0e4353080e6e74))
* **gokind:** keep Sigil names out of the types Synthesize builds ([#178](https://github.com/SpechtLabs/sigil/issues/178)) ([6b08e5e](https://github.com/SpechtLabs/sigil/commit/6b08e5eae4f714410b02251e22d315a18a437cc6))
* **kind:** reject tag options on input fields and accepts below 1 ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **lexer:** explain hex, exponent and separator notation in number literals ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **parser:** limit expression nesting depth ([#105](https://github.com/SpechtLabs/sigil/issues/105)) ([cc036b0](https://github.com/SpechtLabs/sigil/commit/cc036b098452e3f662cd2623c495ce4c6c1ef5f6))
* **policy:** accept a param bound from Go when it declares only min or max ([#61](https://github.com/SpechtLabs/sigil/issues/61)) ([e6fecec](https://github.com/SpechtLabs/sigil/commit/e6fecece04a0299aaa84f53f529723bf1eaee4f0))
* **release:** sign the checksum file and check go.mod is tidy in CI ([#36](https://github.com/SpechtLabs/sigil/issues/36)) ([6eec9a3](https://github.com/SpechtLabs/sigil/commit/6eec9a3200f10d171d0f3542553f4a9169ebd6ae))
* **renovate:** keep the toolchain directive in go.mod ([#16](https://github.com/SpechtLabs/sigil/issues/16)) ([0f8b82d](https://github.com/SpechtLabs/sigil/commit/0f8b82d5fa83aacf91050db53cb6d9a07a4309b8))
* **stamp:** refuse a reserved area that overlaps the Mach-O code signature ([#163](https://github.com/SpechtLabs/sigil/issues/163)) ([a9c7044](https://github.com/SpechtLabs/sigil/commit/a9c70448dbfa382d97d0aabe765877d81631380a))
* **test:** print a test file's cases as an empty list, not null, when it can't run ([607fa47](https://github.com/SpechtLabs/sigil/commit/607fa47c6a4d19fe1738368ca670e2ad44a60bf4))
* **typescript:** start the worker helper in browsers, where the first message arrived before anyone listened ([#123](https://github.com/SpechtLabs/sigil/issues/123)) ([97aa4da](https://github.com/SpechtLabs/sigil/commit/97aa4daf51c7a9aa51133ea7326cbc654e6e352f))
* **wasm:** check every document on compile, and echo the request id on every error ([#101](https://github.com/SpechtLabs/sigil/issues/101)) ([b4ecb47](https://github.com/SpechtLabs/sigil/commit/b4ecb478fb1bdbfdfff6f41b8e95bbfa25f5810a))


### Performance Improvements

* **eval:** fold candidates without comparing every pair deeply ([#67](https://github.com/SpechtLabs/sigil/issues/67)) ([31353bd](https://github.com/SpechtLabs/sigil/commit/31353bd0062ffdabc7c86c8ce550b1cd63e87e76))
* **eval:** reduce allocations and gate benchmark regressions ([#48](https://github.com/SpechtLabs/sigil/issues/48)) ([01d9f72](https://github.com/SpechtLabs/sigil/commit/01d9f72d6a0d1281451b27f1ec73aa9ab0965e7a))
* **result:** don't box typed payloads for trace candidates ([#77](https://github.com/SpechtLabs/sigil/issues/77)) ([4580123](https://github.com/SpechtLabs/sigil/commit/4580123c6c46a7f48f29ed12c5b0ec6b4b45f1d8))


### Miscellaneous Chores

* go mod tidy ([3294c42](https://github.com/SpechtLabs/sigil/commit/3294c426286228d4bc6077bb78100c5017469a7d))

## [0.7.2](https://github.com/SpechtLabs/sigil/compare/v0.7.1...v0.7.2) (2026-10-05)


### Features

* **devtool:** keep the fuzz corpus on a fuzz-corpus branch ([#164](https://github.com/SpechtLabs/sigil/issues/164)) ([76f5b61](https://github.com/SpechtLabs/sigil/commit/76f5b614c2c64b717b1efa3827ec21afa2e49739))


### Bug Fixes

* **format:** keep a comment after a call's opening paren trailing it ([#162](https://github.com/SpechtLabs/sigil/issues/162)) ([645e03c](https://github.com/SpechtLabs/sigil/commit/645e03c25b5d5500023bf8abab678db6da3ae1d3))
* **gokind:** keep Sigil names out of the types Synthesize builds ([#178](https://github.com/SpechtLabs/sigil/issues/178)) ([31a4a31](https://github.com/SpechtLabs/sigil/commit/31a4a31f26393f129385e5083364b3305da69871))
* **stamp:** refuse a reserved area that overlaps the Mach-O code signature ([#163](https://github.com/SpechtLabs/sigil/issues/163)) ([910fd3c](https://github.com/SpechtLabs/sigil/commit/910fd3cd88cbc42ab1fd57425034c6aedf6e16a4))

## [0.7.1](https://github.com/SpechtLabs/sigil/compare/v0.7.0...v0.7.1) (2026-10-04)


### Bug Fixes

* **deps:** update dependency @spechtlabs/sigil to v0.7.0 ([#154](https://github.com/SpechtLabs/sigil/issues/154)) ([f2958d5](https://github.com/SpechtLabs/sigil/commit/f2958d57450c08a07fc583d81a9456884ed1b8d3))
* **deps:** update docs dependencies (major) ([#99](https://github.com/SpechtLabs/sigil/issues/99)) ([7446bab](https://github.com/SpechtLabs/sigil/commit/7446bab95b47c7f21b5346395fef7fb9c8c98e69))

## [0.7.0](https://github.com/SpechtLabs/sigil/compare/v0.6.4...v0.7.0) (2026-10-04)


### ⚠ BREAKING CHANGES

* **rust:** precompiled module, background pool rebuilds, no wasmtime-wasi, typed host functions ([#149](https://github.com/SpechtLabs/sigil/issues/149))

### Features

* **build:** write Sigil modules and policies in Go, served as trusted vocabulary ([#132](https://github.com/SpechtLabs/sigil/issues/132)) ([e08ffc8](https://github.com/SpechtLabs/sigil/commit/e08ffc8553814949e7c71ea3145c1ccefff69708))
* **docs:** run test files in the playground ([#128](https://github.com/SpechtLabs/sigil/issues/128)) ([39a51c3](https://github.com/SpechtLabs/sigil/commit/39a51c3b4e77fa70d160f1c14296effe38d2ad2f))
* **rust:** precompiled module, background pool rebuilds, no wasmtime-wasi, typed host functions ([#149](https://github.com/SpechtLabs/sigil/issues/149)) ([9222be3](https://github.com/SpechtLabs/sigil/commit/9222be39e06903a15fdb5cae2f381dac3aea1031))


### Bug Fixes

* **deps:** update dependency @spechtlabs/sigil to v0.6.4 ([#135](https://github.com/SpechtLabs/sigil/issues/135)) ([166fc0c](https://github.com/SpechtLabs/sigil/commit/166fc0cabe8c16c2ae79038d18f46ddd5ffc5a3e))
* **deps:** update rust crates ([#141](https://github.com/SpechtLabs/sigil/issues/141)) ([8c6b45f](https://github.com/SpechtLabs/sigil/commit/8c6b45fdc18f32734ef0c1b2136b387dd66f66ce))

## [0.6.4](https://github.com/SpechtLabs/sigil/compare/v0.6.3...v0.6.4) (2026-10-01)


### Features

* **cli:** sigil compile builds a standalone binary from a policy bundle ([#119](https://github.com/SpechtLabs/sigil/issues/119)) ([cdec7d0](https://github.com/SpechtLabs/sigil/commit/cdec7d053d87efcb5ef7faa571ec439f290849bb))
* **docs:** a Sigil playground that runs the engine in the browser ([#127](https://github.com/SpechtLabs/sigil/issues/127)) ([4109c71](https://github.com/SpechtLabs/sigil/commit/4109c7179ffa27ca774143d7ecabaf3c26201d0c))
* **rust:** run test files with Sigil::test ([#126](https://github.com/SpechtLabs/sigil/issues/126)) ([f9a6b11](https://github.com/SpechtLabs/sigil/commit/f9a6b1136885238ed046ba8886af96ade966658c))
* **stamp:** patch a payload into a binary's reserved area, the groundwork for sigil compile ([#118](https://github.com/SpechtLabs/sigil/issues/118)) ([59282a5](https://github.com/SpechtLabs/sigil/commit/59282a56680885cbe339f2e7b1e5110a70de4b41))
* **typescript:** run test files with Sigil.test ([#125](https://github.com/SpechtLabs/sigil/issues/125)) ([3ed6b4c](https://github.com/SpechtLabs/sigil/commit/3ed6b4caee15b2f4ca9a91364e5a473d2b8549b2))
* **wasm:** run test files in the WebAssembly module with a test op ([607fa47](https://github.com/SpechtLabs/sigil/commit/607fa47c6a4d19fe1738368ca670e2ad44a60bf4))


### Bug Fixes

* **deps:** update rust crates to 49.0.1 ([#131](https://github.com/SpechtLabs/sigil/issues/131)) ([74aa0cb](https://github.com/SpechtLabs/sigil/commit/74aa0cb67def4f6c1b4e65d4b53baa2066f8283e))
* **test:** print a test file's cases as an empty list, not null, when it can't run ([607fa47](https://github.com/SpechtLabs/sigil/commit/607fa47c6a4d19fe1738368ca670e2ad44a60bf4))
* **typescript:** start the worker helper in browsers, where the first message arrived before anyone listened ([#123](https://github.com/SpechtLabs/sigil/issues/123)) ([97aa4da](https://github.com/SpechtLabs/sigil/commit/97aa4daf51c7a9aa51133ea7326cbc654e6e352f))

## [0.6.3](https://github.com/SpechtLabs/sigil/compare/v0.6.2...v0.6.3) (2026-10-01)


### Features

* **examples:** featuregate, an OFREP feature-flag service in Rust on the Sigil crate ([#111](https://github.com/SpechtLabs/sigil/issues/111)) ([bd544ba](https://github.com/SpechtLabs/sigil/commit/bd544ba20fb6dc09778dc54c216d582116b008ea))
* **rust:** add spechtlabs-sigil, Rust bindings for the WebAssembly module ([#110](https://github.com/SpechtLabs/sigil/issues/110)) ([44dc458](https://github.com/SpechtLabs/sigil/commit/44dc45873c84e927152406f5d7fcc0e850e5c6d6))

## [0.6.2](https://github.com/SpechtLabs/sigil/compare/v0.6.1...v0.6.2) (2026-09-30)


### Features

* **examples:** alertrouter, a TypeScript service on Sigil's WebAssembly build ([#107](https://github.com/SpechtLabs/sigil/issues/107)) ([ed674df](https://github.com/SpechtLabs/sigil/commit/ed674df50ff2caca6ac31a0632571296297de3b8))


### Bug Fixes

* **wasm:** check every document on compile, and echo the request id on every error ([#101](https://github.com/SpechtLabs/sigil/issues/101)) ([b4ecb47](https://github.com/SpechtLabs/sigil/commit/b4ecb478fb1bdbfdfff6f41b8e95bbfa25f5810a))

## [0.6.1](https://github.com/SpechtLabs/sigil/compare/v0.6.0...v0.6.1) (2026-09-30)


### Bug Fixes

* **parser:** limit expression nesting depth ([#105](https://github.com/SpechtLabs/sigil/issues/105)) ([cc036b0](https://github.com/SpechtLabs/sigil/commit/cc036b098452e3f662cd2623c495ce4c6c1ef5f6))

## [0.6.0](https://github.com/SpechtLabs/sigil/compare/v0.5.2...v0.6.0) (2026-09-30)


### ⚠ BREAKING CHANGES

* **wasm:** build sigil as a WebAssembly module ([#98](https://github.com/SpechtLabs/sigil/issues/98))

### Features

* **wasm:** build sigil as a WebAssembly module ([#98](https://github.com/SpechtLabs/sigil/issues/98)) ([d5bd546](https://github.com/SpechtLabs/sigil/commit/d5bd5469a0f948523b699f15d40f825678480bb7))

## [0.5.2](https://github.com/SpechtLabs/sigil/compare/v0.5.1...v0.5.2) (2026-09-30)


### Bug Fixes

* **examples:** log trace and span IDs once on deploygate's failure lines ([#95](https://github.com/SpechtLabs/sigil/issues/95)) ([f9832dc](https://github.com/SpechtLabs/sigil/commit/f9832dc3eeb76550a02c1c636f9d0061e671fbaa))

## [0.5.1](https://github.com/SpechtLabs/sigil/compare/v0.5.0...v0.5.1) (2026-09-30)


### Bug Fixes

* **docs:** Add Releases to docs page ([7641013](https://github.com/SpechtLabs/sigil/commit/7641013fae4fed671de8b81bb2c8b2372c747036))
* **eval:** don't panic on an invocation argument for a param with a single bound ([#90](https://github.com/SpechtLabs/sigil/issues/90)) ([dc526b0](https://github.com/SpechtLabs/sigil/commit/dc526b058a07ac7b90b95ca3532ebc121c6603fe))

## [0.5.0](https://github.com/SpechtLabs/sigil/compare/v0.4.0...v0.5.0) (2026-09-30)


### ⚠ BREAKING CHANGES

* **cli:** read every path the same way, from . by default ([#84](https://github.com/SpechtLabs/sigil/issues/84))
* add enum types and make the decision reason a labeled field ([#82](https://github.com/SpechtLabs/sigil/issues/82))
* **policy:** let a collect one kind name the outcome of a conflict ([#78](https://github.com/SpechtLabs/sigil/issues/78))
* **policy:** WithReasonPrecedence and WithDefault take reason handles from Decision.Reason instead of a decision and reason strings, and Decision.Reason panics on a reason the decision does not declare.

### Features

* add enum types and make the decision reason a labeled field ([#82](https://github.com/SpechtLabs/sigil/issues/82)) ([3f00ca4](https://github.com/SpechtLabs/sigil/commit/3f00ca48dc72ffa10a64c67bf2ccd08292bdfa7f))
* **cli:** clean up help, fmt output, check --policy and eval input ([#85](https://github.com/SpechtLabs/sigil/issues/85)) ([89103d5](https://github.com/SpechtLabs/sigil/commit/89103d5cbfd3a5e185a1bcc234ac8c8b701ca4d3))
* **cli:** find the configuration as sigil or .sigil in YAML, JSON or TOML, and publish its schema ([#89](https://github.com/SpechtLabs/sigil/issues/89)) ([c6e110d](https://github.com/SpechtLabs/sigil/commit/c6e110d9388d1628b0726d19c3919f0c9cca1f66))
* **cli:** find the kind among the inputs ([#83](https://github.com/SpechtLabs/sigil/issues/83)) ([6ff3f27](https://github.com/SpechtLabs/sigil/commit/6ff3f27b09215cf448fc24dafefafc9d903ecdee))
* **cli:** read every path the same way, from . by default ([#84](https://github.com/SpechtLabs/sigil/issues/84)) ([e1c1a47](https://github.com/SpechtLabs/sigil/commit/e1c1a472527a85cf445d423a3b21c830b22566b2))
* **cli:** read kinds and requirements from sigil.yaml ([#86](https://github.com/SpechtLabs/sigil/issues/86)) ([8772b10](https://github.com/SpechtLabs/sigil/commit/8772b10aa215baed44bfe4f8038797b03346843b))
* **cli:** stub host functions in eval and test ([#87](https://github.com/SpechtLabs/sigil/issues/87)) ([ab8592f](https://github.com/SpechtLabs/sigil/commit/ab8592fa5e7bf0111b278de0953c8f4eeb4ae7e6))
* **policy:** let a collect one kind name the outcome of a conflict ([#78](https://github.com/SpechtLabs/sigil/issues/78)) ([776a2a2](https://github.com/SpechtLabs/sigil/commit/776a2a2dfdd1c7b36ed7dc42e785cd264e3c3c85))
* **policy:** name reasons through typed handles in Go ([#65](https://github.com/SpechtLabs/sigil/issues/65)) ([957b308](https://github.com/SpechtLabs/sigil/commit/957b308f99880a2dd0698c5bd0c39d9b3b121a17))
* **policy:** stop evaluations on context cancellation, recover host panics on request ([#73](https://github.com/SpechtLabs/sigil/issues/73)) ([0b0dac9](https://github.com/SpechtLabs/sigil/commit/0b0dac9d93b57f85b028481f8d6ea0beaaed3e27))


### Bug Fixes

* **check:** suggest the quoted string for a bare map key ([#63](https://github.com/SpechtLabs/sigil/issues/63)) ([07cf05f](https://github.com/SpechtLabs/sigil/commit/07cf05fd97e6d2c027da2b9f501d18d68643e9d4))
* **examples:** answer a policy's failure with 500 and the caller's with 422 ([#69](https://github.com/SpechtLabs/sigil/issues/69)) ([12c4043](https://github.com/SpechtLabs/sigil/commit/12c4043d24126665771ffbf5e10859037a74f8b0))
* **examples:** classify failures by assert phase, bound evaluation time, recover host panics ([#76](https://github.com/SpechtLabs/sigil/issues/76)) ([a85742a](https://github.com/SpechtLabs/sigil/commit/a85742af56789bb5bf7acd3c5ac488081ab8d6a8))
* **examples:** report reload health per bundle and keep failures out of decisions_total ([#68](https://github.com/SpechtLabs/sigil/issues/68)) ([a8e42c6](https://github.com/SpechtLabs/sigil/commit/a8e42c6761258f36a565674f6d0d396d900a96a8))
* **policy:** accept a param bound from Go when it declares only min or max ([#61](https://github.com/SpechtLabs/sigil/issues/61)) ([e6fecec](https://github.com/SpechtLabs/sigil/commit/e6fecece04a0299aaa84f53f529723bf1eaee4f0))


### Performance Improvements

* **eval:** fold candidates without comparing every pair deeply ([#67](https://github.com/SpechtLabs/sigil/issues/67)) ([31353bd](https://github.com/SpechtLabs/sigil/commit/31353bd0062ffdabc7c86c8ce550b1cd63e87e76))
* **result:** don't box typed payloads for trace candidates ([#77](https://github.com/SpechtLabs/sigil/issues/77)) ([4580123](https://github.com/SpechtLabs/sigil/commit/4580123c6c46a7f48f29ed12c5b0ec6b4b45f1d8))

## [0.4.0](https://github.com/SpechtLabs/sigil/compare/v0.3.0...v0.4.0) (2026-09-29)


### ⚠ BREAKING CHANGES

* add a filter expression for lists ([#56](https://github.com/SpechtLabs/sigil/issues/56))

### Features

* add a filter expression for lists ([#56](https://github.com/SpechtLabs/sigil/issues/56)) ([b5b724e](https://github.com/SpechtLabs/sigil/commit/b5b724ec2417046253299e57dc6d800d33a4e42c))
* read candidate payloads in outcome asserts ([#57](https://github.com/SpechtLabs/sigil/issues/57)) ([e44b1c6](https://github.com/SpechtLabs/sigil/commit/e44b1c6e4afa4c8ab68b3116add7f42d7f15eaa4))

## [0.3.0](https://github.com/SpechtLabs/sigil/compare/v0.2.1...v0.3.0) (2026-09-28)


### ⚠ BREAKING CHANGES

* **kind:** reject tag options on input fields and accepts below 1
* **explain:** name every document by its full name and count modules separately

### Features

* **cli:** honor --output in every command ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **examples:** deploygate, a Go host service with both kinds, an LGTM stack and ginkgo suites ([#46](https://github.com/SpechtLabs/sigil/issues/46)) ([6e728fe](https://github.com/SpechtLabs/sigil/commit/6e728fe4cef3a24c35287d768b0164f9ffd73b52))
* **examples:** k6 load tests, continuous profiling and a demo CLI for deploygate ([#49](https://github.com/SpechtLabs/sigil/issues/49)) ([ab85250](https://github.com/SpechtLabs/sigil/commit/ab8525044253b8c2ae0daca1d29ef2ec0ff176d1))


### Bug Fixes

* **check:** reject decisions as map values ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **check:** report `==` and `!=` on lists instead of failing to infer an empty literal ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **cli:** point an unbound host function at the host's own binary ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **devtool:** correct the examples in devtool --help ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **explain:** name every document by its full name and count modules separately ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **kind:** reject tag options on input fields and accepts below 1 ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))
* **lexer:** explain hex, exponent and separator notation in number literals ([0089276](https://github.com/SpechtLabs/sigil/commit/0089276167fad5fdb042f35e6befbe2b28aef10e))


### Performance Improvements

* **eval:** reduce allocations and gate benchmark regressions ([#48](https://github.com/SpechtLabs/sigil/issues/48)) ([01d9f72](https://github.com/SpechtLabs/sigil/commit/01d9f72d6a0d1281451b27f1ec73aa9ab0965e7a))

## [0.2.1](https://github.com/SpechtLabs/sigil/compare/v0.2.0...v0.2.1) (2026-09-28)


### Features

* **release:** publish sigil to the homebrew tap ([#42](https://github.com/SpechtLabs/sigil/issues/42)) ([0dbacdc](https://github.com/SpechtLabs/sigil/commit/0dbacdc88ebfd39e727134cda3472f8c7d8b48e5))


### Bug Fixes

* **deps:** update module github.com/spf13/pflag to v1.0.10 ([#40](https://github.com/SpechtLabs/sigil/issues/40)) ([566f8eb](https://github.com/SpechtLabs/sigil/commit/566f8eb602b7418eccd1c5864812399782891399))

## [0.2.0](https://github.com/SpechtLabs/sigil/compare/v0.1.1...v0.2.0) (2026-09-28)


### Features

* **cli:** polish every command's output for a terminal ([#38](https://github.com/SpechtLabs/sigil/issues/38)) ([0417435](https://github.com/SpechtLabs/sigil/commit/041743536154cc087db275746a040641194ae83e))
* **tooling:** sigil fmt, check, eval, test and export, with lints and policytest ([#35](https://github.com/SpechtLabs/sigil/issues/35)) ([3c033f6](https://github.com/SpechtLabs/sigil/commit/3c033f603d87fd52ddec3d94ff11fe2060b1dccd))


### Miscellaneous Chores

* go mod tidy ([3294c42](https://github.com/SpechtLabs/sigil/commit/3294c426286228d4bc6077bb78100c5017469a7d))

## [0.1.1](https://github.com/SpechtLabs/sigil/compare/v0.1.0...v0.1.1) (2026-09-28)


### Bug Fixes

* **release:** sign the checksum file and check go.mod is tidy in CI ([#36](https://github.com/SpechtLabs/sigil/issues/36)) ([6eec9a3](https://github.com/SpechtLabs/sigil/commit/6eec9a3200f10d171d0f3542553f4a9169ebd6ae))

## 0.1.0 (2026-09-28)


### Features

* **cli:** scaffold the sigil command surface with styled output ([f627868](https://github.com/SpechtLabs/sigil/commit/f62786850ccbfe49165b420afe7d475a903c5c3c))
* **eval:** rules, decisions, asserts and the trace ([#27](https://github.com/SpechtLabs/sigil/issues/27)) ([5f6fdf9](https://github.com/SpechtLabs/sigil/commit/5f6fdf9b5cb1c074e940bc5452de0746ea271356))
* **lang:** declared reasons, exclusive outcomes and the resolution rule ([#29](https://github.com/SpechtLabs/sigil/issues/29)) ([3ddb36e](https://github.com/SpechtLabs/sigil/commit/3ddb36e9a42fabc8655ba3f7b81b73f92bc7f0c5))
* **lang:** imports, policy invocation and multi-document bundles ([#8](https://github.com/SpechtLabs/sigil/issues/8)) ([e0af09e](https://github.com/SpechtLabs/sigil/commit/e0af09e94235b82fb4becdda1ea5da181892a2ff))
* **lang:** modules, imports, invocation, required policies and the bundle loader ([#33](https://github.com/SpechtLabs/sigil/issues/33)) ([45bb9a2](https://github.com/SpechtLabs/sigil/commit/45bb9a2261a9f79d8db29e8f342acdd5c31d368a))
* **lang:** pin kind versions, has-only map keys, pin-aware names ([#21](https://github.com/SpechtLabs/sigil/issues/21)) ([7247e21](https://github.com/SpechtLabs/sigil/commit/7247e2191d32507977e8212676946e9ee4d9d410))
* **lang:** settle asserts, collect modes, lets, optionals and param bounds ([#20](https://github.com/SpechtLabs/sigil/issues/20)) ([820bfcd](https://github.com/SpechtLabs/sigil/commit/820bfcdd2798ae20cdc1c734278ce966e230ec00))
* **parser:** lexer, Pratt expression parser and document parser ([#17](https://github.com/SpechtLabs/sigil/issues/17)) ([07f10f1](https://github.com/SpechtLabs/sigil/commit/07f10f14a3d72093bc983e9f63414e5977763cf4))
* **types:** kinds, type checker and expression evaluator ([#18](https://github.com/SpechtLabs/sigil/issues/18)) ([95896bb](https://github.com/SpechtLabs/sigil/commit/95896bb84f4280012f9d9e01e49c1e8a9a09b3f1))


### Bug Fixes

* **deps:** update dependency mermaid to ^11.16.1 [security] ([#2](https://github.com/SpechtLabs/sigil/issues/2)) ([483e05a](https://github.com/SpechtLabs/sigil/commit/483e05ab66cd0d26c10b78f25ab853960178c9a3))
* **deps:** update docs dependencies ([#12](https://github.com/SpechtLabs/sigil/issues/12)) ([de94529](https://github.com/SpechtLabs/sigil/commit/de94529bb98dd5bbfabcb218428fa09df2243826))
* **deps:** update module github.com/spf13/cobra to v1.10.2 ([#13](https://github.com/SpechtLabs/sigil/issues/13)) ([882ced4](https://github.com/SpechtLabs/sigil/commit/882ced41ad41deede85335e3ebd334205618901a))
* remove unnecessary line break for style ([c475d69](https://github.com/SpechtLabs/sigil/commit/c475d69b9e5b8988adeee189a6b62661e175cc57))
* **renovate:** keep the toolchain directive in go.mod ([#16](https://github.com/SpechtLabs/sigil/issues/16)) ([0f8b82d](https://github.com/SpechtLabs/sigil/commit/0f8b82d5fa83aacf91050db53cb6d9a07a4309b8))
