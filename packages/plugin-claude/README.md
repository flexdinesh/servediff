# servediff claude plugin

Requires Claude Code 2.1.265 and an installed servediff binary.
Enable plugin hooks in Claude Code settings.

Completion queues `servediff hook --agent claude`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Install through the repository's root marketplace; no build is needed:

```sh
claude plugin marketplace add /absolute/path/to/servediff
claude plugin install servediff@servediff
```

Remove with `claude plugin uninstall servediff@servediff`. Runtime configuration
comes from the same servediff config/environment as the CLI. The harness must
have `servediff` on PATH or `SERVEDIFF_BINARY` set to its absolute path, including
GUI sessions. See [plugin setup](../../docs/plugins.md) for remote installation,
configuration and migration from the old installer.
