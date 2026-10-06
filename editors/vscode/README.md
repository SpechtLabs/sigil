# Sigil for Visual Studio Code

Editor support for [Sigil](https://sigil.specht-labs.de/), a small, statically typed policy language: highlighting for `.sigil` files, and the Sigil language server for diagnostics, completion, hover, go to definition and formatting.

Install the extension and open a folder that has `.sigil` files. The extension ships with the `sigil` binary for macOS and Linux, so there's nothing else to install or configure.

## What you get

- **Highlighting** for policy, module and kind files, and for ```` ```sigil ```` code fences in Markdown.
- **Diagnostics**: the errors and lints `sigil check` reports, as you type, with the same messages, and **quick fixes** for the ones with an obvious fix, such as a misspelled reason or a missing payload field.
- **Completion** from the kind the document names: inputs, fields, host functions, decisions with their reasons and required payload fields, and rule templates such as `when`, `assert` and `use`, which fill in as snippets.
- **Signature help** inside a decision constructor or a host function call.
- **Hover** with a let's, an input's or a decision's type, and **inlay hints** with the type of each let and quantifier variable.
- **Go to definition** for lets and `use` targets.
- **Formatting** in `sigil fmt`'s canonical style: **Format Document**, or `editor.formatOnSave`.
- **Snippets** that start a document: type `policy`, `module` or `kind`, or `type`, `decision` or `enum` in a kind file, and pick the snippet.
- **Comment toggling**, bracket matching, and two-space indentation as `sigil fmt` writes it.
- **Schema validation** of the configuration file: `sigil.json` out of the box, `sigil.yaml` with the [YAML extension](https://open-vsx.org/extension/redhat/vscode-yaml), and `sigil.toml` with [Even Better TOML](https://open-vsx.org/extension/tamasfe/even-better-toml).

The language server is `sigil lsp`, part of the `sigil` CLI. The extension starts it once per window and restarts it when its settings change.

## Install

The extension isn't in the Visual Studio Marketplace. VS Code installs it from a VSIX file attached to every [Sigil release](https://github.com/SpechtLabs/sigil/releases), and the editors built on [Open VSX](https://open-vsx.org/extension/spechtlabs/sigil) install it from there.

### VS Code

Download the VSIX for your platform from the [latest release](https://github.com/SpechtLabs/sigil/releases/latest):

| Platform | File |
| --- | --- |
| macOS, Apple silicon | `sigil-darwin-arm64-<version>.vsix` |
| macOS, Intel | `sigil-darwin-x64-<version>.vsix` |
| Linux, x64 | `sigil-linux-x64-<version>.vsix` |
| Linux, arm64 | `sigil-linux-arm64-<version>.vsix` |
| Windows and everything else | `sigil-<version>.vsix`, which has no bundled `sigil` (see [Which sigil runs](#which-sigil-runs)) |

Then install it from a terminal:

```sh
code --install-extension sigil-darwin-arm64-<version>.vsix
```

Or open the Extensions view, pick **Install from VSIX...** from its **...** menu, and choose the file.

VS Code doesn't update an extension installed from a file. Once a day the extension asks GitHub for the latest release instead, and when there's a newer one it says so and links to it. Install the new VSIX the same way; `sigil.checkForUpdates` turns the check off.

### VSCodium, Cursor, Gitpod, Theia and other Open VSX editors

Open the Extensions view, search for **Sigil** and install the one published by **spechtlabs**. From a terminal, with VSCodium:

```sh
codium --install-extension spechtlabs.sigil
```

These editors update it like any other extension.

### Which sigil runs

The extension runs the first of these that exists:

1. The binary `sigil.path` names.
2. The binary bundled with the extension, the release build of the same version. The macOS (Apple silicon and Intel) and Linux (x64 and arm64) builds have one.
3. `sigil` on `PATH`.

On Windows, and on other platforms without a bundled binary, install the CLI and make sure it's on `PATH`:

```sh
brew install --cask spechtlabs/tap/sigil
# or
go install github.com/spechtlabs/sigil/cmd/sigil@latest
```

When none of the three exists, the extension says so and offers a link to these instructions. Highlighting and snippets work either way.

## Settings

| Setting | Default | What it does |
| --- | --- | --- |
| `sigil.path` | empty | The `sigil` binary to run. An absolute path, a path starting with `~/`, a path relative to the first workspace folder such as `bin/sigil`, or a command name looked up on `PATH`. Empty uses the bundled binary, then `PATH`. A path that doesn't exist is an error; the extension doesn't fall back to another `sigil` |
| `sigil.server.enabled` | `true` | Run the language server. Set it to `false` to keep highlighting and snippets only |
| `sigil.trace.server` | `messages` | How much of each message between VS Code and the server the **Sigil Language Server Trace** output channel logs, while that channel's log level is **Trace** |
| `sigil.checkForUpdates` | `true` | In VS Code, ask GitHub once a day for the latest Sigil release (`api.github.com/repos/SpechtLabs/sigil/releases/latest`), and offer to download it when it's newer than the extension. Editors that install from Open VSX never ask; they update the extension themselves. A user setting only |

Changing `sigil.path` or `sigil.server.enabled` restarts the server.

A folder you open can't pick the program the extension runs. In an [untrusted workspace](https://code.visualstudio.com/docs/editor/workspace-trust), it ignores a `sigil.path` set in the workspace's own settings, and refuses a relative `sigil.path` such as `bin/sigil` even from your user settings, because it would resolve inside the workspace; absolute paths, `~/` and command names still work. Trusting the workspace restarts the server with both. Everywhere, it skips `PATH` entries that aren't absolute, such as `bin`, since the server starts in the workspace folder. On Windows, `sigil.path` can't name a `.bat` or `.cmd` file, which can't start without a shell.

To format on save:

```json
"[sigil]": {
  "editor.formatOnSave": true
}
```

## Commands

Open the Command Palette and type **Sigil**:

| Command | What it does |
| --- | --- |
| **Sigil: Restart Language Server** | Stops the server and starts it again, with the binary the settings pick now |
| **Sigil: Show Language Server Output** | Opens the **Sigil Language Server** output channel: which binary started, and what the server wrote to stderr |
| **Sigil: Show sigil Version** | Runs `sigil version` with the binary the settings pick, and shows the version, the commit and where the binary came from |

With a `.sigil` file open, the `{}` item in the status bar shows the server's state; click it for the output channel.

## Troubleshooting

**"Couldn't find the sigil binary".** The extension has no bundled binary for your platform, and there's no `sigil` on `PATH`. Install the CLI (see [Which sigil runs](#which-sigil-runs)), or set `sigil.path` to it. VS Code reads `PATH` when it starts, so restart VS Code after installing, or launch it from a shell that has the new `PATH`.

**"sigil.path is set to ..., but there's no executable at ...".** The setting points at a file that doesn't exist or can't run. Fix the path, or clear the setting to use the bundled binary.

**"sigil.path is set to ..., a path relative to the workspace, which an untrusted workspace can't choose".** The workspace isn't trusted, and a relative path would run a program from inside it. Trust the workspace (**Manage Workspace Trust** in the error), or make `sigil.path` absolute.

**"sigil lsp didn't start".** The binary ran but the language server didn't come up. A `sigil` from before the language server exits at once: run **Sigil: Show sigil Version**, and update the CLI or clear `sigil.path`. **Sigil: Show Language Server Output** has what the server printed.

**No diagnostics, or diagnostics about a missing kind.** The server checks a policy against the kind its header names, found the way `sigil check` finds it: a kind file among the workspace's `.sigil` files, or one the nearest `sigil.yaml` lists under `kinds:`. Run `sigil check` in a terminal in the same folder; the server reports what it reports.

**Logging the protocol.** Open the **Sigil Language Server Trace** channel in the Output panel, set its log level to **Trace** from the gear menu, and set `sigil.trace.server` to `verbose` for the full messages.

**Highlighting but nothing else.** Check that `sigil.server.enabled` is `true`, and look at the status bar item and the output channel.

## Building from source

The extension lives in [`editors/vscode`](https://github.com/SpechtLabs/sigil/tree/main/editors/vscode) of the Sigil repository, and builds with [Bun](https://bun.sh/). The repository pins every tool in `.mise.toml`; with [mise](https://mise.jdx.dev/) installed, from the repository root:

```sh
mise run vscode-build     # bundle the extension into editors/vscode/dist/
mise run vscode-lint      # type-check, and lint with Biome
mise run vscode-test      # unit, grammar and integration tests
mise run vscode-package   # build a .vsix with a sigil built from this checkout
code --install-extension editors/vscode/sigil-*.vsix
```

`vscode-package` builds `sigil` for your machine into the VSIX, so the extension you install runs the language server from the same checkout. The integration tests download VS Code into `editors/vscode/.vscode-test/` and open a window; without a display or a network they skip.

To run the extension from source instead, open `editors/vscode` in VS Code and press F5 with the **Run Extension** launch configuration, or start VS Code with `code --extensionDevelopmentPath=editors/vscode`.

## License

Apache-2.0. See [LICENSE](https://github.com/SpechtLabs/sigil/blob/main/LICENSE).
