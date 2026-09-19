# WebMCP

servediff registers its review tools with a compatible browser through the
current WebMCP `document.modelContext` API. This lets a browser agent work with
the page without learning its DOM or clicking controls.

WebMCP is a draft browser API. servediff uses the current standard directly and
does not load a polyfill or expose a legacy API. In browsers without WebMCP, the
page continues to work normally and registers no tools.

## Browser agents and coding harnesses

A browser agent runs with a browser implementing WebMCP. The open page calls
`document.modelContext.registerTool`, and the browser decides when the agent may
invoke the registered tools. The page must remain open, and its normal browser
origin and lifecycle rules apply.

Codex, Claude Code, and OpenCode running on a local machine are coding
harnesses, not inherently browser agents. They can work with servediff without
opening its page when they support MCP `2026-07-28`: configure the servediff
[`/mcp` endpoint](mcp.md). A harness cannot obtain WebMCP tools merely from a
page URL unless it is driving a WebMCP-capable browser.

## Registered tools

When the session's review-comments capability is enabled, the page registers:

- `get_review_comments`: returns open comments from all enabled scopes; optional
  `include_resolved: true` includes resolved comments.
- `resolve_review_comment`: idempotently resolves the comment identified by
  `comment_id`.

The tools have the same input and behavior as their [MCP
counterparts](mcp.md#tools). Results use real server comment IDs and identify
every comment's lifecycle status, applicability, and whether it is currently
actionable. Stale comments are returned and may be resolved by ID.

The getter is marked read-only and its comment content is marked untrusted. The
resolver is marked mutating and idempotent. Both operate only against the open
servediff application; they do not access unrelated websites or external
services.

## How it works

1. The page checks for `document.modelContext.registerTool`.
2. It registers the two tools while review comments are enabled.
3. A browser agent invokes a tool through the browser, subject to browser UX and
   permission policy.
4. The page calls servediff's same-origin REST API.
5. The Go server reads or updates the authoritative comment store.
6. A successful resolution updates the visible review state immediately.
7. Tool registrations are removed when the page unmounts or the capability
   becomes unavailable.

Closing or navigating away from the page removes access to these WebMCP tools.
Comments themselves remain on the server and are still available through the
UI or MCP endpoint.

## Use

1. Start servediff and open its printed URL in a WebMCP-capable browser.
2. Open that browser's agent interface.
3. Ask it to inspect the servediff review comments and address them.
4. Confirm that only handled comments were resolved.

Example prompt:

> Get the open review comments from this servediff page. Check the current code
> for each one, apply valid fixes, and resolve only comments you completed.

Exact invocation and confirmation UI belongs to the browser implementation.
Feature support may require a secure context; loopback `localhost` and
`127.0.0.1` are treated as trustworthy by modern browsers.

## Security

Comment text and preserved code may contain misleading or adversarial content.
They are returned as untrusted data, and agents should treat them as review
evidence rather than privileged instructions.

The servediff API is unauthenticated. WebMCP does not add authentication: anyone
who can reach the server can access its review data through the available HTTP
interfaces. Keep the default loopback binding, or use only a trusted network.

Future hosted sessions, unique URLs, authentication, and multi-session routing
are outside this first version.

See the [WebMCP specification](https://webmachinelearning.github.io/webmcp/) for
the current browser API.
