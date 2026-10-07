# Architecture

For system responsibilities, request flows and design decisions, see
[system.md](system.md).

servediff is a Go/React monorepo with two server composition roots and one
Git-aware producer pipeline. Node is a frontend build/test dependency only.

## Collection and ingestion

`servediff review` collects the originating checkout and all registered worktrees
once, then opens the origin's review URL. Bundled agent plugins invoke
`servediff hook`, which schedules a detached, finite worker using the same Go
collector and ingestion operation. `servediff pipe` collects a supplied patch
without Git lookup; its absolute submission directory is provenance only.
There is no watcher.

The producer captures repository, branch, HEAD, worktree, source, hostname,
agent session (harness, ID, optional name), triggering directory, resolved
comparison, trigger and collection metadata together with all/staged/unstaged
snapshots and bounded file previews. Piped observations support only all scope.
Git processes and filesystem access belong to collection, never server queries.

Collection precedes submission. A changed checkout during collection fails or
is retried within the collection bound; a failed collection is not published as
an empty diff. A successful empty snapshot records that there were no changes.

Both local and remote ingestion validate the same versioned request, then
atomically persist its observation, scopes, immutable previews, review bindings
and retry identity. Acknowledgement means SQLite committed the observation.
The REST endpoint is `POST /api/v2/ingestions`; local discovery uses authenticated
loopback control with the same application operation.

## Identity and stored state

Fresh submissions reuse an unexpired observation when account, source,
repository, checkout, branch, HEAD, comparison policy and stable content hash match. The content
hash covers all/staged/unstaged data, exact file contents and file modes before
preview limits; filesystem timestamps are excluded. Unknown full content
identities remain independent. Older producers without a content hash dedupe
only empty snapshots. Independent sources and checkouts stay separate.

Retry identity remains account/source/submission ID: identical replays return
the original result; changed payloads conflict. Each fresh submission retains
its own retry mapping and captured metadata. Reusing a snapshot preserves its
original manifests, previews, comments and reviewed marks; only its last
submission time and configurable expiry advance. Retention defaults to seven
days; changing settings preserves existing expiry until a fresh submission.
Retries do not extend retention. Session associations survive content deduplication;
session labels can change without mutating captured metadata.
Expired snapshots are hidden from reads, then pruned with all scopes and review
state. Migration applies retention from existing last submission times and
preserves duplicate histories until expiry.

Repository grouping is separate from source and checkout identity. Paths and
hostnames are captured labels, not global identities. Container sources can
supply `--source-id` and `--run-id` so identical branch/path names remain
distinguishable. User/account identity comes from trusted server composition,
not an arbitrary ingestion field. Remote credentials resolve individual users;
one SQLite database stores all users, with user-scoped operations and events.

An observation is an immutable review context, addressed at `/contexts/{id}`.
Its branch/worktree metadata does not change when the producer switches branch.
The API reads manifests, patches, available contents and review state from
SQLite. Queries never invoke Git, refresh a checkout, or require its continued
existence. Freshness is push-only: latest means the most recently collected
submission in each stream, with arrival order breaking ties. It does not guarantee
current filesystem state.

`GET /api/v2/contexts` supports metadata search and repository, branch, worktree,
hostname, source, run, harness, session ID and session name filters. Submission
associations supply session matches independently of original snapshot metadata.
The browser chooses a repository, then an
observation. `GET /api/v2/events` emits transient ingestion notifications;
clients query durable catalog state after reconnecting. The picker filters
freshness, host, branch and worktree before repository grouping. Latest snapshots
with changes are the default; the All checkbox includes unavailable, empty and
unknown-status entries. Unavailable legacy entries are disabled when shown.
Freshness uses a durable stream head for the same owner, source, repository,
checkout, branch and comparison policy, ordered by collection time with arrival order breaking ties.
Pruning expired reviews preserves that head so retained older snapshots stay stale.
Notifications are not a queue or proof of delivery.

## Local and remote composition

`cmd/servediff` provides explicit collection commands and local service
management. Local submission starts or reuses the per-user background server,
prints the committed review/MCP URLs and exits. Service discovery, lifetime
locks and database ownership prevent competing local instances. The private
runtime descriptor is discovery state, not producer identity.

