# Changelog

The extension is released with Sigil and carries its version, and each
platform's build bundles the `sigil` binary of the same release. Changes to
Sigil itself, the language server included, are in the
[Sigil changelog](https://github.com/SpechtLabs/sigil/blob/main/CHANGELOG.md).

## First release

- Highlighting for policy, module and kind files, and for ```` ```sigil ````
  fences in Markdown.
- `sigil lsp` for diagnostics, completion, hover, go to definition and
  formatting, run from the bundled binary, `sigil.path` or `PATH`.
- Snippets that start policy, module and kind documents, and kind declarations; rules, constructors and the rest come from the language server's completions.
- Schema validation for `sigil.yaml`, `sigil.json` and `sigil.toml`.
- A VSIX per platform on every GitHub release, for VS Code, and the extension
  on Open VSX for VSCodium, Cursor and the other Open VSX editors. In VS Code,
  a daily check offers the newer release when there is one.
