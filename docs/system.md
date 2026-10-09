# System design

servediff collects Git changes at the producer and pushes complete observations
to a server. The server validates and stores those observations, then serves
them through REST, MCP and a web dashboard. Queries read stored state; they never
ask a producer to inspect a checkout.

This document records the implemented design and its decisions. See
[architecture.md](architecture.md) for package boundaries,
[the README](../README.md) for commands and [the OpenAPI contract](../packages/api/openapi.yaml)
for transport details.

## Terminology and ownership

| Term                | Meaning and responsibility                                                                                                              |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Producer            | Code with access to a checkout or supplied patch. Collects data, attaches provenance and submits it.                                    |
| Collector           | The shared Git-aware collection pipeline used by manual CLI commands and agent hooks.                                                   |
| Plugin / agent hook | An event-triggered producer. Can invoke the CLI or implement the ingestion contract directly.                                           |
| Server              | Foreground process owning application processing, SQLite, REST, MCP and web assets. Remote composition also owns admission and workers. |
| Observation         | An immutable snapshot: original metadata, diff scopes, patches and available file contents.                                             |
| Submission          | One collection's provenance and retry identity; may associate with an existing matching observation.                                    |
| Agent session       | Harness, session ID and optional mutable name associated with submissions; does not establish authorship.                               |
| Context             | The server-assigned address for reviewing an observation, including its review state.                                                   |
| Source              | A producer installation or explicitly identified container. Distinct from a repository, branch or hostname.                             |
| Checkout            | A source-specific Git working directory or linked worktree. Owned by the producer.                                                      |
| Account / owner     | The server-established identity that owns stored observations and review state.                                                         |

The CLI owns argument parsing, collection, transport and local lifecycle
bootstrap. The server owns the catalog and its interpretation. The CLI does not
register paths for later server collection or maintain its own catalog.

## Processes and deployment

The application core has two compositions. See [architecture.md](architecture.md)
for contracts, durability rules and migration details.

`servediff [PATH]` is one foreground process: collect the selected checkout once,
ingest directly, and serve UI/REST/MCP. Omitted paths mean the current directory.
The captured snapshot remains fixed after edits and branch switches.
The home-directory database survives process termination. Only one local process
owns it; another invocation prompts before authenticated graceful replacement.
Noninteractive callers use `--replace`. There is no collector daemon.

`servediff sync` collects the originating checkout and registered worktrees once,
saves immutable pending payloads, and submits them over authenticated HTTP.
The Docker server owns durable queue admission, the ingestion worker, shared
application services and persistent storage. Sync waits for committed results.
Plugins schedule finite remote-only collector invocations with harness metadata.

Both modes use identical collection, validation, identity, ingestion, catalog
and review operations. Their lifecycle and submission delivery differ.

## Request and response flows

### Collect and ingest

Collection resolves the baseline and all/staged/unstaged scopes together with
full-content identity and bounded immutable previews. It never fetches refs.
Local collection selects one checkout; remote sync discovers all registered
worktrees. Explicit `--branch` recovery remains available for remote collection.

Local composition calls `contextservice.Ingest` directly. Remote collectors
POST to `/api/v2/ingestion-jobs`, receiving 202 with a durable job/status URL.
Acceptance records owner, payload hash, submission identity and arrival sequence
atomically. A leased worker calls the same ingestion operation. The observation,
scopes, session associations, retry identity and stream head commit atomically.
Then the worker acknowledges the job.

Clients poll owner-scoped status; succeeded includes the context ID.
Transient ingestion notifications remain hints; clients reload durable catalog
state after reconnecting. Queue workers may execute out of order, but stream
freshness follows collection time and acceptance sequence.

### Browse and review

The dashboard queries `/api/v2/contexts`, selects a repository, then selects an
observation identified by worktree, branch, source, run and time. Distinct
content from the same worktree remains independently selectable; unchanged
reviews reuse one context. Search supports repository, branch, worktree,
hostname, source and run metadata, plus harness/session ID/session name through
submission associations. Filters cover All / Latest / Stale, Host,
Branch and Worktree, with OR within a multi-select filter and AND across
filters. Filtering precedes repository grouping and counts. Latest snapshots
with changes are the default; the right-aligned All checkbox also includes
unavailable, empty and unknown-status entries. Unavailable entries remain
disabled and skipped by keyboard navigation. Selections persist while navigating
the picker. New collections make older snapshots stale within the same owner,
source, repository, checkout, branch and comparison policy; collection time wins over upload time,
with arrival order breaking ties. Fresh deduplicated submissions can make an
existing review latest again without changing its captured contents or metadata.
The durable stream head survives expiry and pruning so older retained snapshots
do not become latest through housekeeping.

Opening a context or switching scope loads its stored manifest, patches and
available contents through scoped REST endpoints. MCP uses the same stored
context and review services. Comments and reviewed marks are mutable SQLite
state bound to the observation and scope; the captured diff stays immutable.

`GET /api/v2/events` supplies transient server-sent ingestion notifications.
The browser reloads the catalog on events and reconnection. An arrival does not
silently replace the observation being reviewed. The event stream is a hint to
query durable state, not a durable queue or an event replay log.