`cmd/servediff-server` runs the remote server in the foreground with the same
ingestion/query core and SQLite adapter. Bearer authentication protects API/MCP;
browser access uses HTTP Basic with username/token. Persistent bootstrap creates
an admin and generated private token file; explicit user provisioning requires
stopping the server. Remote
deployment supplies TLS through its hosting environment. The remote server
requires no Git executable, repository mount or collector process. A configured
remote endpoint never starts a local service.

Remote auth, account resolution, future storage adapters and optional queue
infrastructure belong at composition boundaries. Hook scheduling records a
request, while a receipt still means durable publication. Hook workers retain
bounded pending payloads locally and retry on subsequent completion triggers;
there is no upstream relay or perpetual upload process. Manual collection
commands continue reporting network failures directly.

Discovery resolves the supplied Git directory and its registered worktrees,
without a worktree-count limit. Hooks ignore non-Git input. Child-repository
scanning and unmerged branch recovery are excluded from the default policy.
Manual and automatic collection compare working contents with the merge base
of the local default branch; staged/unstaged retain HEAD/index semantics.
`--base HEAD` requests only working changes. `review --branch` explicitly collects
immutable branch objects without reading the checkout's dirty files. Discovery
never fetches or creates worktrees.

Collector repository, checkout, and branch identities persist in Git metadata.
Paths, branch labels, and remote URLs are hints, so moves and renames preserve
identity. Queue namespaces also include config and routing environment. Workers
verify identities before using updated location hints. Saved payloads precede
destination resolution and survive a removed checkout; replay keeps submission
identity and respects destination/database identity. A filesystem copy carrying
Git enrollment metadata retains the same identity; a fresh clone enrolls anew.

Private collector state under `~/.local/state/servediff/hooks` stores finite
requests, immutable pending payloads, acknowledgements, location hints and job
statuses. Structured rotating `hooks.log` records discovery, collection,
ingestion, waiting, errors and skip decisions, excluding credentials and captured
contents. `collector status` exposes latest job statuses; `collector retry`
schedules incomplete jobs in the current routing/config namespace. No perpetual
retry worker or unrestricted filesystem search runs.
Automatic collection skips initially clean checkouts, publishes dirty-to-clean
transitions and records new sessions observing unchanged snapshots. Session-scoped
acknowledgements reconcile with server state; manual review always submits.
Pending payload retention stays seven days, independent of server retention.

## Migration

SQLite migrations preserve historical comments, reviewed marks and captures.
Legacy worktree contexts remain in the catalog but cannot load Git through the
server; create a new observation with `servediff review`. Retained captures
remain queryable with their existing IDs. Unsupported schemas fail without
deleting existing data. Stop older binaries before upgrading because older
processes may not honor current ownership locks.

## Project boundaries

- `apps/web`: React/Vite REST client and review dashboard.
- `cmd/servediff`, `cmd/servediff-server`: composition roots; shared producer destination/submission orchestration in the CLI.
- `internal/ingestion`: producer/server wire contract and HTTP client.
- `internal/collector`: Git-aware collection and explicit discovery policies.
- `internal/hooks`: detached sync scheduling, coalescing, retry state and cooldown.
- `internal/contextservice`: ingestion orchestration and database-backed catalog.
- `internal/reviewservice`: shared review operations.
- `internal/reviewstore`: SQLite migrations, observations, session associations, credentials and review state.
- `internal/httpapi`, `internal/mcpapi`: REST/MCP transport adapters.
- `internal/daemon`, `internal/controlapi`: local lifecycle and private discovery.
- `internal/diffsource`: producer-side Git/patch adapters and stored sources.
- `packages/api`: canonical OpenAPI and generated TypeScript client.
- `packages/shared`: TypeScript review/UI behavior.
- `packages/plugin-*`: native local agent adapters; Go owns collection/ingestion.

Transports compose shared application operations; they do not call each other.
REST review mutations live in `reviewservice`. Global MCP `/mcp` provides context
search, stored diffs/patches and comments using explicit context IDs; scoped MCP
URLs retain a fixed default context. All resolution stays user-scoped.
Cross-language contracts use OpenAPI and serialized fixtures. Add storage or
queue interfaces around actual application operations when a second backend
exists, rather than introducing a generic backend framework.

Sources advertise capabilities derived from stored scopes and available
contents. Observation refresh is unavailable. The API and UI enforce those
capabilities; provenance is not a substitute for capability checks.

Production builds generate the API client, build the frontend and embed staged
assets in the binaries. Collectors require Git; servers and browsers do not.
