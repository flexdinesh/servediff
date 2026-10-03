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

Run from a Git repository:

```sh
servediff
# Also: servediff . or servediff --path /path/to/repo
```

Register another repository or choose the service address:

```sh
servediff /path/to/repo
servediff . --port 4000
servediff service config set host 0.0.0.0
servediff service restart
servediff --version
```

Commands start or reuse one background service per user, register their input,
open its browser URL, and exit. Choose a repository, then its worktree in the
top bar. Worktrees are discovered only for the selected repository. Piped diffs
remain directly selectable. Repeated registration preserves review IDs.

By default, the service binds to `127.0.0.1` and uses the first available port
from 7981 through 7990. Manage its lifetime explicitly:

```sh
servediff service start
servediff service status
servediff service status --json
servediff service stop
servediff service config set port 4000
servediff service config get host
servediff service config remove port
servediff service restart --config '{"port":4000}'
```

`service start` starts an empty service and prints its status without opening a
browser. The service stays running after terminal closure and has no idle shutdown.
`--no-browser` suppresses browser opening for that invocation. Listener, state,
and asset settings supplied explicitly must match a running service; conflicting
settings require `service restart`.

First registration or service start creates `~/.config/servediff/config.json`.
Settings: `host` (default `127.0.0.1`), `port` (`null` selects 7981–7990; `0`
requests an ephemeral port), `state` (SQLite path, or `memory`), and `webDir`
(empty uses embedded assets). `config remove KEY` restores its default.
Config edits apply on the next start/restart. Start/restart `--config` merges
temporary JSON overrides over saved settings; overrides are never saved.
Restart without overrides reapplies saved config. Legacy `--port`, `--state`,
and `--web-dir` remain supported. Public `--host` is replaced by
`service config set host VALUE`.

If acknowledgement is lost, the CLI retries once against the same state and
settings. It refuses to replay into a changed database or restarted memory
store. When recovery fails, the error identifies where data may have been saved.

Setting `host` to `0.0.0.0` exposes every registered context through the
unauthenticated web server on every network interface and accepts any HTTP host
name. Use it only on a trusted network.

## Reviewing changes

Switch between all, staged, and unstaged changes. Use the file tree to navigate,
mark files as reviewed, and select **+** beside a line to comment. **Copy
unresolved** and **Copy all** export review comments as agent-ready XML.

Comments and reviewed-file marks persist by worktree or capture. Display preferences stay
in the browser.

Live diffs load on navigation or **Refresh**. No repository watchers or background
diff polling run. Agent hooks can announce a change in the current checkout:

```sh
servediff change
servediff change --path /path/to/worktree --branch feature --detail "Agent finished"
```

The hook requires a running service and registered repository; it does not start
a service or register a repository. Branch/detail are notification metadata.
Only an open view reloads its selected worktree/scope; dormant worktrees stay idle.
Drafts defer automatic reloads. Without hooks, navigate or refresh to see edits.

## Piped diffs

Register a fixed Git patch alongside live repositories:

```sh
git diff | servediff
git show | servediff
git show main..HEAD~1 | servediff
servediff - < saved.patch
```

Piped input takes priority over a directory argument. Each invocation creates a
separate immutable capture, selectable alongside worktrees. It does not refresh
and is not automatically associated with the submission directory's repository.
Its reviews remain editable. The CLI prints the capture ID and URL. Reopen it
within 14 days with
`servediff --capture <id>`. Captures and their reviews expire 14 days after
creation. Standard Git patches are limited to 16 MiB total and 2 MiB
per file. Combined merge diffs are shown against the first parent. Use
`git show --diff-merges=separate` to review the result against every parent.

## API and data

The OpenAPI 3.1 contract is served at `/openapi.yaml`. `/api/v2/contexts` lists
known worktree contexts and captures. `/api/v2/repositories` reads registered
repositories; `/api/v2/repositories/{id}/worktrees` discovers the selected repo's
worktrees. `/api/v2/events` delivers change/review notifications. Review operations
use `/api/v2/contexts/{id}/...`.
Legacy unscoped `/api/v1` review routes require exactly one context in daemon
mode; multiple contexts return `409 context_required`. Compatible coding agents
can access review-comment tools through the MCP `2026-07-28` Streamable HTTP
endpoint at `/mcp/contexts/{id}`, printed by the CLI; see
[docs/mcp.md](docs/mcp.md).

A compatible browser can expose the same tools to its browser agent through
WebMCP while the servediff page is open; see
[docs/webmcp.md](docs/webmcp.md).

The API is unauthenticated. The process account is the current user for all
browser and MCP requests. Browser same-origin and cross-site checks remain, but
anyone who can reach the server can access that user's review data.

Review data is stored in `$XDG_STATE_HOME/servediff/state.db`, or
`~/.local/state/servediff/state.db` when `XDG_STATE_HOME` is unset. Schema-2
databases migrate without changing review IDs. Unsupported schemas and legacy
review JSON are rejected with existing data preserved. Stop old foreground
servediff processes before upgrading: they do not honor the new database
ownership lock.
`--state memory` keeps contexts and reviews only until that service stops.
`/api/v1/diffs/captures` remains available for retained captures.

Service lifecycle and registration use a separate authenticated loopback control
listener. Its token is private to the local user; it does not authenticate the
web API. Use `servediff serve ...` for an explicit foreground process, stopped
with **Ctrl+C**.

For source setup, development commands, and architecture, see
[docs/development.md](docs/development.md). Maintainers can find publishing
instructions in [docs/release.md](docs/release.md).
