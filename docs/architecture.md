# Architecture

servediff composes one Go application core into foreground local and distributed
remote runtimes. React/Vite assets are embedded; Node is a build dependency.

## Composition

- `servediff PATH`: collect once, ingest directly, then watch only that checkout.
  The foreground process owns shared persistent SQLite state, UI, REST and MCP.
  Ctrl-C stops collection and serving; observations and review state persist.
- `servediff sync`: finite collector including registered worktrees; authenticated
  HTTP admission, durable job polling, and process exit after committed results.
- `servediff-server`: foreground container process with authenticated HTTP,
  durable queue, worker, UI, REST and MCP. No Git or checkout mount.

`serverapp.Run` shares HTTP/task cancellation, graceful shutdown and pruning.
Local composition adds authenticated singleton discovery/replacement and a
collector watcher. Remote composition adds credential resolution and a worker.
REST and MCP call the same query/review services, never each other over HTTP.

The local lifecycle lock serializes replacement/startup until the new descriptor
is published. Database ownership is released only after serving and background
work stop. Replacement never trusts a PID alone. Legacy daemon commands retain
their old explicit compatibility behavior; new local collection does not submit
through their HTTP ingestion adapter.

## Modules and contracts

- `collector`, `diffsource`: Git discovery, stable enrollment, parsing, bounded
  previews and content identity. Watch reuses CollectChanged with bounded polling.
- `ingestion`: versioned request/job/receipt contracts, validation, pure content
  identity and comparison policy, and HTTP clients.
- `contextservice.Store`: atomic observation publication and stored catalog reads.
- `reviewservice.CommentStore`, `MutationStore`: shared review state operations.
- `httpapi.Store`: review persistence capabilities required by REST.
- `reviewdata`: storage-independent IDs, results and errors.
- `reviewstore`: SQLite adapter and schema migration; no Git query dependency.
- `ingestionqueue.Queue`: durable acceptance/status, claims, renewable leases,
  completion, delayed retry and terminal failure.
- `submission`: private immutable manual-upload recovery; `hooks` adds finite,
  coalesced harness scheduling and existing session-aware suppression.
- `serverapp`, `httpapi`, `mcpapi`: shared transport composition.
- `daemon`: existing lifecycle primitives plus foreground replacement.
- `remoteserver`: remote authentication and composition root.
- `config`: shared home-directory settings, independent of lifecycle protocol.

Storage interfaces describe complete application operations, not independently
committed CRUD steps. The SQLite queue shares its owning store. A future external
broker adapter must persist payload/status and scheduling intent atomically,
then publish from an outbox. Broker messages should carry references rather than
64 MiB captures. A new storage adapter must pass the same behavioral tests.

## Invariants

An observation is an immutable captured review context. Fresh submissions may
deduplicate within the same owner/source/repository/checkout/branch/HEAD/comparison
and full-content identity. Unknown content identities remain independent.
Captured metadata, scope manifests, previews, comments and reviewed marks survive
deduplication; new session associations remain searchable.

Transport retries retain submission ID and exact payload; changed replays
conflict. Retries never extend retention. Fresh matching submissions can extend
expiry. Existing migration and branch-adoption semantics remain intact.

Queue acceptance records a durable sequence. Stream heads compare collection
time, then acceptance sequence; worker completion order cannot regress freshness.
Heads outlive pruning. Queue replay records survive deletion/expiry, preventing
delayed delivery from resurrecting observations. Compact job/replay records are
retained; completed/failed payload bodies are removed. Legacy direct deletion
continues allowing an explicit new submission after deleting its retry mapping.

Queries read stored data only. Failure during collection never becomes an empty
observation. Empty successful captures record dirty-to-clean transitions.

## Queue and recovery

Remote `POST /api/v2/ingestion-jobs` returns 202 plus a Location. Status is
authenticated and owner-scoped. Success means committed publication; acceptance
alone does not. The synchronous v2 ingestion endpoint remains compatible.

The built-in queue caps active work at 1,024 jobs and 256 MiB, with 30-second
renewable leases, one worker, two-minute processing deadlines and five processing
attempts. Transient failures retry with bounded exponential delay; permanent
errors terminate. Expired leases recover after restart. Commit-before-ack crashes
replay the original ingestion transaction without creating another review.

Manual sync persists captures before health/network calls and pins attempted
uploads to destination identity. `sync --retry` needs no surviving checkout.
Hooks retain their existing private pending payloads and finite retry scheduling.
Plugins require remote configuration and never start a local server.

## Compatibility and validation

The positional local command, sync, and top-level config are the primary UX.
Legacy commands remain migration compatibility paths. Schema 8 migrates existing
captures/reviews in place; stop old processes before upgrading. Generated OpenAPI
types and embedded web assets are committed.

Validation covers legacy identities/retention, queue acceptance order, restart
after commit, lease fencing, terminal failure, owner isolation, full CLI sync
recovery, local edit watching and singleton replacement.
