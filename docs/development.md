# Development

servediff is a Go server with a React/Vite web application embedded into the
release binary. Node and pnpm are build and test dependencies only.

## Requirements

- Go 1.25
- Git
- Node 26
- pnpm 11
- [Task](https://taskfile.dev)

Install JavaScript dependencies after cloning:

```sh
pnpm install
```

## Commands

| Purpose                                                    | Command                      |
| ---------------------------------------------------------- | ---------------------------- |
| Start Vite HMR with a managed Go fixture server            | `task dev` or `pnpm dev:web` |
| Start a standalone Go fixture server with built web assets | `task dev:server`            |
| Generate the TypeScript API types                          | `task generate`              |
| Build the web application                                  | `task web:build`             |
| Build the dependency-free CLI at `dist/servediff`          | `task build`                 |
| Build and locally install the CLI                          | `task install`               |
| Install Playwright Chromium                                | `pnpm test:browser:install`  |
| Run web unit and browser tests                             | `pnpm test:web`              |
| Run Go tests                                               | `task test:go`               |
| Run distribution API conformance tests                     | `task test:conformance`      |
| Run TypeScript checks                                      | `pnpm typecheck`             |
| Run JavaScript linting                                     | `pnpm lint`                  |
| Format supported files                                     | `pnpm format`                |
| Run every repository check                                 | `task check`                 |

`task install` embeds the current web build and installs `servediff` into
`$GOBIN`, or `$GOPATH/bin` when `GOBIN` is unset. Ensure that directory is on
`PATH`. The task also removes obsolete pnpm-global Node shims created by older
versions of the repository.

## Development data

Development uses `test/fixtures/sample.diff` with in-memory review state. The
Vite server starts and stops its own Go fixture process, so frontend development
does not need a real repository or persisted data.

The Go binary also supports explicit fixture use:

```sh
go run ./cmd/servediff \
  --fixture test/fixtures/sample.diff \
  --state memory \
  --no-browser
```

## Build pipeline

The production build generates API types, builds the web application, stages the
Vite output under `internal/webui`, and embeds those assets into the Go binary.
The resulting `dist/servediff` executable has no Node runtime dependency. Live
repository mode still requires Git.

The OpenAPI contract in `packages/api/openapi.yaml` is the frontend/server
boundary. After changing it, run:

```sh
task generate
```

## Frontend conventions

Follow [DESIGN.md](../DESIGN.md). Semantic tokens in `apps/web/src/tokens.css`
map into Tailwind and shadcn theme roles. Reuse primitives from
`apps/web/src/components/ui`; keep authored CSS for specialized layout, dynamic
geometry, and Pierre's measured rendering boundary.

`App.tsx` composes page sections. `app-state.tsx` owns shared state through
`AppProvider`; `DiffWorkspace.tsx` owns Pierre rendering; `use-review.ts` uses
the generated REST client. Keep section-only state within its component.

See [architecture.md](architecture.md) for repository boundaries and dependency
rules.
