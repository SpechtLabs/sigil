# Examples

Each directory is a Go module of its own that builds against the Sigil checkout it lives in, with its own README and `.mise.toml`. Run its tasks from that directory, or with `mise -C examples/<name> run <task>` from the repository root.

- [`deploy-gates/`](./deploy-gates): deploygate, the production deploy gate and access-grant service, with an observability stack.
