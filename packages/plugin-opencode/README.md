# diffx opencode plugin

Requires OpenCode 2.0.22 and an installed diffx binary.
Uses the V2 server plugin API. Earlier V1 plugin APIs are unsupported. Event location selects the checkout when events originate from another worktree.

Completion queues `diffx hook --harness opencode`. The CLI detaches collection,
health checks and remote ingestion; the hook waits only for scheduling.
Failures never request continuation or write into the conversation.

Install the ready-built package with OpenCode's native CLI:

```sh
opencode plugin add 'github:flexdinesh/diffx#main::path:packages/plugin-opencode'
```

Remove with `opencode plugin remove` using the same Git spec. For a local
checkout, add `/absolute/path/to/diffx/packages/plugin-opencode` to the
`plugins` array in `opencode.jsonc`; keep that path available and remove its
entry to uninstall. The CLI accepts npm/Git specs, not local paths.

Runtime configuration comes from the same diffx config/environment as the
CLI. The harness must have `diffx` on PATH or `DIFFX_BINARY` set to its
absolute path. See [plugin setup](../../docs/plugins.md) for configuration and
migration from the old installer.

The committed `dist/index.js` is self-contained; installation needs no build,
workspace imports or host SDK at runtime. Developers regenerate it with
`mise run plugins:build`. Host SDK types are development-only.

Requires remote `server` and `token` settings in diffx config. Plugins never
start a local server.
