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
| Build agent plugin adapters                                | `mise run plugins:build`         |
| Install Playwright Chromium                                | `pnpm test:browser:install`      |
| Run web unit and browser tests                             | `pnpm test:web`                  |
| Run Go tests                                               | `mise run test:go`               |
| Run boundary and shared-rule contracts                     | `mise run test:contracts`        |
| Check production package dependencies                      | `mise run check:boundaries`      |
| Check frontend design-token use                            | `mise run check:design`          |
| Run distribution API conformance tests                     | `mise run test:conformance`      |
| Run TypeScript checks                                      | `pnpm typecheck`                 |
| Run JavaScript linting                                     | `pnpm lint`                      |
| Format supported files                                     | `pnpm format`                    |
| Run every repository check                                 | `mise run check`                 |
| Run all pre-push checks, including Go race tests           | `pnpm check:push`                |
| Run lightweight CI checks                                  | `mise run check:ci`              |

Set `SERVEDIFF_TEST_PORT` for a separate browser-test server (default 4173), e.g. `SERVEDIFF_TEST_PORT=4183 mise exec -- pnpm --filter @servediff/web test:browser`.

`mise run setup` enables the Husky `pre-push` hook. Every push runs
`mise run check:push`: all repository suites, boundary contracts and Go race tests, then rejects
uncommitted generated API types or embedded assets. `mise run setup` installs
Playwright Chromium; reinstall it after upgrading Playwright.
The hook needs mise on `PATH`; mise activates project tools. Checks run on your
local platform.

Both CI and release verification run `mise run check:ci`: static checks, web and
CLI builds, generated-file consistency, and the distribution API smoke test.
Local `mise run check` adds dependency/design guards, behavioral contracts,
JavaScript unit/browser suites, release-tool tests and all Go tests; pre-push
adds Go race tests. Keep these full suites and guards local. Chromium is installed
by local setup, not CI/release workflows. CI does not run a native OS test matrix.
GitHub's `ci-required` ruleset requires the lightweight `checks` status on an
up-to-date `main` change, including administrators; it is configured in GitHub,
not by mise. PR evidence records full local validation.

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

`dev --fixture` collects its fixture before starting in the foreground and does
not use personal lifecycle discovery. `sync` collects the originating checkout and all registered
worktrees against the default branch merge base, plus working changes. Use
`--base HEAD` for working changes only, and `--branch` for explicit object-only
recovery. `sync` submits to one configured destination and exits.
Redirected stdin (`git diff | servediff`) starts a foreground local process with
a fixed patch and ignores remote config. `servediff [PATH]` likewise collects
once, defaults to the current directory, and opens a fixed checkout snapshot.
Neither mode watches Git or automatically replaces the selected snapshot.
A path argument or
`--path` cannot be combined with redirected stdin.
`CollectPatch` preserves the supplied patch without Git lookup;
its absolute submission directory is provenance only, not repository identity.
Use isolated
runtime directories (`SERVEDIFF_RUNTIME_DIR`) and in-memory state for lifecycle tests; fixture processes
must not register inputs in the personal service. `SERVEDIFF_EXIT_ON_STDIN_CLOSE`
is a foreground development-process lifecycle hook.

Stop running processes before upgrading. Schema 9 resets older versioned
databases transactionally on first open. Current databases survive restart;
in-memory state does not. Server `retentionDays` defaults to seven; fresh
submissions apply the current setting, while exact retries preserve expiry.

For isolated production-user tests, start `servediff-server` with a temporary
persistent database. Initial admin credentials live in `<database>.admin-token`;
startup reports its path. Stop the server before `user create --name NAME --state
DB`, save its printed token and restart. Users share one database, while credentials
scope REST, MCP and events. Do not provision against the running personal service.

## Build pipeline

