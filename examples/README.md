# Examples

Each directory is a complete service built on Sigil, against the checkout it lives in, with its own README and `.mise.toml`. Run its tasks from that directory, or with `mise run -C examples/<name> <task>` from the repository root.

- [`deploy-gates/`](./deploy-gates): deploygate, a Go service that decides production deploys and grants access, with an observability stack.
- [`alert-routing/`](./alert-routing): alertrouter, a TypeScript service on Sigil's WebAssembly build that routes Alertmanager alerts, with an operator console, an observability stack and a k6 load test suite.
