# servediff codex plugin

Private local plugin. Requires Codex CLI 0.160.0 and an installed servediff binary.
Enable the plugin and approve its hook trust review. Hooks must be enabled in Codex settings.

Completion queues `servediff hook --agent codex`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Use the repository's local plugin installation tasks. Runtime configuration
comes from the same servediff config/environment as the CLI. The local installer
selects an absolute binary path for the hook command.
