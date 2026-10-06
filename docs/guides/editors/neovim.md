---
title: Use Sigil in Neovim
icon: simple-icons:neovim
createTime: 2026/10/06 12:00:00
permalink: /guides/editors/neovim/
---

By the end of this guide, Neovim opens `.sigil` files with tree-sitter highlighting, `//` comments and two-space indentation, and the Sigil language server checks them as you type, completes fields from the kind file, and formats in `sigil fmt`'s style. [sigil.nvim](https://github.com/SpechtLabs/sigil.nvim) does all of it; its README is the reference for every option.

You need Neovim 0.11 or later. Tree-sitter through nvim-treesitter's `main` branch needs Neovim 0.12, the tree-sitter CLI 0.26.1 or later and a C compiler.

## Install the sigil binary

The plugin runs the `sigil` in your `$PATH` and never downloads one:

```sh
brew install --cask spechtlabs/tap/sigil
```

With [mise](https://mise.jdx.dev) instead:

```sh
mise use -g github:SpechtLabs/sigil
```

## Install the plugin

With lazy.nvim, LazyVim included, the spec is one line:

```lua
{ "SpechtLabs/sigil.nvim", version = "*" }
```

The plugin loads at startup even under LazyVim's `defaults.lazy = true`, and calls its own `setup()`. LazyVim's nvim-lspconfig, mason, blink.cmp and conform.nvim need no entry for Sigil: the plugin enables the server itself, the binary comes from `$PATH` rather than mason, blink.cmp adds its capabilities to every server, and conform falls back to the server's formatting.

With Neovim 0.12's built-in package manager:

```lua
vim.pack.add({ "https://github.com/SpechtLabs/sigil.nvim" })
```

vim-plug, mini.deps and packer.nvim work the same way, with no `setup()` call; the [README](https://github.com/SpechtLabs/sigil.nvim#install-the-plugin) has their lines.

## Open a policy

Open a `.sigil` file in a directory with a `sigil.yaml` whose `kinds:` names your kind file. The nearest configuration file above the file (`sigil.yaml`, or any other name in [Configuration file](/reference/config/)) is the workspace root, and without one the nearest `.git` is.

The first sigil buffer of a session has nvim-treesitter build the `sigil` parser, which takes a few seconds; highlighting switches from the regex fallback to tree-sitter when it's done. To have LazyVim install it with its other parsers instead, add it to nvim-treesitter's list:

```lua
{ "nvim-treesitter/nvim-treesitter", opts = { ensure_installed = { "sigil" } } }
```

A misspelled field shows up as a diagnostic, with the checker's suggestion:

```text
unknown field "severty" on type Alert help: did you mean "severity"? Alert declares: name, severity, labels, firing_for
```

Typing `alert.` completes the fields of the kind's `Alert` type, `K` shows a decision's signature, and `CTRL-]` (`gd` in LazyVim) jumps to a `let` or a `use` target. `gq` formats a range, and LazyVim's format on save formats the whole file.

## Check the setup

Run `:checkhealth sigil`. It reports the binary and its version, whether `sigil lsp --stdio` answers, the client and its root, and nvim-treesitter, the tree-sitter CLI, the parser and each query. A working setup reads like this, abridged:

```text
sigil binary ~
- ✅ OK `sigil lsp --stdio` answers initialize as sigil

language server ~
- ✅ OK config: cmd sigil lsp --stdio, root markers sigil.yaml, .git
- ✅ OK enabled for sigil buffers

tree-sitter ~
- ✅ OK nvim-treesitter `main` found
- ✅ OK tree-sitter CLI found; nvim-treesitter builds the parser with it
```

A sigil from before the language server shows `doesn't start a language server: exited with status 1: Error: "sigil lsp" is not implemented yet`; upgrade it.

`:Sigil` shows the same for the current buffer in one screen, and `:Sigil restart` restarts the server after you upgrade sigil or edit `sigil.yaml`.

## Use a project's sigil from mise

When a repository pins sigil in its `.mise.toml` and your global `$PATH` has none or another version, start the server through mise:

```lua
{
  "SpechtLabs/sigil.nvim",
  version = "*",
  opts = { lsp = { cmd = { "mise", "exec", "--", "sigil", "lsp", "--stdio" } } },
}
```

mise picks the version for Neovim's working directory, so start Neovim inside the repository.

## Format with sigil fmt through conform

conform.nvim formats through the server by default. To have it run `sigil fmt` itself, for example to format in a buffer the server can't attach to:

```lua
require("conform").setup({
  formatters_by_ft = { sigil = { "sigil_fmt" } },
  formatters = {
    sigil_fmt = { command = "sigil", args = { "fmt", "-" }, stdin = true },
  },
})
```

## Turn parts off

`lsp = { enabled = false }` keeps the server off, and `treesitter = { enabled = false }` leaves tree-sitter to your own configuration. Without nvim-treesitter, highlighting stays regex-based and everything else works. The [configuration reference](https://github.com/SpechtLabs/sigil.nvim#configuration) lists every option.
