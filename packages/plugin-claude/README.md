# servediff claude plugin

Private local plugin. Requires Claude Code 2.1.265 and an installed servediff binary.
Enable plugin hooks in Claude Code settings. The personal plugins directory requires Claude Code 2.1.265 or later.

Completion queues `servediff hook --agent claude`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Use the repository's local plugin installation tasks. Runtime configuration
comes from the same servediff config/environment as the CLI. The local installer
selects an absolute binary path for the hook command.
