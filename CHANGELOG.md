# Changelog

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
