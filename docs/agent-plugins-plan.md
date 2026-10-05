# Agent plugins implementation plan

## Outcome

Locally installed Codex, Claude Code, OpenCode and Pi plugins request a sync at
agent completion. The request returns promptly; a detached Go worker collects
the latest checkout against HEAD and submits through the existing collector and
ingestion flow. All worktrees remain visible in the existing web UI. No plugin
publication, turn baselines, watcher, or frontend changes.

## Contracts and ownership

- `servediff hook --harness NAME`: command hooks provide `cwd` and `session_id`
  on JSON stdin. Embedded extensions pass `--path` and `--run-id` explicitly.
  Optional `--config-file` selects machine settings. Hook failures never request
  agent continuation; diagnostics go to private local logs.
- `__hook-worker`: internal detached worker entry. Null stdin and independent
  stdout/stderr prevent the host waiting for inherited pipes. Workers have a
  finite deadline and survive normal host teardown.
- `internal/collector`: checked collection, stable content identity, complete
  bounded before/after previews. A changed-only entry avoids constructing
  previews when the acknowledged checkout fingerprint is unchanged.
- `internal/hooks`: private atomic request/state files, worker ownership,
  coalescing, last acknowledged fingerprint, bounded pending payloads, retries,
  service-start cooldown and log retention. Keys include destination, source
  and checkout; agent session IDs are provenance, not checkout identity.
- `internal/config`: code defaults, JSON file, environment, explicit flags in
  that order. Config editing reads/writes file values without environment
  leakage. Existing inline `--config` remains supported. Missing files have
  defaults; invalid files/environment fail clearly. Server settings change on
  restart; workers resolve settings on each invocation.
- Private `packages/plugin-{codex,claude,opencode,pi}`: native manifests and
  completion adapters only. Codex/Claude use `Stop`, OpenCode idle status,
  Pi `agent_settled`. No direct HTTP, conversation output, or model tools.
- Local installation tasks: install/remove individual plugins, preserve other
  host settings, use absolute paths, expose setup/trust requirements, verify
  installed artifacts independently of workspace module resolution.

## Tracer bullets

1. **One real path:** a synthetic completion JSON launches a detached worker,
   collects an isolated dirty repository and commits an observation in an
   isolated local service. Confirm return latency, host-independent lifetime,
   correct worktree metadata and preserved before/after contents.
2. **Reliable sync:** repeat unchanged triggers without uploads; edit a file
   without changing its size/timestamp and sync it; submit dirty-to-clean;
   branch/HEAD/staging changes invalidate the fingerprint. Mark acknowledged
   state only after durable receipt. Coalesce concurrent triggers and process
   an event arriving while collection/upload is active. Unknown full content
   identities must not be falsely suppressed.
3. **Failure isolation:** health probe, one local startup attempt, bounded
   transient retries, shared cooldown after startup failure, bounded pending
   payload retention. Preserve submission ID for ambiguous retries; never
   silently switch an uncertain request to another server/database. New latest
   state can supersede obsolete pending work. No perpetual retry loop.
4. **Four hosts:** native event adapters feed the same Go path. Verify package
   shapes and local installation/removal, then smoke-test available real hosts
   without requiring model calls. Document any unavailable host verification.
5. **Ship:** relevant focused tests, full `mise run check`, Go race tests,
   generated-asset cleanliness, push branch and create PR targeting `main`.

## Parallel implementation

Independent Worktrunk worktrees own config resolution, changed-only collection,
hook worker/state, native plugins, and local installer. The integration agent
owns CLI dispatch, end-to-end tests and documentation. Contracts are shared
before implementation; integrate commits serially and resolve checks together.

## Future compatibility

Keep captured data immutable and retain existing full-file contents. Producer
comparison and HTTP/local transport stay separate so a later turn-baseline
producer can reuse ingestion. Do not add speculative turn fields or overload
HEAD with a turn identity. Existing latest/stale stream semantics remain intact.

## Verification

Behavior tests cover detached execution, coalescing/handoff, retry identity,
destination changes, bounded state, clean transitions, startup cooldown,
configuration precedence and editing, native lifecycle filtering, and installer
preservation/idempotency. Existing Git/store tests cover immutable snapshots.
No frontend change is expected; generated assets must remain clean after checks.

## Unresolved questions

None. Local-only plugin installation; latest checkout capture; async collection
and ingestion; bounded failures; environment overrides JSON defaults.
