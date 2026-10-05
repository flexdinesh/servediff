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

The worker captures all/staged/unstaged changes against HEAD, including untracked
files. It retains patches and bounded full before/after contents for future
full-file viewing. It does not attribute edits to a turn or agent; completion is
a trigger, and the worker may capture edits made after that trigger.

Concurrent triggers coalesce per checkout/config location. Workers normalize
nested directories to the checkout root. Exact contents, file modes, HEAD,
branch, staging and repository identity determine the fingerprint; timestamps
alone do not. A successful acknowledgement and a still-current stored context
allow unchanged uploads to be skipped. Dirty-to-clean transitions submit an
empty observation, superseding the old changed one. Unknown complete content
identities are collected conservatively rather than falsely suppressed.

Workers health-check the destination and start the local service if stopped.
Each worker has a one-minute deadline, at most two attempts per payload, and a
shared one-minute cooldown after failed startup. Pending immutable payloads
retain retry IDs; newer checkout state supersedes obsolete pending data. Retries
only run on later completion triggers, without a perpetual background loop.
Server/database identity prevents ambiguous requests from being replayed into a
replacement database.

Private hook state defaults to `~/.local/state/servediff/hooks`; `XDG_STATE_HOME`
and isolated `SERVEDIFF_RUNTIME_DIR` are respected. Pending data expires after
seven days and is pruned toward a 256 MiB budget; active uploads are protected.
Acknowledgements refresh after 24 hours so unchanged observations retain their
server availability. Diagnostics stay in `hooks.log`, bounded to approximately
128 KiB. Hooks never open a browser or write diagnostics into the conversation.

Manual adapter invocation for diagnosis:

```sh
servediff hook --agent pi --path /path/to/checkout --run-id session-id
```

Codex/Claude command hooks instead provide a JSON event with `cwd` and
`session_id` on stdin. Scheduling success means the request was recorded, not
that ingestion committed. Inspect the dashboard or hook log for delivery state.
