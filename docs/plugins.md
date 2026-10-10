# Agent plugins

Plugins target the remote server only. Configure `diffx config set server URL`
and `diffx config set token -` before enabling hooks.

Install a plugin to synchronize the latest checkout after an agent finishes.
Collection and ingestion run in a detached Go worker; the agent only schedules
work. The web UI's existing latest/stale filters cover all submitted worktrees.

## Install and remove

Install the diffx executable separately. For the current plugin contract,
use the latest development version:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx@main
```

For CLI development, use `mise run install` instead. Ensure `diffx` is on
the harness's PATH, including GUI sessions, or set `DIFFX_BINARY` to its
absolute path in the harness environment. All four adapters read that override
at runtime; they do not capture an executable path during installation.

The repository includes ready-to-use plugin artifacts. Installation requires no
pnpm, mise or build step. Use each harness's own installer;
restart active sessions after installing or updating.
Update the remote server alongside the collector to enable durable ingestion jobs.

CLI harness selection uses `--harness`; `--agent` is no longer accepted. Update
existing plugins and scripts alongside the diffx executable.

### Codex

Register the repository marketplace, then install its plugin:

```sh
codex plugin marketplace add /absolute/path/to/diffx
codex plugin add diffx@diffx
```

For a remote marketplace, replace the first command's path with
`flexdinesh/diffx`. The marketplace manifest is
`.agents/plugins/marketplace.json`. Remove the plugin with:

```sh
codex plugin remove diffx@diffx
```

### Claude Code

```sh
claude plugin marketplace add /absolute/path/to/diffx
claude plugin install diffx@diffx
```

For a remote marketplace, replace the first command's path with
`flexdinesh/diffx`. The marketplace manifest is
`.claude-plugin/marketplace.json`. Remove the plugin with:

```sh
claude plugin uninstall diffx@diffx
```

### Pi

Install the ready-built package from a checkout:

```sh
pi install /absolute/path/to/diffx/packages/plugin-pi
```

Keep that checkout at the same path: Pi loads the local package there. Remove
using the same source passed to install:

```sh
pi remove /absolute/path/to/diffx/packages/plugin-pi
```

### OpenCode

OpenCode's native installer accepts npm and Git package specs. Install the
package from this repository's `main` branch:

```sh
opencode plugin add 'github:flexdinesh/diffx#main::path:packages/plugin-opencode'
```

Remove that registration with:

```sh
opencode plugin remove 'github:flexdinesh/diffx#main::path:packages/plugin-opencode'
```

For a local checkout, add the absolute package directory to the `plugins` array
in your `opencode.jsonc` instead; the CLI does not accept local paths:

```jsonc
{
  "plugins": ["/absolute/path/to/diffx/packages/plugin-opencode"],
}
```

Preserve existing configuration and plugin entries. Keep the checkout at that
path. To remove a local install, remove only its `plugins` entry.

| Host        | Supported baseline    | Completion event                  | Installation                       |
| ----------- | --------------------- | --------------------------------- | ---------------------------------- |
| Codex       | CLI 0.160.0           | `Stop`                            | Native marketplace/plugin CLI      |
| Claude Code | 2.1.265               | `Stop`                            | Native marketplace/plugin CLI      |
| OpenCode    | 2.0.22, V2 plugin API | `session.status` with idle status | Native Git install or local config |
| Pi          | 1.0.0                 | `agent_settled`                   | Native local package install       |

Codex requires enabling the plugin and approving its hook trust review; changing
hook definitions can require another review. Host policies can disable hooks.
Claude hooks must also be enabled. OpenCode V1 plugin APIs are not supported.
Harness installers own plugin registration, updates and removal; their native
configuration variables and installation scopes apply.

## Configuration

Server and collector settings resolve in this order:

1. Code defaults.
2. JSON config at `~/.config/diffx/config.json`.
3. Environment: `DIFFX_HOST`, `DIFFX_PORT`, `DIFFX_STATE`,
   `DIFFX_WEB_DIR`, `DIFFX_SERVER_URL`, `DIFFX_TOKEN`,
   `DIFFX_RETENTION_DAYS`.
4. Explicit command flags or start/restart inline JSON overrides.

`DIFFX_CONFIG_PATH` or `--config-file FILE` selects another JSON file.
Missing files receive defaults; invalid JSON or environment values fail clearly.
No `.env` file is loaded automatically. Config commands edit file values without
persisting environment overrides. Restart the local server after changing its
settings; workers read effective settings each invocation but reuse the running
server until restarted.

```sh
diffx config set port 7981 --config-file /path/to/config.json
diffx config set server https://reviews.example.com
diffx config set retentionDays 14
# Remote config is read by each finite invocation.
```

JSON `server` and `token` select remote ingestion through Go; environment and
explicit `--server`/`--token` override them. Plugins require a nonempty remote `server` and bearer `token`; no local fallback.
Config files are private because `token` may contain a credential.
`retentionDays` is a positive whole-day server setting, default seven;
`--retention-days` overrides it for server startup. Remote collectors do not
change the remote server's retention. Existing expiries change only after a fresh
submission; retries do not extend them.
The remote server must support `/api/v2/health`; a remote failure never starts or
redirects into a local service. Remote servers preserve their public listen
default unless an explicit config file, environment or `--listen` overrides it.

## Synchronization

The worker captures committed feature-branch changes and dirty files against the
merge base with the local default branch (`origin/HEAD`, `main`, then `master`).
Staged/unstaged scopes retain their HEAD/index meanings, including untracked
files. If no default branch exists, live collection records explicit
working-tree/HEAD fallback; object-only recovery requires an available baseline.
It retains patches and bounded full before/after contents for future
full-file viewing. It does not attribute edits to a turn or agent; completion is
a trigger, and the worker may capture edits made after that trigger.

Nested hook directories resolve to the checkout root. Collection includes only
that checkout; other registered worktrees are not inspected.
Non-Git input is ignored; child repositories and branches without checkouts are
excluded. Explicit `sync --branch` can recover committed changes after worktree
removal. Discovery never fetches, switches branches or creates worktrees.
Supply the new path after moving a repository; an old path alone cannot locate
an arbitrary move. The triggering directory is stored separately from each
collected worktree's root.

Repositories, checkouts, and branches enroll persistent identities in Git
metadata. Paths, names, and remote URLs remain descriptive location hints.
Renaming a branch or moving a checkout preserves enrolled identities and lets a
later hook update the saved location. A fresh clone gets new identities; a full
filesystem copy also copies enrollment metadata. If both copies remain live,
the collector rejects the second location instead of silently merging sources;
use a separate source identity for an independent copy. Read-only Git metadata
cannot enroll safely; collection fails visibly
instead of inventing a path identity. Keep Git metadata with the repository.

Concurrent triggers coalesce by stable source/config/routing/session identity. Exact
contents, file modes, HEAD, comparison, staging and repository identity determine the fingerprint; timestamps
alone do not. A successful acknowledgement and a still-current stored context
allow unchanged uploads to be skipped after server reconciliation. Sessions keep
separate acknowledgements; a new session association is recorded even for
identical content. Initially clean checkouts are skipped when the server has no
retained stream history; otherwise collection reconciles conservatively.
Dirty-to-clean transitions submit an
empty observation, superseding the old changed one. Unknown complete content
identities are collected conservatively rather than falsely suppressed.

Workers save immutable collected payloads before checking the destination, then
health-check the configured remote destination. No local service is started.
Each worker has a one-minute deadline, at most two attempts per payload, and a
shared one-minute cooldown after failed startup. Pending immutable payloads
retain retry IDs and survive checkout removal. Retries run on later completion
triggers or explicit retry commands, without a perpetual background loop.
Server/database identity prevents ambiguous requests from being replayed into a
replacement database.

Private hook state defaults to `~/.local/state/diffx/hooks-v4`; `XDG_STATE_HOME`
and isolated `DIFFX_RUNTIME_DIR` are respected. Pending data expires after
seven days independently of server retention and is pruned toward a 256 MiB budget; active uploads are protected.
Acknowledgements refresh after 24 hours so unchanged observations retain their
server availability. Diagnostics stay in `hooks.log`, bounded to approximately
128 KiB per log file, with one rotated backup. JSONL activities include discovery
paths, stable identities, branch/base/HEAD, collection counts, ingestion attempts,
acknowledgements, waiting state, skip reasons, and errors. Job `status.json` files
retain the latest state. Logs exclude payload contents and credentials; pending
files necessarily contain collected diffs and are private. Hooks never open a
browser or write diagnostics into the conversation.

Inspect and recover queued work:

```sh
diffx collector status
diffx collector retry
tail -n 50 ~/.local/state/diffx/hooks-v4/hooks.log
diffx hook --harness codex --retry
```

Status prints JSON. Retry schedules a finite pass over incomplete work for the
current config and routing environment; it does not change destinations. Use the
same `--config-file` and environment as the original hook. `waiting` identifies
an unresolved source or incomplete delivery; inspect its error and pending files.
An `ingestion` stage with `complete` status and a context ID confirms a committed
server context. Manual-command logs use `acknowledged` for the same outcome.

Manually recover committed changes even when another branch is checked out:

```sh
diffx sync --path /path/to/repository --branch feature --base main --no-browser
diffx sync --path /path/to/checkout --base HEAD --no-browser
```

Manual sync defaults to auto and collects only the selected checkout. `--print`
returns the configured server URL. `--base HEAD` selects working changes only.
`--branch` reads only the named branch's Git objects, defaults its
base to auto, and ignores the current checkout's dirty files. Deleted uncommitted
files cannot be recovered unless a payload was captured first. Renamed/deleted
refs also require surviving commits or a previously saved payload.

Manually invoke the hook handler for any supported harness:

```sh
diffx hook --harness codex --path /path/to/checkout
diffx hook --harness pi --path /path/to/checkout --run-id session-id --session-name 'Feature work'
```

Supported harnesses are `codex`, `claude`, `opencode`, and `pi`. `--run-id` is
optional session metadata for logs and search; `--session-name` adds an optional
label. Neither claims ownership of edits. With `--path` and no `--run-id`, the
run ID stays empty. Sessions associate with shared reviews; reused snapshots
retain original metadata, comments and reviewed marks. REST filters `harness`,
`sessionId`, `sessionName` and legacy `runId` query submission associations.
Pi supplies its session manager's optional name. OpenCode remembers names from
session-created/renamed events and includes them at completion; an already-running
session may have no name until such an event arrives.

Without `--path`, Codex/Claude command hooks provide a JSON event with `cwd` and
`session_id` on stdin; optional `session_name` supplies the name. Explicit
`--run-id`/`--session-name` override the event. To simulate that input:

```sh
printf '%s\n' '{"cwd":"/path/to/checkout","session_id":"session-id","session_name":"Feature work"}' | diffx hook --harness codex
```

Hooks default to `--base auto`, never open a browser, and return after scheduling.
Scheduling success does not confirm ingestion; hooks log failures and exit
successfully. Inspect the dashboard or hook log for delivery state. Manual
`diffx sync` waits for receipts and reports failures directly. Both manual sync
and hooks skip unchanged uploads only after acknowledgement and server freshness
checks; new session metadata is submitted. Manual and hook collection use the
same auto baseline.
