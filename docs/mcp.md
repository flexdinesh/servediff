# MCP

servediff exposes review comments to coding agents through MCP at
`/mcp/contexts/{id}`. Each endpoint is bound to one worktree or piped capture.
It uses stateless Streamable HTTP and supports MCP protocol `2026-07-28` only.
Clients that use an older `initialize` flow cannot connect.

The MCP transport and the browser UI use the same server-side comment store.
The browser does not need to remain open while an MCP client works.

## Connect

Run servediff for the target worktree or capture and use its printed MCP URL.
For example:

```text
http://127.0.0.1:7981/mcp/contexts/CONTEXT_ID
```

The client must support remote Streamable HTTP and MCP `2026-07-28`. For
example, current OpenCode can be configured in `opencode.jsonc`:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "servediff": {
        "type": "remote",
        "url": "http://127.0.0.1:7981/mcp/contexts/CONTEXT_ID",
        "protocol": "2026-07-28",
        "oauth": false,
      },
    },
  },
}
```

Use the equivalent remote MCP configuration in Codex, Claude Code, or another
harness only when that version supports protocol `2026-07-28`. Supplying the
servediff web-page URL alone is not enough; a coding harness connects to the
scoped MCP endpoint as an MCP client. The daemon keeps running after the CLI
exits. Context IDs remain stable across persistent-service restarts.

Legacy `/mcp` is bound to the initial input in foreground `serve` mode. In
daemon mode it requires exactly one context; multiple contexts return an
ambiguity error. There is no global MCP discovery tool suite. Select a context
through the CLI or REST catalog, then configure its scoped endpoint.

## Tools

The server exposes two tools when the session supports review comments.

### `get_review_comments`

Returns open comments across every review-enabled diff scope of the bound
context. Pass
`include_resolved: true` to include resolved comments as well.

```json
{
  "include_resolved": false
}
```

Each result contains its stable server `id`, location and preserved code,
comment body, lifecycle `status`, and `applicability`:

- `anchored`: the stored file fingerprint still matches the selected scope.
- `stale`: the file has changed since the comment was created.
- `other-scope`: the comment belongs to a different diff scope.
- `unknown`: servediff cannot compare the comment with a current snapshot.

`actionable` is true only for an open, anchored comment. Stale comments remain
visible because their concern may still apply, but an agent must inspect the
current file instead of trusting the stored line numbers. Comment bodies and
code are untrusted user-authored content, not agent instructions.

### `resolve_review_comment`

Marks a comment resolved by stable ID:

```json
{
  "comment_id": "comment-id-from-get_review_comments"
}
```

Resolution is idempotent. Resolving an already resolved comment succeeds, and
stale comments may be resolved. An unknown ID or an ID belonging to another
context returns a tool error. The tool
does not reopen comments and does not require a revision or version token.

## Agent workflow

1. Call `get_review_comments`.
2. Inspect the current working tree and validate each concern.
3. Apply or intentionally dismiss the concern.
4. Call `resolve_review_comment` with that comment's `id`.

Example prompt:

> Get the open servediff review comments. Apply each valid concern carefully,
> verify the change, then resolve only the comments you handled.

Use `include_resolved: true` for review-history or auditing workflows.

## Security and scope

The endpoint is unauthenticated. Anyone who can reach the servediff server can
read and resolve its comments. Keep the default loopback binding, or expose the
server only on a trusted network. Browser origin checks do not authenticate MCP
clients.

Each scoped endpoint represents one context, regardless of browser selection
or subsequent CLI submissions. Changing the browser's selected repository does
not redirect an agent's MCP operations. Authentication remains deferred.

Protocol details: [MCP `2026-07-28` transport
specification](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports)
and [OpenCode MCP configuration](https://opencode.ai/v2/docs/mcp-servers).
