# servediff

Review local Git changes in your browser without modifying your working tree or
index.

## Install

With Homebrew:

```sh
brew install flexdinesh/tap/servediff
```

Latest stable release with Go 1.25 or later:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@latest
```

Or install a specific stable version:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@v0.1.1
```

Or install the latest development changes from `main`:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@main
```

## Usage

Collect a Git checkout and open its committed observation:

```sh
servediff review
servediff review --path /path/to/repo
servediff review --no-browser
servediff --version
```

Each invocation collects once and creates an independent observation, including
repository, branch, worktree, HEAD, hostname and source metadata. The local
server starts automatically when needed and stays running after the CLI exits.
Bare `servediff` prints help. There is no watcher and opening the dashboard does
not recollect Git data.

`review` prints the observation's `/contexts/{id}` URL and opens it when a
browser is available. SSH sessions and headless Linux sessions skip opening;
`--no-browser` also disables it. Local URLs use `localhost` by default or the
configured host IP. A `0.0.0.0` listener also prints each LAN IPv4 URL.

Manage the local server explicitly:

```sh
servediff service start
servediff service status --json
servediff service stop
servediff service restart
servediff service config set host 0.0.0.0
servediff service config get host
servediff service config remove host
servediff service restart --config '{"host":"127.0.0.1","port":4000}'
```

Defaults are created in `~/.config/servediff/config.json`; `SERVEDIFF_CONFIG_PATH`
overrides the location. Start/restart JSON overrides apply to that invocation. The default
listener binds to `127.0.0.1` and chooses a port from 7981 through 7990.
The local API is unauthenticated: a non-loopback listener exposes its review
data to anyone who can reach it.

## Agent hooks and remote ingestion

An agent hook uses the same collection operation as a manual review:

```sh
servediff review --trigger agent-hook --agent my-agent --run-id run-123 --no-browser
```

Use `--source-id` to supply a stable source/container identity. Hostnames,
branches and source-local paths are searchable metadata, not global identities.
Separate submissions remain independently reviewable, even on the same branch.

Build/install the remote server with Go:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff-server@main
servediff-server --listen 0.0.0.0:7981 --state /data/state.db --account my-account
```

Set `SERVEDIFF_TOKEN` to a secret of at least 32 bytes before starting the
remote server. API/MCP requests use bearer authentication; the browser uses
HTTP Basic with the account name and token as password. Deploy behind TLS.
The initial remote composition serves one account; multi-user authentication
and alternative storage/queue backends remain future extensions.

Collectors select that destination with `--server` or `SERVEDIFF_SERVER_URL`,
and use `SERVEDIFF_TOKEN` for authentication:

```sh
servediff review --server https://reviews.example.com --no-browser
```

Remote submission does not start a local server. The remote server needs no Git
installation or repository mount: all queries read committed SQLite data.
Plugins may instead submit the same `POST /api/v2/ingestions` request directly.

## Reviewing changes

Select a repository, then an observation. Search/filter stored metadata to find
a branch, worktree, host, source or agent run. Switch between its collected all,
staged and unstaged scopes. Use the file tree to navigate, mark files as reviewed
and comment on lines. **Copy unresolved** and **Copy all** export comments as
agent-ready XML.

Observations are immutable. A later submission creates another observation;
it never relabels or refreshes an earlier diff. Comments and reviewed marks are
stored in SQLite. Display preferences remain in the browser.

## Piped diffs

Submit a fixed patch explicitly:

```sh
git diff | servediff pipe
git show | servediff pipe
servediff pipe --path /path/to/repo < saved.patch
```

Pipe uses the same ingestion protocol as review and agent hooks. It captures
Git metadata when its directory is a checkout; otherwise it records an
unassociated observation. Piped diffs have only the all scope and do not
include full file contents. Standard Git patches are limited to 16 MiB total
and 2 MiB per file. Combined merge diffs are shown against the first parent.
Use `git show --diff-merges=separate` to review every parent separately.

## API and data

The OpenAPI contract is served at `/openapi.yaml`. `POST /api/v2/ingestions`
validates and atomically commits an observation; acknowledgement means commit,
not collection scheduled. Replaying the same source/submission identity and
payload returns its original receipt. Reusing it with different payload fails.
There is no durable upload queue in the collector; failures are reported.

`/api/v2/contexts` lists stored observations with `q`, `repository`, `branch`,
`worktree`, `hostname`, `sourceId` and `runId` filters. Scoped review operations
use `/api/v2/contexts/{id}/...`; `/api/v2/events` sends ingestion notifications.
Latest means latest received, not guaranteed current filesystem state.

Compatible coding agents use the printed `/mcp/contexts/{id}` URL; see
[docs/mcp.md](docs/mcp.md). A compatible browser can expose the same tools through
WebMCP while the page is open; see [docs/webmcp.md](docs/webmcp.md).

Local data lives in `$XDG_STATE_HOME/servediff/state.db`, or
`~/.local/state/servediff/state.db`. `--state memory` disables persistence.
Migrations preserve historical reviews and retained captures. Legacy worktree
contexts cannot load Git through the server; submit a new observation with
`servediff review`. Unsupported databases are rejected without deleting data.
Stop older processes before upgrading. Local service lifecycle uses a private
authenticated loopback control listener; its token does not authenticate the
local public API.

For development and architecture, see [docs/development.md](docs/development.md)
and [docs/architecture.md](docs/architecture.md). Publishing instructions:
[docs/release.md](docs/release.md).
