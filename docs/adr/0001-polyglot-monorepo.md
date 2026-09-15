# ADR 0001: Native polyglot workspace

Status: accepted

## Context

servediff has an existing Node server and React application. A Go server will
provide the same behavior, later host additional transports such as MCP, and
produce a single binary containing the web UI. Future Go commands and web apps
may share private Go packages or focused TypeScript packages.

## Decision

- Keep pnpm workspaces for JavaScript and use one root Go module.
- Keep the Node implementation during Go parity work.
- Use OpenAPI and serialized fixtures as cross-language boundaries.
- Put Go binaries in `cmd` and shared private Go packages in root `internal`.
- Use Taskfile only to orchestrate native pnpm and Go commands.
- Embed staged Vite output in the Go binary.
- Add modules, Nx, Turborepo, or Bazel only after measured need.

## Consequences

Cross-language changes remain atomic. Each ecosystem keeps native tooling. The
final Go build requires both toolchains, while Go unit tests do not require Node.
During migration, behavior must be tested against both server implementations.
