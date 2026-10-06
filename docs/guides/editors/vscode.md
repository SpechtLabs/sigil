---
title: Set up VS Code
icon: mdi:microsoft-visual-studio-code
createTime: 2026/10/06 12:00:00
permalink: /guides/editors/vscode/
---

This guide sets up Visual Studio Code for a policy repository. When it's done, `.sigil` files are highlighted, problems show up as you type with the messages `sigil check` prints, and **Format Document** writes `sigil fmt`'s style. It works the same in VSCodium, Cursor and the other editors that install extensions from Open VSX; only the install step differs.

## Install the extension

The extension isn't in the Visual Studio Marketplace. VS Code installs it from a VSIX file on the GitHub release; VSCodium, Cursor, Gitpod, Theia and the other editors that use Open VSX install it from there.

In **VS Code**, download the file for your platform from the [latest release](https://github.com/SpechtLabs/sigil/releases/latest):

| Platform | File |
| --- | --- |
| macOS, Apple silicon | `sigil-darwin-arm64-<version>.vsix` |
| macOS, Intel | `sigil-darwin-x64-<version>.vsix` |
| Linux, x64 | `sigil-linux-x64-<version>.vsix` |
| Linux, arm64 | `sigil-linux-arm64-<version>.vsix` |
| Windows and everything else | `sigil-<version>.vsix`, which needs [sigil on `PATH`](#install-sigil) |

Install it from a terminal:

```sh
code --install-extension sigil-darwin-arm64-<version>.vsix
```

Or open the Extensions view, pick **Install from VSIX...** from its **...** menu, and choose the file. VS Code won't update an extension installed this way. Instead, once a day the extension checks GitHub for a newer release and offers a link to it; install the new file the same way. Set `sigil.checkForUpdates` to `false` to stop the check.

In an **Open VSX editor**, search the Extensions view for **Sigil**, published by **spechtlabs**, and install it. From a terminal, with VSCodium:

```sh
codium --install-extension spechtlabs.sigil
```

These editors keep it up to date like any other extension.

Open the repository's folder. The extension starts when the folder has a `.sigil` file anywhere in it. On macOS and Linux it runs the `sigil` it ships with, the build of the same release, so there's nothing else to install; open a policy and you should see highlighting, and diagnostics as soon as you type.

## Install sigil

On Windows, and any other platform the extension has no bundled binary for, it runs `sigil` from `PATH`. Install the CLI:

```sh
brew install --cask spechtlabs/tap/sigil
```

Or with Go:

```sh
go install github.com/spechtlabs/sigil/cmd/sigil@latest
```

VS Code reads `PATH` once, when it starts, so restart it after installing. Without a `sigil` the extension shows an error with a link back here; highlighting and snippets still work.

## Run a different sigil

To run a build of your own, or a version other than the bundled one, set `sigil.path` in your settings. It takes an absolute path, a path starting with `~/`, a path relative to the workspace folder, or a command name to look up on `PATH`:

```json
{
  "sigil.path": "~/go/bin/sigil"
}
```

The server restarts when the setting changes. **Sigil: Show sigil Version** in the Command Palette says which binary runs and where it came from.

A workspace's `.vscode/settings.json` can set `sigil.path` too, for a repository that builds its own binary, such as a host binary with the kinds linked in. VS Code only honours it once you trust the workspace, and until then a relative `sigil.path`, even one in your user settings, is refused rather than resolved inside the folder.

## Format on save

The extension formats with the language server, in the style `sigil fmt` writes. To format every time you save, add this to your settings:

```json
{
  "[sigil]": {
    "editor.formatOnSave": true
  }
}
```

## Check the configuration file

With the [YAML extension](https://open-vsx.org/extension/redhat/vscode-yaml) installed, `sigil.yaml` completes its keys and lint names and flags a typo, from the [configuration file's schema](/reference/config/#schema). `sigil.json` gets the same out of the box, and `sigil.toml` with [Even Better TOML](https://open-vsx.org/extension/tamasfe/even-better-toml).

## When the server doesn't start

The `{}` item in the status bar, next to the language mode, shows the server's state while a `.sigil` file is open. Click it to open the **Sigil Language Server** output channel, which shows the binary that started and everything the server wrote.

- **sigil not found**: there's no bundled binary for this platform and no `sigil` on `PATH`. [Install sigil](#install-sigil).
- **sigil.path is set to ..., but there's no executable at ...**: fix the path or clear the setting. The extension doesn't fall back to another `sigil` when the setting is wrong.
- **sigil lsp didn't start**: the binary runs but has no language server, which means it's older than the extension. Update it, or clear `sigil.path` to use the bundled one.
- **Problems about a kind that can't be found**: the server finds kinds the way [`sigil check`](/reference/cli/#sigil-check) does. Run `sigil check` in the same folder; it reports what the editor reports.

After fixing any of these, run **Sigil: Restart Language Server**.