## Data and identity

The conceptual model is:

```text
user
  repository grouping
    observations from any number of sources and checkouts
      captured metadata
      submissions and observing sessions
      immutable scopes: all / staged / unstaged
        manifest, patches, available contents
        mutable review state
```

An observation can also be unassociated with a repository, such as a standalone
piped patch. Server-assigned context, diff and version IDs identify stored
records; producer paths do not identify server files.

| Identity or metadata | Current meaning                                                                                                                                                                                 |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Account              | Local default user or remotely authenticated individual; never accepted as an arbitrary producer claim.                                                                                         |
| Repository key       | Enrolled in Git metadata, initially derived from sanitized remote URL or source/common directory; preserved through moves and remote changes. Different remote URL forms may enroll separately. |
| Source ID            | Stable local producer identity, or explicit `--source-id` for a container/installation. Hostname is a searchable label, not this identity.                                                      |
| Checkout key         | Enrolled per source in Git metadata, distinguishing linked worktrees and independent sources and surviving directory moves.                                                                     |
| Submission ID        | Identifies one collection and survives retries. A new collection gets a new ID.                                                                                                                 |
| Provenance           | Repository name, remote URL, path, worktree name, branch, HEAD, resolved comparison, hostname, harness/session, triggering directory, trigger, collection time and collector version.           |
| Stored time          | Server timestamp used for catalog ordering; separate from the producer's collection timestamp.                                                                                                  |

Each authenticated user can receive observations
from many containers, including identical branch and path names. Source, session
and checkout metadata preserve their differences. Provenance describes what a
producer reported; it is not proof of repository ownership or trustworthiness.
Identical snapshots share review state within the same user/source/checkout/
branch/comparison identity. Sessions associate with those shared contexts;
renaming a session updates its searchable label without changing snapshot
metadata. The triggering session observed collection; it does not own every
worktree's edits.

SQLite is the durable query source, including the diff data itself. Reads need
neither Git nor a repository mount. Available full contents are captured within
a collection budget; unsupported or unavailable previews remain explicit.
Snapshot retention is configurable in whole days, default seven, after the last
fresh submission. Changing configuration preserves existing expiry until a
fresh submission; retries do not extend it. Reads enforce
expiry immediately; local and remote servers prune at startup and hourly,
removing all scopes, previews, comments and marks. Compact replay identities and
stream heads survive deletion and expiry, preventing replay resurrection and
regression of freshness. See architecture.md for the intentional schema reset.

## Freshness and delivery

Composition decisions and trade-offs live in [architecture.md](architecture.md).

“Latest” means the most recently collected submission within a source, repository,
checkout, branch and comparison policy; arrival order breaks ties. Delayed uploads remain stale.
It does not mean verified current checkout state. Producer collection clocks and
submission arrival order can differ. The API does not contact producers to
establish freshness.

### Failure and retry semantics

Replay identity is **account + source ID + submission ID**. The server hashes
the validated request representation. A matching replay returns the original
context; different content under the same identity fails with a conflict.
A separate identity uses account, source, repository, checkout, branch, HEAD,
source kind, scopes, comparison policy and producer content hash to reuse unexpired snapshots.
The hash covers full content before preview truncation, including binary and
untracked files, modes, renames and staged/unstaged state; mtime and ctime are
excluded. Unsupported full identities fail closed and remain independent.
Producers without a hash dedupe only known empty snapshots. Fresh matching
submissions retain separate retry mappings and provenance, refresh catalog
recency and reset expiry without replacing the original snapshot or review.

The CLI performs a bounded retry of the exact collected request after an
eligible transport failure. It does not recollect the checkout during that
retry. A lost response can mean the transaction committed; replay resolves that
uncertainty without creating a second observation. Validation and conflicting
requests fail explicitly rather than being retried as transport errors.

Hook workers persist bounded immutable pending payloads and acknowledgements.
They retry after subsequent triggers or `collector retry`; no perpetual upload
process or guarantee that an agent hook will run exists. Local acknowledgements
are scoped by session and reconciled with server state before suppressing
uploads. Pending data expires after seven days independently of server retention.
Manual producers report failures. Direct plugins implementing the
contract must preserve submission identity themselves when retrying. A later
manual collection has a new submission ID and may reuse matching content; it
is not a replay of a previous failed submission.

## Local and remote boundaries

Local and remote deployment share application/storage contracts and the HTTP
runtime. Local-only lifecycle discovery is authenticated, private and limited to
status/shutdown for foreground snapshots. Collection in those sessions never uses HTTP.

Remote credentials establish owner identity. REST, MCP, ingestion jobs, job status
and browser assets require authentication. Plugins never fall back to local
routing. Production terminates TLS at the deployment boundary and mounts a
persistent database volume.

## Extension direction

Implement new adapters against the existing consuming-package contracts and
behavioral conformance tests. Preserve atomic commit boundaries, owner isolation,
identity bytes and retry semantics. External queues need durable payload/status
storage plus transactional scheduling intent; multiple processes require a
storage backend designed for that concurrency. SQLite retains one owning process.
