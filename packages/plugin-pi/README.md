# servediff pi plugin

Requires Pi 1.0.0 and an installed servediff binary.
Uses `agent_settled`, after retries, compaction and queued continuations finish. The earlier `agent_end` event is intentionally unused.

Completion queues `servediff hook --harness pi`. The CLI detaches collection,
health checks, local startup and ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Install the ready-built package from a checkout with Pi's native CLI:

```sh
pi install /absolute/path/to/servediff/packages/plugin-pi
```

Keep the checkout at that path. Remove with `pi remove` using the same absolute
source path. Runtime configuration comes from the same servediff
config/environment as the CLI. The harness must have `servediff` on PATH or
`SERVEDIFF_BINARY` set to its absolute path. See
[plugin setup](../../docs/plugins.md) for configuration and migration from the
old installer.

The committed `dist/index.js` is self-contained; installation needs no build,
workspace imports or host SDK at runtime. Developers regenerate it with
`mise run plugins:build`. Host SDK types are development-only.
