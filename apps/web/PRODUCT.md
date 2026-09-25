# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Developers checking Git diffs of their changes. They review the code in a browser and hand feedback to a coding agent.

## Product Purpose

servediff helps developers inspect local Git changes, leave review comments, and pass actionable feedback to a coding agent. A successful session lets the developer understand the changes and communicate the work they want done.

## Positioning

A local review workspace that combines a browser-based Git diff with comments that coding agents can consume. The diff is the center of the experience.

## Operating Context

- A developer runs `servediff` from a Git repository. The Go server opens the review UI in a browser.
- Reviews can cover all, staged, or unstaged changes. The file tree, reviewed-file marks, and comments help track progress.
- Comments can be copied as agent-ready XML. The same review comments are available to compatible coding agents through MCP and, in supported browsers, WebMCP.
- The CLI can also display a piped Git patch as a fixed review.
- Review data is stored by the local servediff server, as documented in the README.

## Capabilities and Constraints

- The web UI uses the Pierre diff package extensively. Use its intended APIs and rendering model; never break its selection, annotations, sticky headers, context expansion, or virtual scrolling.

## Evidence on Hand

- Product usage and behavior: `../../README.md`.
- Architecture and frontend ownership: `../../docs/architecture.md` and `../../docs/development.md`.
- Agent integration: `../../docs/mcp.md` and `../../docs/webmcp.md`.
- Existing visual system and Pierre integration rules: `../../DESIGN.md` and `src/DiffWorkspace.tsx`.

## Product Principles

- Keep code and its diff readable as the primary content.
- Make review feedback precise and easy to hand to a coding agent.
- Respect Pierre's supported integration and measured rendering behavior.
- Keep review state clear across diff scopes and changing files.
