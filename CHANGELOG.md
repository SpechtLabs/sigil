# Changelog

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
* **parser:** lexer, Pratt expression parser and document parser ([#17](https://github.com/SpechtLabs/sigil/issues/17)) ([07f10f1](https://github.com/SpechtLabs/sigil/commit/07f10f14a3d72093bc983e9f63414e5977763cf4))
* **types:** kinds, type checker and expression evaluator ([#18](https://github.com/SpechtLabs/sigil/issues/18)) ([95896bb](https://github.com/SpechtLabs/sigil/commit/95896bb84f4280012f9d9e01e49c1e8a9a09b3f1))


### Bug Fixes

* **deps:** update dependency mermaid to ^11.16.1 [security] ([#2](https://github.com/SpechtLabs/sigil/issues/2)) ([483e05a](https://github.com/SpechtLabs/sigil/commit/483e05ab66cd0d26c10b78f25ab853960178c9a3))
* **deps:** update docs dependencies ([#12](https://github.com/SpechtLabs/sigil/issues/12)) ([de94529](https://github.com/SpechtLabs/sigil/commit/de94529bb98dd5bbfabcb218428fa09df2243826))
* **deps:** update module github.com/spf13/cobra to v1.10.2 ([#13](https://github.com/SpechtLabs/sigil/issues/13)) ([882ced4](https://github.com/SpechtLabs/sigil/commit/882ced41ad41deede85335e3ebd334205618901a))
* remove unnecessary line break for style ([c475d69](https://github.com/SpechtLabs/sigil/commit/c475d69b9e5b8988adeee189a6b62661e175cc57))
* **renovate:** keep the toolchain directive in go.mod ([#16](https://github.com/SpechtLabs/sigil/issues/16)) ([0f8b82d](https://github.com/SpechtLabs/sigil/commit/0f8b82d5fa83aacf91050db53cb6d9a07a4309b8))
