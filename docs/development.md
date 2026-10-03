# Development

servediff is a Go server with a React/Vite web application embedded into the
release binary. Node and pnpm are build and test dependencies only.

## Install development version

Install the latest development changes from `main` with Go 1.25 or later:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@main
```

## Requirements

- [mise](https://mise.jdx.dev) 2026.8.6 or later
- C compiler for Go race tests
- Git
- Bash (included with Git for Windows)

Mise manages Go 1.25, Node from `.node-version`, and pnpm from `package.json`.
After cloning, trust the project config, install tools, then install dependencies,
hooks, and Playwright Chromium:

```sh
mise trust
mise install
mise run setup
```

`mise run` activates project tools without shell activation. For direct pnpm
commands, activate mise in your shell or use `mise exec -- pnpm <script>`.
List available tasks with `mise tasks`. Keep personal overrides in
`mise.local.toml`, which is ignored by Git.

## Commands

| Purpose                                                    | Command                          |
| ---------------------------------------------------------- | -------------------------------- |
| Start Vite HMR with a managed Go fixture server            | `mise run dev` or `pnpm dev:web` |
| Start a standalone Go fixture server with built web assets | `mise run dev:server`            |
| Generate the TypeScript API types                          | `mise run generate`              |
| Build the web application                                  | `mise run web:build`             |
| Build the dependency-free CLI at `dist/servediff`          | `mise run build`                 |
| Build and locally install the CLI                          | `mise run install`               |
| Install Playwright Chromium                                | `pnpm test:browser:install`      |
| Run web unit and browser tests                             | `pnpm test:web`                  |
| Run Go tests                                               | `mise run test:go`               |
| Run distribution API conformance tests                     | `mise run test:conformance`      |
| Run TypeScript checks                                      | `pnpm typecheck`                 |
| Run JavaScript linting                                     | `pnpm lint`                      |
| Format supported files                                     | `pnpm format`                    |
| Run every repository check                                 | `mise run check`                 |
| Run all pre-push checks, including Go race tests           | `pnpm check:push`                |
| Run lightweight CI checks                                  | `mise run check:ci`              |

Set `SERVEDIFF_TEST_PORT` for a separate browser-test server (default 4173), e.g. `SERVEDIFF_TEST_PORT=4183 mise exec -- pnpm --filter @servediff/web test:browser`.

`mise run setup` enables the Husky `pre-push` hook. Every push runs
`mise run check:push`: all repository suites and Go race tests, then rejects
uncommitted generated API types or embedded assets. `mise run setup` installs
Playwright Chromium; reinstall it after upgrading Playwright.
The hook needs mise on `PATH`; mise activates project tools. Checks run on your
local platform.

Both CI and release verification run `mise run check:ci`: static checks, web and
CLI builds, generated-file consistency, and the distribution API smoke test.
Unit, browser, release-tool, and Go
race suites run locally before pushing. CI does not run a native OS test matrix.

`mise run install` embeds the current web build and installs `servediff` into
`$GOBIN`, or `$GOPATH/bin` when `GOBIN` is unset. Ensure that directory is on
`PATH`. The task also removes obsolete pnpm-global Node shims created by older
versions of the repository.

The production web build is committed under `internal/webui/dist` so installs
from tags and `main` contain the complete application. Run `mise run web:stage` and
commit asset changes after modifying the frontend.

## Development data

Development uses `test/fixtures/sample.diff` with in-memory review state. The
Vite server starts and stops its own Go fixture process, so frontend development
does not need a real repository or persisted data.

Start the fixture server with built web assets:

```sh
mise run dev:server -- --no-browser
```

`serve` stays in the foreground and does not use the personal daemon. Ordinary
repo/pipe commands submit to the background service and exit. Use isolated
runtime directories (`SERVEDIFF_RUNTIME_DIR`) and in-memory state for lifecycle tests; fixture processes
must not register inputs in the personal service. `SERVEDIFF_EXIT_ON_STDIN_CLOSE`
is a foreground development-process lifecycle hook.

Before replacing a running binary, stop the service with `servediff service
stop`. Stop older foreground binaries separately; they do not honor the new
database ownership lock. Restart uses the invoking binary and restores retained
contexts from persistent state. Memory state lasts only for one process.

## Build pipeline

The production build generates API types, builds the web application, stages the
Vite output under `internal/webui`, and embeds those assets into the Go binary.
The resulting `dist/servediff` executable has no Node runtime dependency. Live
repository mode still requires Git.

The OpenAPI contract in `packages/api/openapi.yaml` is the frontend/server
boundary. After changing it, run:

```sh
mise run generate
```

## Frontend conventions

Follow [DESIGN.md](../DESIGN.md). Semantic tokens in `apps/web/src/tokens.css`
map into Tailwind and shadcn theme roles. Reuse primitives from
`apps/web/src/components/ui`; keep authored CSS for specialized layout, dynamic
geometry, and Pierre's measured rendering boundary.

`App.tsx` composes page sections. `AppProvider` composes appearance, sidebar,
and workspace owners. Consumers subscribe through domain hooks for diff source,
navigation, draft, review, reviewed files, and collapse state. One draft persists
across scopes and pauses diff polling. Context selection uses the top-bar
popup picker (also Cmd/Ctrl+K) and scopes every request. Registering a repository
discovers its Git worktrees at any path. Catalog reads reuse metadata for
10 seconds without loading diffs; registration refreshes discovery immediately.
Discovery preserves existing review identities and submission order.
The picker shows changed-file counts across staged, unstaged, and untracked
changes. Changed checkouts come first, ordered within each group by latest commit
or working-tree change, including search results. Captures use their fixed
snapshot count; unknown metadata is distinct from an empty checkout.
Switching disposes the previous workspace's request ownership and resets transient
state.
`DiffWorkspace.tsx` owns Pierre rendering,
versions, worker options, and measured geometry. `use-review.ts` and
`use-reviewed-files.ts` own their REST requests and recovery; reads cannot settle
across writes or owner disposal. Keep section-only state within its component.

See [architecture.md](architecture.md) for repository boundaries and dependency
rules.
