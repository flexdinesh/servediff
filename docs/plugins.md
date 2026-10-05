# Agent plugins

Install a plugin to synchronize the latest checkout after an agent finishes.
Collection and ingestion run in a detached Go worker; the agent only schedules
work. The web UI's existing latest/stale filters cover all submitted worktrees.

## Install and remove

Install the servediff executable separately. For the current plugin contract,
use the latest development version:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@main
```

For CLI development, use `mise run install` instead. Ensure `servediff` is on
the harness's PATH, including GUI sessions, or set `SERVEDIFF_BINARY` to its
absolute path in the harness environment. All four adapters read that override
at runtime; they do not capture an executable path during installation.

The repository includes ready-to-use plugin artifacts. Installation requires no
pnpm, mise or build step. Use each harness's own installer;
restart active sessions after installing or updating.
Restart the local servediff service with the updated binary too; older running
servers cannot accept the new branch identity metadata.

CLI harness selection uses `--harness`; `--agent` is no longer accepted. Update
existing plugins and scripts alongside the servediff executable.

### Codex

Register the repository marketplace, then install its plugin:

```sh
codex plugin marketplace add /absolute/path/to/servediff
codex plugin add servediff@servediff
```

For a remote marketplace, replace the first command's path with
`flexdinesh/servediff`. The marketplace manifest is
`.agents/plugins/marketplace.json`. Remove the plugin with:

```sh
codex plugin remove servediff@servediff
```

### Claude Code

```sh
claude plugin marketplace add /absolute/path/to/servediff
claude plugin install servediff@servediff
```

For a remote marketplace, replace the first command's path with
`flexdinesh/servediff`. The marketplace manifest is
`.claude-plugin/marketplace.json`. Remove the plugin with:

```sh
claude plugin uninstall servediff@servediff
```

### Pi

Install the ready-built package from a checkout:

```sh
pi install /absolute/path/to/servediff/packages/plugin-pi
```

Keep that checkout at the same path: Pi loads the local package there. Remove
using the same source passed to install:

```sh
pi remove /absolute/path/to/servediff/packages/plugin-pi
```

### OpenCode

OpenCode's native installer accepts npm and Git package specs. Install the
package from this repository's `main` branch:

```sh
opencode plugin add 'github:flexdinesh/servediff#main::path:packages/plugin-opencode'
```

Remove that registration with:

```sh
opencode plugin remove 'github:flexdinesh/servediff#main::path:packages/plugin-opencode'
```

For a local checkout, add the absolute package directory to the `plugins` array
in your `opencode.jsonc` instead; the CLI does not accept local paths:

```jsonc
{
  "plugins": ["/absolute/path/to/servediff/packages/plugin-opencode"],
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

### Migrating from the repository installer

Remove old registration before installing the native packages to avoid duplicate
completion triggers:

- Codex: run `codex plugin remove servediff@servediff-local`, then
  `codex plugin marketplace remove servediff-local`.
- Pi: use `pi list` to find the old private servediff package source, then
  `pi remove` with that source.
- Claude Code: remove the old `~/.claude/skills/servediff` directory only if its
  `.servediff-local-install` marker identifies it as servediff's managed install.
  Respect `CLAUDE_CONFIG_DIR` if set.
- OpenCode: remove the old `~/.config/opencode/plugins/servediff.js` only if its
  `// servediff managed local plugin` header identifies the old installer output.
  Respect `XDG_CONFIG_HOME` if set.

Leave other host files and plugins intact. The old `plugins:install` and
`plugins:remove` mise tasks are no longer used.

## Configuration

Server and collector settings resolve in this order:

1. Code defaults.
2. JSON config at `~/.config/servediff/config.json`.
3. Environment: `SERVEDIFF_HOST`, `SERVEDIFF_PORT`, `SERVEDIFF_STATE`,
   `SERVEDIFF_WEB_DIR`.
4. Explicit command flags or start/restart inline JSON overrides.

`SERVEDIFF_CONFIG_PATH` or `--config-file FILE` selects another JSON file.
Missing files receive defaults; invalid JSON or environment values fail clearly.
No `.env` file is loaded automatically. Config commands edit file values without
persisting environment overrides. Restart the local server after changing its
settings; workers read effective settings each invocation but reuse the running
server until restarted.

```sh
servediff service config set port 7981 --config-file /path/to/config.json
servediff service restart --config-file /path/to/config.json
```

`SERVEDIFF_SERVER_URL` and `SERVEDIFF_TOKEN` select remote ingestion through Go.
The remote server must support `/api/v2/health`; a remote failure never starts or
redirects into a local service. Remote servers preserve their public listen
default unless an explicit config file, environment or `--listen` overrides it.

## Synchronization

The worker captures committed feature-branch changes and dirty files against the
merge base with the local default branch (`origin/HEAD`, `main`, then `master`).
Staged/unstaged scopes retain their HEAD/index meanings, including untracked
files. If no default branch exists, live collection falls back to HEAD and logs
the reason; object-only recovery requires an available baseline.
It retains patches and bounded full before/after contents for future
full-file viewing. It does not attribute edits to a turn or agent; completion is
a trigger, and the worker may capture edits made after that trigger.

Nested hook directories resolve to the checkout root. A workspace directory is
searched at most two levels and 128 directories, reading at most 128 entries per
directory and excluding hidden directories,
`node_modules`, and `vendor`. Discovery then includes registered live worktrees
and up to 128 unmerged local branches without live worktrees. Removed worktrees
can therefore recover committed branch changes from Git objects. It never
fetches, checks out branches, creates worktrees, or searches the home directory
globally. Supply the new path after moving a repository outside the supplied
workspace; an old path alone cannot locate an arbitrary move.

Repositories, checkouts, and branches enroll persistent identities in Git
metadata. Paths, names, and remote URLs remain descriptive location hints.
Renaming a branch or moving a checkout preserves enrolled identities and lets a
later hook update the saved location. A fresh clone gets new identities; a full
filesystem copy also copies enrollment metadata. If both copies remain live,
the collector rejects the second location instead of silently merging sources;
use a separate source identity for an independent copy. Read-only Git metadata
cannot enroll safely; collection fails visibly
instead of inventing a path identity. Keep Git metadata with the repository.

Concurrent triggers coalesce by stable source/config/routing identity. Exact
contents, file modes, HEAD, staging and repository identity determine the fingerprint; timestamps
alone do not. A successful acknowledgement and a still-current stored context
allow unchanged uploads to be skipped. Dirty-to-clean transitions submit an
empty observation, superseding the old changed one. Unknown complete content
identities are collected conservatively rather than falsely suppressed.

Workers save immutable collected payloads before checking the destination, then
health-check the destination and start the local service if stopped.
Each worker has a one-minute deadline, at most two attempts per payload, and a
shared one-minute cooldown after failed startup. Pending immutable payloads
retain retry IDs and survive checkout removal. Retries run on later completion
triggers or explicit retry commands, without a perpetual background loop.
Server/database identity prevents ambiguous requests from being replayed into a
replacement database.

Private hook state defaults to `~/.local/state/servediff/hooks`; `XDG_STATE_HOME`
and isolated `SERVEDIFF_RUNTIME_DIR` are respected. Pending data expires after
seven days and is pruned toward a 256 MiB budget; active uploads are protected.
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
servediff collector status
servediff collector retry
tail -n 50 ~/.local/state/servediff/hooks/hooks.log
servediff hook --harness codex --retry
```

Status prints JSON. Retry schedules a finite pass over incomplete work for the
current config and routing environment; it does not change destinations. Use the
same `--config-file` and environment as the original hook. `waiting` identifies
an unresolved source or incomplete delivery; inspect its error and pending files.
An `ingestion` stage with `complete` status and a context ID confirms a committed
server context. Manual-command logs use `acknowledged` for the same outcome.

Manually recover committed changes even when another branch is checked out:

```sh
servediff review --path /path/to/repository --branch feature --base main --no-browser
servediff review --path /path/to/checkout --base auto --no-browser
```

Manual review defaults to HEAD. `--branch` reads only Git objects, defaults its
base to auto, and ignores the current checkout's dirty files. Deleted uncommitted
files cannot be recovered unless a payload was captured first. Renamed/deleted
refs also require surviving commits or a previously saved payload.

Manually invoke the hook handler for any supported harness:

```sh
servediff hook --harness codex --path /path/to/checkout
servediff hook --harness pi --path /path/to/checkout --run-id session-id
```

Supported harnesses are `codex`, `claude`, `opencode`, and `pi`. `--run-id` is
optional session metadata for logs and search; it does not affect deduplication
or identify edits made during that session. With `--path` and no `--run-id`, the
run ID stays empty. Reused reviews retain their original metadata.

Without `--path`, Codex/Claude command hooks provide a JSON event with `cwd` and
`session_id` on stdin; `session_id` supplies the run ID unless `--run-id` overrides
it. To simulate that input:

```sh
printf '%s\n' '{"cwd":"/path/to/checkout","session_id":"session-id"}' | servediff hook --harness codex
```

Hooks default to `--base auto`, never open a browser, and return after scheduling.
Scheduling success does not confirm ingestion; hooks log failures and exit
successfully. Inspect the dashboard or hook log for delivery state. Manual
`servediff review` waits for submission, reports failures directly, and defaults
to HEAD; use `--base auto` to match the hook baseline.
