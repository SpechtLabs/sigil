---
title: Set up your editor
icon: mdi:application-edit-outline
createTime: 2026/10/06 12:00:00
permalink: /guides/editors/
---

This guide connects an editor to `sigil lsp`, the Sigil language server. Once it's set up, the editor shows the problems `sigil check` reports while you type. It also completes inputs, fields, functions, decisions, reasons and imports, shows a decision's whole declaration on hover, and jumps to where a name is declared, in the kind file or in another policy.

For VS Code and Neovim, the editor plugins are the recommended setup: [Set up VS Code](/guides/editors/vscode/) and [Set up Neovim](/guides/editors/neovim/). The rest of this page is for any other editor that runs a language server.

## What the server needs

The server reads the same files `sigil check` does. That means the `sigil` binary and the exported kind file in the repository, not the host's Go code.

- Put `sigil` on your `PATH`; `sigil version` prints its version. A host binary built with `cli.Main` runs the language server too. Use its name in place of `sigil` below, and its linked kinds count.
- The editor starts the server as `sigil lsp --stdio` for files that end in `.sigil`, with the language ID `sigil`, and talks to it over stdin and stdout. It takes no other flags and no settings, and its log goes to stderr.
- For each open file, the server reads the project from the nearest `sigil.yaml` at or above it, or from the workspace folder when there's none. Use `sigil.yaml` and then `.git` as the root markers. [Projects](/reference/cli/#projects) has the rules.
- Run `sigil check` at the root of the policy repository first. When it finds the kinds and the policies, the server will too.

## Helix

Add the language and its server to `languages.toml`, in `~/.config/helix/` or in the repository's `.helix/`:

```toml
[language-server.sigil]
command = "sigil"
args = ["lsp", "--stdio"]

[[language]]
name = "sigil"
scope = "source.sigil"
file-types = ["sigil"]
roots = ["sigil.yaml", ".git"]
comment-token = "//"
language-servers = ["sigil"]
```

`hx --health sigil` shows whether Helix finds the server. `gd` goes to the definition, `space k` shows the hover, and `:format` formats the file. Helix doesn't highlight Sigil yet, because the tree-sitter grammar's queries use Neovim's capture names.

## Emacs

Eglot, built into Emacs 29 and later, starts the server for a major mode. Emacs has no Sigil mode, so define a minimal one and register the server in your init file:

```elisp
(define-derived-mode sigil-mode prog-mode "Sigil"
  "Major mode for Sigil policy, module and kind files."
  (setq-local comment-start "// ")
  (setq-local comment-start-skip "//+\\s-*"))
(add-to-list 'auto-mode-alist '("\\.sigil\\'" . sigil-mode))

(with-eval-after-load 'eglot
  (add-to-list 'eglot-server-programs '(sigil-mode "sigil" "lsp" "--stdio")))
(add-hook 'sigil-mode-hook #'eglot-ensure)
```

`M-.` goes to the definition, ElDoc shows the hover in the echo area, and `M-x eglot-format-buffer` formats the file. This mode doesn't highlight anything.

## Neovim without the plugin

[The plugin](/guides/editors/neovim/) sets all of this up. To start the server with Neovim 0.11's built-in client alone, put this in your `init.lua`:

```lua
vim.filetype.add({ extension = { sigil = "sigil" } })

vim.lsp.config("sigil", {
  cmd = { "sigil", "lsp", "--stdio" },
  filetypes = { "sigil" },
  root_markers = { "sigil.yaml", ".git" },
})
vim.lsp.enable("sigil")
```

`K` shows the hover, `CTRL-]` goes to the definition, and `gq` formats. For highlighting, install the tree-sitter grammar in `editors/tree-sitter-sigil`, as [its README](https://github.com/SpechtLabs/sigil/blob/main/editors/tree-sitter-sigil/README.md#neovim) describes.

## Check that it works

1. Open a policy, such as `payments/production.sigil`.
2. On a new line, type `when service.`. The editor offers the fields of the input's struct type, and marks the line with `expected a field name after` until you pick one.
3. Inside a `when` body, type `deny(reason: ` to get the decision's reasons.
4. Hover over a decision constructor to see its reasons and payload fields.

When nothing happens:

- Check that the editor started the server: `hx --health sigil` in Helix, `M-x eglot-events-buffer` in Emacs, `:checkhealth vim.lsp` in Neovim.
- Read the server's log, where every line starts with `sigil lsp:`. It's `:log-open` in Helix, the events buffer in Emacs, and `:lua vim.cmd.edit(vim.lsp.get_log_path())` in Neovim.
- Run `sigil check` from the file's directory. A configuration file that doesn't parse, or a requirement that can't be enforced, stops the check there and shows as an error message in the editor.

[`sigil lsp`](/reference/cli/#sigil-lsp) lists what completes where, what hover shows, and where definitions go.