The production build generates API types, builds the web application, stages the
Vite output under `internal/webui`, and embeds those assets into the Go binary.
The resulting `dist/servediff` and `dist/servediff-server` executables have no
Node runtime dependency. Git is required only for producer-side checkout
collection. Local/remote server queries read SQLite and never invoke Git.

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
across scopes. Context selection uses the top-bar popup picker (also Cmd/Ctrl+K)
and scopes every request. The “Switch review” picker first selects a repository
or Piped, then an immutable observation. Repository observations retain branch,
worktree, source/run, comparison, and collection-time metadata. Submission/session
associations remain separate from original snapshot metadata; catalog API filters
include `harness`, `sessionId`, `sessionName` and legacy `runId`. Global search is labeled
“Search reviews” and includes repository, branch, worktree, hostname, and source/run
labels. Piped history uses “Search Piped”, with collection timestamps newest first
and no repository filters. The header identifies these reviews with a terminal
icon, “Piped”, and collection time, without repository, branch, worktree, or directory.
The optional API `Context.source` distinguishes `local` and `stdin` independently
of its identity. Catalog presentation masks repository identity and stale state
on stdin observations. Repository
freshness, host, branch, worktree, and status filters never exclude Piped imports.
Catalog reads query stored metadata only; ingestion events, reconnect, and tab
visibility reload the catalog without replacing the selected observation.
Opening a review never discovers worktrees or reads Git. Repository results default to
Latest snapshots with changes. All / Latest / Stale filters freshness; the
right-aligned All checkbox also includes unavailable, empty, and unknown-status
snapshots. A newer collection supersedes older snapshots for the same owner,
source, repository, checkout, branch and comparison policy; arrival order breaks collection-time
ties. Freshness uses a durable stream head, including deduplicated collections and
observations outside the current search or page. Expiry and pruning leave that head
intact, so older retained snapshots stay stale. A newer collection of previous
content makes its existing review latest again; its original snapshot metadata stays fixed.
Changed observations come first, ordered by last submission time; counts
describe their fixed snapshots. Host, Branch, and Worktree filters apply before
repository grouping. Unavailable entries are disabled and skipped during keyboard
navigation. Filter selections persist while navigating and reopening the picker.
Switching disposes the previous workspace's request ownership and resets transient
state.
The header's delete action removes the selected snapshot, all
its stored diff scopes, comments, and reviewed-file marks after confirmation.
Deletion preserves stream freshness history; older snapshots remain stale.
It leaves Git files untouched, and a new collection can create a review again.
The footer shows the selected scope's actual baseline. Its Diff comparison popover
explains the calculation and exposes the recorded base ref, base commit, merge base,
and HEAD with full commit IDs. These facts are persisted in observation metadata
and returned by `GET /api/v2/contexts/{contextId}` as `observation.comparison`
and `observation.head`; reading them never resolves current Git refs.

`DiffWorkspace.tsx` owns Pierre rendering,
versions, worker options, and measured geometry. `use-review.ts` and
`use-reviewed-files.ts` own their REST requests and recovery; reads cannot settle
across writes or owner disposal. Keep section-only state within its component.

See [architecture.md](architecture.md) for repository boundaries and dependency
rules.

Shared producer orchestration in `cmd/servediff` resolves config → environment →
flags once, pins retries to destination/database identity and serves manual/hook
delivery. Hooks use session-scoped acknowledgements plus server reconciliation;
their private pending data retains a separate seven-day expiry. REST and MCP
compose `reviewservice`; global `/mcp` provides catalog discovery and stored
diff/patch retrieval with explicit context IDs.

See [plugins.md](plugins.md) for native harness installation, asynchronous
checkout sync, configuration precedence and hook diagnostics. Normal builds and
checks compile the adapters; host SDK dependencies are development types only.
Pi and OpenCode's self-contained `dist/index.js` artifacts are committed so a
clone is ready to install without a build. After changing adapter source, run
`mise run plugins:build` and commit the regenerated artifacts. Codex and Claude
load their checked-in hook definitions through the root marketplace manifests.
