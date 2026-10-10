# diffx

Review local Git changes in your browser without modifying your working tree or
index.

## Install

With Homebrew:

```sh
brew install flexdinesh/tap/diffx
```

Latest stable release with Go 1.25 or later:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx@latest
```

Or install a specific stable version:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx@v0.2.0
```

Or install the latest development changes from `main`:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx@main
```

## Usage

Capture the selected checkout once in one foreground process:

```sh
diffx
diffx .
diffx /path/to/repo --host 0.0.0.0
diffx . --replace
diffx . --base HEAD --no-browser
```

Collection and ingestion run in-process. The same process serves the web UI,
REST and MCP until Ctrl-C. The path defaults to the current directory and resolves
to its containing checkout, including linked worktrees. Only that checkout is
collected. The browser opens the captured snapshot; later edits and branch switches
do not change it. Run the command again to capture a newer diff. Observations and
comments persist across restarts in the shared local database. Failed collection
never substitutes an empty diff.

Only one local instance runs. Starting another prompts before replacing it;
scripts must pass `--replace`. Replacement requests authenticated graceful
shutdown and waits for database ownership. Unreachable instances are not killed
using an unverified PID.

Publish once to a remote server, including every registered worktree:

```sh
diffx config set server https://reviews.example.com
diffx config set token - < /path/to/private-token
diffx sync
diffx sync --print
diffx sync --debug
diffx sync --retry
```

Sync collects only the selected checkout and waits for pending observations to
commit. Unchanged uploads are skipped after server freshness checks. `--print`
prints only the configured server URL on success; progress/errors use stderr. `--debug` uses one updating
terminal line, or plain lines when redirected. Failed uploads remain private,
immutable pending submissions; `sync --retry` recovers them without the checkout.
Cancelling a wait does not cancel an accepted server job.

`diffx .` and piped diffs always run locally, even with remote config. Sync
and plugins require a remote URL and bearer token; they never bootstrap a local
server.

The all scope includes branch changes from the local default branch's merge base
plus working changes. Staged/unstaged retain HEAD/index semantics.
`--base HEAD` selects working changes only. Collection never fetches Git refs.
Matching captures reuse review state. Retention defaults to seven days after the
last fresh submission; retries do not extend it.

Defaults live in `~/.config/diffx/config.json`; `--config-file` or
`DIFFX_CONFIG_PATH` overrides the location. Precedence: defaults, JSON,
environment, explicit flags. `diffx config {set|get|remove}` edits persisted
settings. Token reads are masked; `set token -` reads stdin.

The default listener binds to `127.0.0.1` on an available port from 7981–7990.
`--port 0` chooses an OS-assigned port. A non-loopback listener exposes local
reviews to reachable clients. The browser opens a loopback URL for wildcard binds.

Use `diffx --help` for usage. Legacy `review`, `serve`, `service` and
`capture` commands are removed. Development fixtures use `diffx dev --fixture`.

## Agent hooks and remote ingestion

Install Codex, Claude Code, OpenCode or Pi plugins to synchronize each
worktree automatically after agent completion:

```sh
codex plugin marketplace add flexdinesh/diffx
codex plugin add diffx@diffx
```

Configure a remote server and token first. Plugins are remote-only.
The plugin returns after scheduling; a detached Go worker collects only the
triggering checkout using the same branch comparison as manual review.
Non-Git directories are ignored. Child repositories and branches without live
checkouts are excluded; use `sync --branch` for explicit object-only recovery.
Persistent Git identities survive checkout moves and branch renames.
Initially clean automatic captures are skipped. Dirty-to-clean transitions and
new session associations are submitted. Unchanged uploads are skipped, overlapping requests
coalesce, and failures stay outside the agent conversation. See
[plugin setup](docs/plugins.md) for native installation on all hosts,
configuration and removal. Plugins are ready to install from the repository;
the diffx executable is installed separately and must be available to the
harness through PATH or `DIFFX_BINARY`.

Inspect collection/delivery activity and retry waiting payloads:

```sh
diffx collector status
diffx collector retry
tail -n 50 ~/.local/state/diffx/hooks-v4/hooks.log
diffx sync --branch feature --base main --no-browser
```

Manually schedule the same detached collection used by harness plugins:

```sh
diffx hook --harness codex --path /path/to/repo
```

To collect synchronously with harness metadata:

```sh
diffx sync --trigger agent-hook --harness codex --run-id run-123 --session-name 'Feature work' --no-browser
```

Use `--source-id` to supply a stable source/container identity. Hostnames,
branches and source-local paths are searchable metadata, not global identities.
Different sources, checkouts, branches, HEADs, or diff contents remain
independently reviewable. Identical content from the same checkout reuses its
review context. Sessions associate with shared reviews; they do not own or
claim authorship of every worktree's edits. Harness, session ID and optional
session name remain searchable even when identical content reuses a review.

Build and run the container setup for server development:

```sh
mise run docker:up
mise run docker:down
```

Compose publishes HTTP on `127.0.0.1:7981` and preserves the named SQLite volume
when stopped or recreated. Set `DIFFX_PORT=7982` to use another port. Retrieve
the initial credential with `docker compose cp server:/data/state.db.admin-token ./admin-token`.
See [server development](docs/development.md#server-development) for configuration
and persistence details. Deploy behind HTTPS when exposing the server remotely.

Or build and run the image directly:

```sh
mise run docker:build
docker run --name diffx-server --read-only -p 127.0.0.1:7981:7981 \
  --mount source=diffx-data,target=/data diffx-server:local
