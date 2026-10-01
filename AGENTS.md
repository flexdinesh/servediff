# AGENTS.md

Go CLI/server in `cmd/` and `internal/`; embedded React/Vite app in `apps/web/`.

- Keep changes minimal. Follow existing patterns.
- Preserve TypeScript type safety: no `any`, type assertions, or non-null assertions.
- Use pnpm. Install dependencies with `pnpm install --frozen-lockfile`.
- Run relevant checks for changes; `task check` runs all checks.
- After frontend changes, run `task web:stage` and commit `internal/webui/dist`.
- See `docs/development.md` and `docs/release.md` for workflows.

## Git etiquette

- Before pushing to any remote, run all CI checks locally with `task check`; resolve failures.
- After checks, `git status --porcelain --untracked-files=all -- internal/webui/dist` must be empty, matching CI's embedded-asset drift check.
