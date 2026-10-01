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
servediff .
```

You can also serve another repository or choose the address:

```sh
servediff /path/to/repo
servediff . --port 4000
servediff . --host 0.0.0.0
servediff --version
```

By default, servediff binds to `127.0.0.1`, opens a browser, and uses the first
available port from 7981 through 7990. Stop it with **Ctrl+C**.

Passing `--host 0.0.0.0` exposes the unauthenticated server on every network
interface and accepts any HTTP host name. Use it only on a trusted network.

## Reviewing changes

Switch between all, staged, and unstaged changes. Use the file tree to navigate,
mark files as reviewed, and select **+** beside a line to comment. **Copy
unresolved** and **Copy all** export review comments as agent-ready XML.

Comments and reviewed-file marks persist by worktree or capture. Display preferences stay
in the browser.

## Piped diffs

Serve a fixed Git patch instead of a live repository:

```sh
git diff | servediff
git show | servediff
git show main..HEAD~1 | servediff
servediff - < saved.patch
```

Piped input takes priority over a directory argument. Re-run the command to
refresh a piped diff. Each run creates a capture with a distinct ID, printed at
startup when persistence is enabled. Reopen it within 14 days with
`servediff --capture <id>`. Captures and their reviews expire 14 days after
creation. Standard Git patches are limited to 16 MiB total and 2 MiB
per file. Combined merge diffs are shown against the first parent. Use
`git show --diff-merges=separate` to review the result against every parent.

## API and data

The OpenAPI 3.1 contract is served at `/openapi.yaml`; REST endpoints are under
`/api/v1`. Compatible coding agents can access review-comment tools through the
MCP `2026-07-28` Streamable HTTP endpoint at `/mcp`; see
[docs/mcp.md](docs/mcp.md).

A compatible browser can expose the same tools to its browser agent through
WebMCP while the servediff page is open; see
[docs/webmcp.md](docs/webmcp.md).

The API is unauthenticated. The process account is the current user for all
browser and MCP requests. Browser same-origin and cross-site checks remain, but
anyone who can reach the server can access that user's review data.

Review data is stored in `$XDG_STATE_HOME/servediff/state.db`, or
`~/.local/state/servediff/state.db` when `XDG_STATE_HOME` is unset. Existing
review JSON is discarded; there is no data migration. Use `--state memory` to
disable persistence. `/api/v1/diffs/captures` lists retained captures.

For source setup, development commands, and architecture, see
[docs/development.md](docs/development.md). Maintainers can find publishing
instructions in [docs/release.md](docs/release.md).
