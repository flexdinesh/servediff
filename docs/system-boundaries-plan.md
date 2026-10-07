# System boundaries implementation

## Decisions

- Git collection defaults to branch changes from the default branch merge base,
  plus working changes. Staged and unstaged remain HEAD/index comparisons.
- Collect current checkout and all registered worktrees. Workspace discovery and
  object-only branch recovery remain explicit policies; hooks ignore non-Git input.
- Each producer invocation has one destination. Remote failures never fall back
  locally. Independent local and remote deployments may coexist.
- Immutable snapshot content is distinct from collection/session provenance.
  Sessions associate with shared reviews within one user; metadata does not assert
  authorship. Preserve triggering directory for all-worktree collections.
- Production resolves credentials to individual users in one SQLite database,
  one replica. Bootstrap admin and a generated persistent token; no organizations.
  Local composition supplies a default user.
- Retention is configurable in whole days, defaults to seven. Existing snapshots
  retain their expiry until fresh submission; retries do not extend retention.
- Publication receipts mean committed storage. No server queue or storage backend
  framework is introduced without a concrete backend.

## Shared contract

`ingestion.Metadata` gains optional `comparison`, `agentSession`, and
`triggerRoot`. Comparison: `kind` (`working-tree` or `branch`), `baseRef`,
`baseCommit` (resolved baseline tip), `mergeBase` (actual comparison commit).
Agent session: `harness`, `id`, optional `name`. Existing `agent`/`runId` remain
compatible labels; missing structured session falls back to those fields.
Filters add `harness`, `sessionId`, `sessionName`; `runId` remains supported.
Legacy missing comparisons mean working-tree/HEAD.

Configuration gains `server`, `token`, `retentionDays` (default 7, positive).
Store adds `OpenWithRetention(path, duration)`; `Open` keeps the seven-day default.
Local/control and remote settings carry `RetentionDays`; zero from legacy callers
means default. Destination config uses file < environment < explicit flags.

## Parallel tracks and ownership

1. Comparison/collection: collector, diffsource, ingestion validation. Persist
   baseline facts; default auto; expose policies for registered worktrees versus
   workspace/recovery. Test commits, dirty edits, clean base, explicit HEAD,
   detached/unborn repositories, consistent collection and changed refs.
2. Session storage/retention: reviewstore except authentication, contextservice.
   Store/query every fresh submission's session association, preserve snapshot
   and comments on dedupe, partition freshness by comparison policy. Migration
   preserves old data/retry hashes. Retention applies to all new/renewed scopes.
3. Producer orchestration/config: cmd/servediff, hooks, config, controlapi,
   daemon. Share destination/submission logic and acknowledgements; manual review
   ingests all worktrees and opens the originating review; hooks skip initial clean
   captures, publish clean transitions, preserve new session associations, expose
   optional session names. Reconcile missing acknowledgement state conservatively.
4. Production users: remoteserver, cmd/servediff-server, new reviewstore auth file.
   Persist hashed credentials, bootstrap admin once, authenticate bearer/Basic
   per user, scope handlers/services/events/caches, provide local user/token
   provisioning command. Test two-user REST/MCP/event isolation and restarts.
5. Application/transports: reviewservice, httpapi, mcpapi, serverapp. Move common
   review operations out of REST into application services; expose context search
   and stored diff retrieval through global MCP while retaining scoped tools.
   Preserve protocol and REST behavior. Require user-scoped context resolution.

Foundation precedes these tracks. Tracks commit isolated worktrees; integration
resolves cross-track interfaces and behavior. API schema, generated artifacts,
plugin metadata adapters, system/development documentation and comprehensive
regression tests are finalized after integration.

Integration decisions: manual review remains an explicit fresh submission;
hooks own their suppression acknowledgements and reconcile server state when
missing. Both share collection/routing/delivery operations. Ingestion protocol 2
advertises provenance support; the server continues accepting legacy protocol 1
payloads and retries. Collectors/server should be upgraded together.

## Acceptance and verification

- Manual and hooked captures use identical branch semantics and policy freshness.
- All registered worktrees collected; failures never publish false empty captures.
- Initially clean automatic captures suppressed; dirty-to-clean delivered.
- Identical content in sessions A/B shares one review, filterable under both.
- Credentials select owners; one user's IDs cannot access another user's data.
- Admin token persists across restarts; secrets never appear in help/logs.
- Config precedence/invalid values tested; configurable expiry preserves replay.
- REST/MCP reads remain stored-only after checkout deletion; review operations
  share services. Legacy API/context routes remain compatible where feasible.
- Run relevant package tests per track, then `mise run check`, Go race tests,
  generation/staging, and generated drift checks. Commit generated artifacts.
- No push, deployment, running-user service restart or PR creation required.

## Risks and execution guidance

Default branch comparisons change manual-review behavior; document `--base HEAD`.
Schema additions preserve legacy observations and classify missing policy as HEAD.
Session names are optional and mutable; plugin payloads may supply only IDs.
Configured baselines use existing local refs, with no automatic fetch. Unborn or
missing-default checkouts retain explicit working-tree fallback metadata.
Only independent tracks run in parallel. Update this plan and surface deviations
if integration changes scope, contracts, behavior or validation.

## Completion and verification

Implemented all five tracks on `codex/system-boundaries`; integrated their
independent commits, schema migration, generated API/plugin/web assets and docs.
Registered-worktree collection has no count limit; regression covers 129
checkouts. Temporary agent worktrees removed; integrated worktree retained.

Passed `mise run check`: static checks, builds, generated drift, API conformance,
unit/plugin tests, all 65 browser tests, release tests and the complete Go suite.
Passed `mise run test:race` across all Go packages. One browser assertion timed
out on the first full run; passed three isolated repetitions and the full rerun
without changing code or weakening its assertion.

Upgrade collector and server together for ingestion protocol 2 and control
protocol 4. Production user provisioning currently requires stopping the
server; credentials and all users share one database. No push or deployment.

Unresolved questions: none.
