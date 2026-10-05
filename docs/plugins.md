# Local agent plugins

Install a plugin to synchronize the latest checkout after an agent finishes.
Collection and ingestion run in a detached Go worker; the agent only schedules
work. The web UI's existing latest/stale filters cover all submitted worktrees.

## Install and remove

From a development checkout, install dependencies and a chosen plugin:

```sh
mise exec -- pnpm install --frozen-lockfile
mise run plugins:install --host codex
mise run plugins:install --host claude
mise run plugins:install --host opencode
mise run plugins:install --host pi
mise run plugins:remove --host pi
```

Omit `--host` to select all four. Installing builds the CLI and plugin artifacts,
then copies them to private user directories; publishing is unnecessary. The
installer captures an absolute CLI path, so GUI sessions need not inherit the
same PATH. Keep that executable in place, or reinstall with another binary:

```sh
mise run plugins:install --host codex --binary /path/to/servediff --config-file /path/to/config.json
```

Repeated installation/removal preserves unrelated host settings and plugins.
Existing unmanaged files at servediff's destination are not overwritten. Native
Codex and Pi registration requires their CLIs on PATH. Restart active sessions
after installing or replacing adapters.

| Host        | Supported baseline    | Completion event                  | User installation                                   |
| ----------- | --------------------- | --------------------------------- | --------------------------------------------------- |
| Codex       | CLI 0.160.0           | `Stop`                            | Private local marketplace, installed through Codex  |
| Claude Code | 2.1.265               | `Stop`                            | `~/.claude/skills/servediff` plugin directory       |
| OpenCode    | 2.0.22, V2 plugin API | `session.status` with idle status | `~/.config/opencode/plugins/servediff.js`           |
| Pi          | 1.0.0                 | `agent_settled`                   | Private copied package registered with `pi install` |

Codex requires enabling the plugin and approving its hook trust review; changing
hook definitions can require another review. Host policies can disable hooks.
Claude hooks must also be enabled. Claude's native loader was unavailable in the
development environment; its package shape and command execution were verified.
OpenCode V1 plugin APIs are not supported.

Installation respects `CLAUDE_CONFIG_DIR`, `XDG_CONFIG_HOME`, and `XDG_DATA_HOME`;
native Codex/Pi commands respect their host configuration variables.

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
