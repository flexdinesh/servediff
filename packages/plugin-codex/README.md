# servediff codex plugin

Requires Codex CLI 0.160.0 and an installed servediff binary.
Enable the plugin and approve its hook trust review. Hooks must be enabled in Codex settings.

Completion queues `servediff hook --agent codex`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Install through the repository's root marketplace; no build is needed:

```sh
codex plugin marketplace add /absolute/path/to/servediff
codex plugin add servediff@servediff
```

Remove with `codex plugin remove servediff@servediff`. Runtime configuration
comes from the same servediff config/environment as the CLI. The harness must
have `servediff` on PATH or `SERVEDIFF_BINARY` set to its absolute path, including
GUI sessions. See [plugin setup](../../docs/plugins.md) for remote installation,
configuration and migration from the old installer.
