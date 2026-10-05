# servediff pi plugin

Private local plugin. Requires Pi 1.0.0 and an installed servediff binary.
Uses `agent_settled`, after retries, compaction and queued continuations finish. The earlier `agent_end` event is intentionally unused.

Completion queues `servediff hook --agent pi`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Use the repository's local plugin installation tasks. Runtime configuration
comes from the same servediff config/environment as the CLI. `SERVEDIFF_BINARY`
can select a binary when running the adapter outside the installer.

`pnpm build` emits a self-contained `dist/index.js`; installed copies require no
workspace imports or host SDK at runtime. Host SDK types are development-only.
