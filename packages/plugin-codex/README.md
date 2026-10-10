# diffx codex plugin

Requires Codex CLI 0.160.0 and an installed diffx binary.
Enable the plugin and approve its hook trust review. Hooks must be enabled in Codex settings.

Completion queues `diffx hook --harness codex`. The CLI detaches collection,
health checks and remote ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Install through the repository's root marketplace; no build is needed:

```sh
codex plugin marketplace add /absolute/path/to/diffx
codex plugin add diffx@diffx
```

Remove with `codex plugin remove diffx@diffx`. Runtime configuration
comes from the same diffx config/environment as the CLI. The harness must
have `diffx` on PATH or `DIFFX_BINARY` set to its absolute path, including
GUI sessions. See [plugin setup](../../docs/plugins.md) for remote installation,
configuration and migration from the old installer.

Requires remote `server` and `token` settings in diffx config. Plugins never
start a local server.
