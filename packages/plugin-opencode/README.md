# servediff opencode plugin

Private local plugin. Requires OpenCode 2.0.22 and an installed servediff binary.
Uses the V2 server plugin API. Earlier V1 plugin APIs are unsupported. Event location selects the checkout when events originate from another worktree.

Completion queues `servediff hook --agent opencode`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Use the repository's local plugin installation tasks. Runtime configuration
comes from the same servediff config/environment as the CLI. `SERVEDIFF_BINARY`
can select a binary when running the adapter outside the installer.

`pnpm build` emits a self-contained `dist/index.js`; installed copies require no
workspace imports or host SDK at runtime. Host SDK types are development-only.
