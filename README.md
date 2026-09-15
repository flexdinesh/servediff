# servediff

Review local Git changes in your browser. Built with React, Vite, Tailwind CSS v4,
[shadcn/ui](https://ui.shadcn.com) on Base UI, and
[Pierre diffs](https://diffs.com); inspired by diffshub and
[diffx](https://github.com/wong2/diffx).

## Setup

Requires **Node 26**, **pnpm 11**, and **Git**. Developing or building the
parallel Go implementation also requires **Go 1.25**. From this checkout:

```sh
pnpm install
pnpm build
pnpm add -g .
```

This registers `servediff` globally, pointing to this checkout. Keep the checkout in place. If pnpm reports a missing global bin directory, run `pnpm setup`, restart your terminal, and retry.

## Usage

From any Git repository:

```sh
servediff .
```

servediff automatically opens the local address in your browser on macOS and
Linux, except in SSH sessions. Stop with **Ctrl+C**. The network address is
reachable by other devices on your local network.

```sh
servediff .                  # current repository
servediff /path/to/repo      # another repository
servediff . --port 4000      # different port
```

Subdirectories resolve to the repository root. Switch between all, staged, and unstaged changes; edits refresh automatically. Use the file tree to navigate and **+** beside a line to comment. **Copy unresolved** or **Copy all** exports agent-ready XML with instructions, short comment IDs, captured code context, and review provenance. Comments and reviewed-file marks persist through the local server; display preferences stay in your browser. The viewer never changes your Git files or index.

Or pipe Git output directly:

```sh
git diff | servediff
git show | servediff
git show main..HEAD~1 | servediff
git diff main HEAD~1 | servediff
servediff - < saved.patch
```

Piped or redirected input takes priority over a directory argument; an empty pipe opens an empty diff. Without input redirection, a directory is required. Piped diffs are fixed snapshots; re-run the command to update. Commit ranges show each commit separately, including repeated files. `git diff` between two refs shows their net difference. Standard Git patches up to 16 MiB total / 2 MiB per file are supported; use `git show --diff-merges=separate` for merge commits.

## Development

From this checkout:

```sh
pnpm dev:web
pnpm dev:server
```

Both development commands use `test/fixtures/sample.diff`. The web workspace
runs the production API handler with an in-memory fixture store preloaded with
comments and reviewed-file state; the server workspace runs the Node CLI with
the same diff piped to stdin.

The repository also contains a contract-compatible Go server. It coexists with
the Node server while parity is verified; the Node implementation remains the
published `servediff` command.

```sh
task dev             # Go API with fixture data + Vite HMR
task dev:go          # standalone Go server with built web UI
task build:go        # dependency-free dist/servediff-go binary
task check           # JavaScript, Go, and cross-server conformance
```

Without [Task](https://taskfile.dev), equivalent `pnpm` and `go` commands are
listed in `Taskfile.yml`. The built Go binary accepts a repository path, piped
patch, or `--fixture`; use `--state memory` for isolated development/tests.

Install Chromium once, then test either workspace independently or run every
check from the root:

```sh
pnpm test:browser:install
pnpm --filter @servediff/server test
pnpm --filter @servediff/web test
pnpm check
```

Run `pnpm build` after frontend changes, then restart `servediff`. No global reinstall needed.

See [docs/architecture.md](docs/architecture.md) for workspace boundaries and
[ADR 0001](docs/adr/0001-polyglot-monorepo.md) for the migration decision.

UI work follows [DESIGN.md](DESIGN.md). Existing semantic tokens in
`apps/web/src/tokens.css` remain canonical and are mapped into Tailwind/shadcn
theme roles by the global stylesheet. Reuse repository-owned primitives in
`apps/web/src/components/ui`; reserve authored CSS for specialized layout,
dynamic geometry, and Pierre's measured rendering boundary.

`App.tsx` composes the page sections. `app-state.tsx` owns shared state through
`AppProvider`; `DiffWorkspace.tsx` owns Pierre rendering, and `use-review.ts`
uses the generated REST client. Section-only state stays with its component.

## API

The server prints a per-run API token and includes it in browser URLs as a URL
fragment. REST clients send it as `Authorization: Bearer <token>`. The OpenAPI
3.1 contract is available at `/openapi.yaml`; regenerate the TypeScript client
after contract changes with `pnpm generate:api`.

The versioned API exposes the active source, current diff snapshots, file
patches and contents, comment CRUD/export, and reviewed-file marks under
`/api/v1`. Piped input supports only the `all` scope and remains immutable.
Comments persist by source under `$XDG_STATE_HOME/servediff/reviews` (or
`~/.local/state/servediff/reviews`).
