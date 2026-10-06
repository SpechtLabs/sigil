# tree-sitter-sigil

A [tree-sitter](https://tree-sitter.github.io/) grammar for Sigil: policy, module and kind files, including files that hold several documents separated by `---` or by the next header. Editors built on tree-sitter use it for highlighting, folding, indentation and text objects. The Go parser in `internal/parser` defines the language, and the tests here hold the grammar to it.

The parser name is `sigil` and the file extension is `.sigil`. Policies, modules and kinds share the extension and the parser: the header keyword (`policy`, `module` or `kind`) starts each document, so one tree can hold all three.

## Layout

| Path | What it is |
| --- | --- |
| `grammar.js` | The grammar. It has no npm dependencies and no external scanner |
| `src/` | The parser `tree-sitter generate` writes from `grammar.js`, committed because editors compile it without running the generator |
| `tree-sitter.json` | The grammar's name, scope, file types and query paths, for the CLI and for editors that read it |
| `queries/` | `highlights`, `locals`, `folds`, `indents`, `injections` and `textobjects`, with [nvim-treesitter's capture names](https://github.com/nvim-treesitter/nvim-treesitter/blob/main/CONTRIBUTING.md) |
| `test/corpus/` | Corpus tests: one case per grammar production, input and expected tree |
| `test/highlight/` | Highlight tests: `.sigil` files with `^` assertions under the lines they check |
| `grammar_test.go` | The Go tests that hold the grammar to the Go parser |

## Regenerate and test

From the repository root, with the tree-sitter CLI that `.mise.toml` pins:

```sh
mise run tree-sitter-generate   # src/ from grammar.js
mise run tree-sitter-test       # corpus, highlight and Go tests
```

Run `tree-sitter-generate` after every change to `grammar.js` and commit `src/` with it. Generating uses the CLI's built-in JavaScript runtime, so it needs neither Node nor npm. The parser is generated for ABI 15, which Neovim 0.12 loads.

`tree-sitter-test` runs `tree-sitter test`, then `go test` in this directory. The Go tests need the tree-sitter CLI and a C compiler, and skip when the CLI isn't on `PATH`, so a plain `go test ./...` passes without them:

- `TestCorpus` parses every `.sigil` file in the repository, every `sigil` code block in a Markdown file, the formatter's golden files and the input of every corpus test case. Each source the Go parser accepts must parse without an `ERROR` or `MISSING` node, and both trees must agree on where every document, statement, declaration, composite type and composite expression starts and ends, which holds operator precedence and statement boundaries to the Go parser's. The test tries a code block that isn't a whole file as a policy body, a kind body, an expression, and a list of expressions, one per line. It skips the sources the Go parser rejects, such as its error goldens, but a corpus test case fails it when the Go parser rejects it, or accepts it although the case is marked `:error`.
- `TestQueries` compiles every query file against the grammar, so a node or field that a grammar change renames fails here, not in an editor.
- `TestGeneratedSourcesAreCurrent` regenerates the parser and fails when `src/` differs from it. A tree-sitter CLI update that changes the generated parser fails it too; run `tree-sitter-generate` and commit the result.

CI runs the same task in the Editors workflow.

## Use it in an editor

### Neovim

[sigil.nvim](https://github.com/SpechtLabs/sigil.nvim) registers this parser at a pinned revision, installs it on the first `.sigil` file, and starts tree-sitter highlighting and indentation along with the language server; [Use Sigil in Neovim](https://sigil.specht-labs.de/guides/editors/neovim/) sets it up. Without the plugin, do it by hand.

nvim-treesitter's `main` branch installs the parser and the queries from this directory. Register it in a `User TSUpdate` autocommand, add the filetype, and run `:TSInstall sigil`:

```lua
vim.api.nvim_create_autocmd("User", {
  pattern = "TSUpdate",
  callback = function()
    require("nvim-treesitter.parsers").sigil = {
      install_info = {
        url = "https://github.com/SpechtLabs/sigil",
        location = "editors/tree-sitter-sigil",
        queries = "editors/tree-sitter-sigil/queries",
      },
    }
  end,
})

vim.filetype.add({ extension = { sigil = "sigil" } })
```

`location` is where the parser is, relative to the repository; `queries` is relative to the repository too. nvim-treesitter compiles `src/` with `tree-sitter build`, so the tree-sitter CLI must be on `PATH`. Without a `revision`, it installs from `main`. For a local checkout, use `path = "/path/to/sigil"` in place of `url`; nvim-treesitter then links the queries instead of copying them.

Start highlighting with `vim.treesitter.start()` in a `FileType sigil` autocommand, folds with `vim.wo.foldexpr = "v:lua.vim.treesitter.foldexpr()"`, and indentation with `vim.bo.indentexpr = "v:lua.require'nvim-treesitter'.indentexpr()"`. [nvim-treesitter-textobjects](https://github.com/nvim-treesitter/nvim-treesitter-textobjects) finds `textobjects.scm` with the other queries.

### Other editors

Helix and Zed load tree-sitter grammars from a Git repository and a subdirectory, too. Their capture names differ from Neovim's, so they need queries of their own; `queries/` is the starting point.

## Text objects

| Capture | Matches |
| --- | --- |
| `@function` | A rule (`when`), and a host function's declaration (`fn`) |
| `@class` | A document, a struct type and a decision |
| `@conditional` | A rule: its condition, and its body |
| `@block` | A rule body, a struct type's fields, a decision's body |
| `@loop` | A quantifier (`any`, `all`) or a filter, and its body |
| `@call` | A decision constructor, a policy invocation, a host function call |
| `@parameter` | An argument, a function's parameter type, an imported name |
| `@statement` | A statement of a policy or module, a declaration of a kind |
| `@assignment` | A `let`, and a param or payload field with a default |
| `@regex` | The pattern after `matches` |
| `@number`, `@comment` | Number and duration literals, comments |

## Where the tree is more lenient than the parser

An editor needs a tree for text that doesn't check yet, so the grammar accepts some things the Go parser reports, and leaves them to it:

- Comparisons chain, and `or` and `xor` mix, grouped as left-associative operators.
- A quantifier or filter can be an operand of any operator, as in `x == all r in xs: p`, and a quantifier's range and a map key can be any expression.
- A module can hold any statement, a rule body can hold `use` and `param`, and `use` can come after other statements.
- Whitespace may surround the dots of a dotted name such as `deploy.common`.
- The lexer's checks on literals: a duration's units, in order, each once, and an integer's range.

One place reads differently: in a decision body, a payload field's default ends before an operator at the level of a comparison or looser, so that a field named like a keyword operator (`in: string`) starts the next field. The Go parser looks ahead for the `:` instead. A default must be a constant, which only literals, `+` and `-` make, so the two never disagree on a kind that checks.
