# Architecture

servediff is a polyglot monorepo with a Go daemon and a React web application.
Node is a frontend build and test dependency only. One daemon per user owns the
database and serves independently addressed worktrees and piped captures.

## Runtime and data ownership

Ordinary CLI commands ensure the daemon, submit an absolute worktree path or a
bounded raw patch, print/open the returned context URL, and exit. One process
contains the collector, SQLite store, and API/UI server. Registration resolves
repository identity without collecting diffs or discovering other worktrees.
The collector reads Git on demand; API sources read committed SQLite versions.
The same process parses captures and writes reviews. `service`
commands manage its lifetime; `serve` composes the same application in a
foreground process for fixtures and supervision.

A context is a selectable review target. Worktree contexts produce updated
all/staged/unstaged diffs; capture contexts contain one immutable patch/version.
Both support editable reviews. Linked worktrees share repository grouping but
have separate contexts. Repeated registration reuses a worktree; independent
pipe invocations create independent captures with 14-day expiry. Captures have
no automatic repository association.

The durable catalog survives restarts; runtime sources are reconstructed lazily.
Catalog listing does not run Git. Missing repositories fail within their own
context. Existing location, diff, version, and review IDs are preserved by the
additive schema-4 migration. Unsupported older schemas fail without deletion.

Repository selection requests `/api/v2/repositories/{id}/worktrees`; discovery
runs only for that repository, without comparing files. Selecting a worktree or
scope requests its current diff. The API awaits the collector, which collects
that scope's manifest, patches, and available full-file contents, verifies the
revision did not change during collection, and publishes them atomically in
SQLite. The API then queries SQLite. File/review reads never scan Git.
Concurrent collection for one context/scope shares work; cancellation of one
HTTP caller does not cancel shared collection. Collection has a 30-second
deadline, a 16 MiB preview budget, and one retry for concurrent edits.
Current/review-pinned versions survive pruning; five recent unreferenced live
versions are retained per scope.

`servediff change` uses authenticated local control to identify one registered
repository/worktree, increment its persisted generation, and send an SSE event.
Optional branch/detail are metadata. It performs no diff scan, starts no service,
and registers no repository. An open browser refreshes its selected target/scope;
drafts defer reloads. Removed worktrees keep reviews and fail on refresh.
SQLite catalog reads, dormant worktrees, and SSE heartbeats perform no Git work.
No filesystem watchers or periodic diff/catalog/comment polling run. Without
hooks, navigation/manual Refresh supplies freshness. Reconnect refreshes selected
data.

`~/.config/servediff/config.json` holds host, port, state, and webDir. Defaults are
written on first use; config set/get/remove validates settings. Startup precedence
is defaults, saved config, legacy explicit flags, then start/restart JSON
`--config`. Overrides are temporary; restart without them reapplies saved config.
`SERVEDIFF_CONFIG_PATH` overrides the path for isolation; setting
`SERVEDIFF_RUNTIME_DIR` also isolates config alongside runtime files.

Every REST/MCP operation resolves an explicit context once and verifies resource
membership. There is no daemon-wide active repository. Browser selection is
client state, represented by `/contexts/{id}`. Scoped REST routes live under
`/api/v2/contexts/{id}`; scoped MCP uses `/mcp/contexts/{id}`. Legacy unscoped
routes fail with an ambiguity error when daemon mode has multiple contexts.

Private lifecycle/registration traffic uses an authenticated loopback control
listener, separate from the web listener. A private runtime descriptor carries
discovery credentials and effective settings; it is not saved configuration.
Lifecycle and lifetime locks prevent competing starts. Database ownership also
protects foreground processes using the same state file. Old binaries must be
stopped before migration because they do not honor these locks.

Service discovery returns authenticated status and its control connection
together. Submissions stay bound to that instance; output uses its URLs. A lost
acknowledgement permits one replay with the original submission ID, only after
verifying the accepted settings and durable state identity (the existing user
ID). Recovery cannot redirect input to a replaced database or restarted memory
store. The complete operation has a two-minute deadline; discovery and recovery
each have a 30-second bound. Ambiguous failures retain the original error and
identify the database where submission may have committed.

Git admission limits running and queued commands. Each command has a 20-second
deadline including queue time, plus at most one second for pipe draining.
Platform adapters own subprocess cleanup: process groups on Linux/macOS and
jobs assigned during process creation on Windows. Cancellation reaps the Git
process, cleans up its owned children, and releases admission.

The public REST/MCP trust model remains unauthenticated. Non-loopback binding
exposes all registered contexts. Control authentication does not authenticate
the web listener. No idle service shutdown or automatic conflicting-setting
restart is performed.

## Project boundaries

- `apps/web`: React/Vite UI. It consumes the REST contract, never Go packages.
- `cmd/servediff`: Go composition root and CLI only.
- `internal`: Go application behavior and private adapters shared by future Go commands.
- `packages/api`: canonical OpenAPI contract, generated TypeScript client, and Go embedding shim.
- `packages/shared`: TypeScript-only review and UI behavior; not a cross-language model package.
- `test/fixtures`: serialized inputs shared across implementations.

Go CLI, control HTTP, REST, and MCP compose shared application services over
source and store packages. Transports must not call each other. Cross-language
sharing happens through OpenAPI and serialized fixtures, not source imports.

- `internal/contextservice`: durable catalog, registration, capture submissions,
  and context resolution.
- `internal/reviewservice`: shared review operations.
- `internal/collector`: scoped collection, coalescing, atomic publication, and
  SQLite-backed API sources.
- `internal/config`: saved settings and temporary override merging.
- `internal/reviewstore`: migrations, context identities, submission
  deduplication, and review persistence.
- `internal/httpapi`: public REST adapters and the shared context snapshot
  manager, including coalescing and cache ownership.
- `internal/daemon`: lifecycle coordination, discovery, detachment, and server
  composition.
- `internal/controlapi`: authenticated local control protocol and its client.
- `internal/processlock`: platform locks for lifecycle and database ownership.
- `internal/session`, `internal/diffsource`: runtime capability resolution and
  Git/patch source behavior.

## Production builds

The build generates the API client, builds the web application, stages its
output below `internal/webui`, then embeds it in `dist/servediff`. Node and pnpm
are build dependencies, not binary runtime dependencies. Live repository mode
still requires the Git executable.

## Dependency rules

- Deployable applications do not import other applications.
- Go commands contain wiring only; reusable Go code stays in `internal`.
- API wire types originate in OpenAPI.
- Shared packages must have a specific purpose and a real second consumer.
- Add another Go module only for an independently versioned/released component.

## Capability-driven sessions

Diff sources declare technical support; session resolution combines it with
application policy. The API advertises the resulting capabilities, and both the
server and web UI enforce them. Source kind is provenance and presentation
metadata, never a behavioral feature check.