```

The volume contains observations, jobs and credentials. First
startup writes the admin token to `/data/state.db.admin-token`; copy it with
`docker cp diffx-server:/data/state.db.admin-token ./admin-token`.
The image runs without Git or a repository mount, as an unprivileged user.
SIGTERM drains HTTP and stops the worker; durable unfinished jobs recover after
their leases expire. Back up the database while stopped.

Build/install the remote server with Go:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx-server@main
DIFFX_HOST=127.0.0.1 DIFFX_PORT=7981 DIFFX_STATE=./test-data/state.db diffx-server
```

First startup creates `admin` with a generated token saved privately at
`<database>.admin-token`; startup prints its path. The token persists across
restarts. API/MCP requests use bearer authentication; the browser uses HTTP Basic
with the username and token as password. Deploy behind TLS. Multiple individual
users share one SQLite database; credentials select and isolate each user's data.
The database stores credential hashes. To add a user, stop the server, run:

```sh
diffx-server user create --name alice --state /data/state.db
```

Save the printed token, then restart the server. `--account` / `DIFFX_ACCOUNT` and `DIFFX_TOKEN`
can supply initial bootstrap credentials; existing credentials are never replaced.
Use `--retention-days 14` or config/environment to change server retention.
The server defaults to `127.0.0.1:7981` and `./data/state.db`, independent of
personal CLI settings. Its image defaults to `0.0.0.0:7981` and `/data/state.db`;
env vars override image defaults, and explicit flags override env vars.

Collectors select that destination with config `server`, `DIFFX_SERVER_URL`
or `--server`, and authenticate with config `token`, `DIFFX_TOKEN` or `--token`:

```sh
diffx sync --server https://reviews.example.com
```

Remote submission does not start a local server. The remote server needs no Git
installation or repository mount: all queries read committed SQLite data.
Other producers can submit the same `POST /api/v2/ingestions` contract directly;
the bundled plugins invoke the Go collector.

## Reviewing changes

Select a repository, then an observation. Search/filter stored metadata to find
a branch, worktree, host, source or agent run. Switch between its collected all,
staged and unstaged scopes. Use the file tree to navigate, mark files as reviewed
and comment on lines. **Copy unresolved** and **Copy all** export comments as
agent-ready XML.

Observations are immutable. New content creates another observation; matching
submissions associate new provenance with an existing review without changing
its captured diff. Comments and reviewed marks are
stored in SQLite. Display preferences remain in the browser.

## Piped diffs

Review a fixed patch in a foreground local process:

```sh
git diff | diffx
git show | diffx
diffx < saved.patch
```

The process serves the UI, REST and MCP until Ctrl-C, without watching a checkout.
Piped diffs ignore remote configuration. `diffx -` explicitly selects stdin;
combining redirected stdin with a path argument or `--path` is rejected.
Collection does not inspect Git or attach repository identity.
Piped diffs have only the all scope and do not
include full file contents. Standard Git patches are limited to 16 MiB total
and 2 MiB per file. Combined merge diffs are shown against the first parent.
Use `git show --diff-merges=separate` to review every parent separately.

## API and data

The OpenAPI contract is served at `/openapi.yaml`. `POST /api/v2/ingestions`
validates and atomically commits an observation; acknowledgement means commit,
not collection scheduled. Replaying the same source/submission identity and
payload returns its original receipt. Reusing it with different payload fails.
Hooks persist bounded immutable payloads before delivery and retry after later
triggers or `diffx collector retry`. Their pending-data retention remains
seven days independently of server retention. Manual commands always submit and
report failures directly.

Collectors and servers require ingestion protocol 4; upgrade them together.
Public v1 routes and synchronous ingestion are removed.

`/api/v2/contexts` lists stored observations with `q`, `repository`, `branch`,
`worktree`, `hostname`, `sourceId`, `runId`, `harness`, `sessionId` and `sessionName`
filters. Session filters search submission associations. Scoped review operations
use `/api/v2/contexts/{id}/...`; `/api/v2/events` sends ingestion notifications.
Latest means the most recently collected submission for each source, repository,
checkout, branch and comparison policy; arrival order breaks ties. It does not guarantee current
filesystem state. Older snapshots stay stale when newer reviews expire.

Compatible coding agents can discover stored reviews through `/mcp` or use the
printed `/mcp/contexts/{id}` URL; see
[docs/mcp.md](docs/mcp.md). A compatible browser can expose the same tools through
WebMCP while the page is open; see [docs/webmcp.md](docs/webmcp.md).

Local data lives in `$XDG_STATE_HOME/diffx/state.db`, or
`~/.local/state/diffx/state.db`. `--state memory` disables persistence.
Current schema 9 state persists across restarts. Older, newer or unrecognized
databases are refused without an automatic reset. To start fresh, stop the
server, back up its state directory, and explicitly choose a new database path.
Legacy commands and pending producer payloads are retired.
Stop older processes before upgrading. Local service lifecycle uses a private
authenticated loopback control listener; its token does not authenticate the
local public API.

For development and architecture, see [docs/development.md](docs/development.md)
and [docs/architecture.md](docs/architecture.md). Publishing instructions:
[docs/release.md](docs/release.md).
