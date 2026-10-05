# Architecture

For system responsibilities, request flows and design decisions, see
[system.md](system.md).

servediff is a Go/React monorepo with two server composition roots and one
Git-aware producer pipeline. Node is a frontend build/test dependency only.

## Collection and ingestion

`servediff review` collects a checkout once. Bundled agent plugins invoke
`servediff hook`, which schedules a detached, finite worker using the same Go
collector and ingestion operation. `servediff pipe` collects a supplied patch and attaches Git metadata
when its submission directory is a checkout. There is no watcher.

The producer captures repository, branch, HEAD, worktree, source, hostname,
agent/run, trigger and collection metadata together with all/staged/unstaged
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
repository, checkout, branch, HEAD and stable content hash match. The content
hash covers all/staged/unstaged data, exact file contents and file modes before
preview limits; filesystem timestamps are excluded. Unknown full content
identities remain independent. Older producers without a content hash dedupe
only empty snapshots. Independent sources and checkouts stay separate.

Retry identity remains account/source/submission ID: identical replays return
the original result; changed payloads conflict. Each fresh submission retains
its own retry mapping and captured metadata. Reusing a snapshot preserves its
original manifests, previews, comments and reviewed marks; only its last
submission time and seven-day expiry advance. Retries do not extend retention.
Expired snapshots are hidden from reads, then pruned with all scopes and review
state. Migration applies retention from existing last submission times and
preserves duplicate histories until expiry.

Repository grouping is separate from source and checkout identity. Paths and
hostnames are captured labels, not global identities. Container sources can
supply `--source-id` and `--run-id` so identical branch/path names remain
distinguishable. User/account identity comes from trusted server composition,
not an arbitrary ingestion field. Multi-account remote authentication is a
future extension; the first remote deployment uses one configured account.

An observation is an immutable review context, addressed at `/contexts/{id}`.
Its branch/worktree metadata does not change when the producer switches branch.
The API reads manifests, patches, available contents and review state from
SQLite. Queries never invoke Git, refresh a checkout, or require its continued
existence. Freshness is push-only: latest means the most recently collected
submission in each stream, with arrival order breaking ties. It does not guarantee
current filesystem state.

`GET /api/v2/contexts` supports metadata search and repository, branch, worktree,
hostname, source and run filters. The browser chooses a repository, then an
observation. `GET /api/v2/events` emits transient ingestion notifications;
clients query durable catalog state after reconnecting. The picker filters
freshness, host, branch and worktree before repository grouping. Latest snapshots
with changes are the default; the All checkbox includes unavailable, empty and
unknown-status entries. Unavailable legacy entries are disabled when shown.
Freshness uses a durable stream head for the same owner, source, repository,
checkout and branch, ordered by collection time with arrival order breaking ties.
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
browser access uses HTTP Basic with the configured account and token. Remote
deployment supplies TLS through its hosting environment. The remote server
requires no Git executable, repository mount or collector process. A configured
remote endpoint never starts a local service.

Remote auth, account resolution, future storage adapters and optional queue
infrastructure belong at composition boundaries. Hook scheduling records a
request, while a receipt still means durable publication. Hook workers retain
bounded pending payloads locally and retry on subsequent completion triggers;
there is no upstream relay or perpetual upload process. Manual collection
commands continue reporting network failures directly.

Hook discovery starts at the supplied directory, scans at most two child levels
and 128 directories, then inspects registered worktrees and unmerged local
branches. Live feature branches compare working contents with the merge base of
the local default branch; staged/unstaged retain HEAD/index semantics. Branches
without checkouts collect from immutable Git objects. Discovery never fetches
or creates worktrees. Manual review preserves its HEAD default and exposes
`--base` and `--branch` for explicit recovery.

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

## Migration

SQLite migrations preserve historical comments, reviewed marks and captures.
Legacy worktree contexts remain in the catalog but cannot load Git through the
server; create a new observation with `servediff review`. Retained captures
remain queryable with their existing IDs. Unsupported schemas fail without
deleting existing data. Stop older binaries before upgrading because older
processes may not honor current ownership locks.

## Project boundaries

- `apps/web`: React/Vite REST client and review dashboard.
- `cmd/servediff`, `cmd/servediff-server`: local/remote composition roots.
- `internal/ingestion`: producer/server wire contract and HTTP client.
- `internal/collector`: Git-aware collection shared by manual/agent triggers.
- `internal/hooks`: detached sync scheduling, coalescing, retry state and cooldown.
- `internal/contextservice`: ingestion orchestration and database-backed catalog.
- `internal/reviewservice`: shared review operations.
- `internal/reviewstore`: SQLite migrations, atomic observations and review state.
- `internal/httpapi`, `internal/mcp`: query/ingestion transport adapters.
- `internal/daemon`, `internal/controlapi`: local lifecycle and private discovery.
- `internal/diffsource`: producer-side Git/patch adapters and stored sources.
- `packages/api`: canonical OpenAPI and generated TypeScript client.
- `packages/shared`: TypeScript review/UI behavior.
- `packages/plugin-*`: native local agent adapters; Go owns collection/ingestion.

Transports compose shared application operations; they do not call each other.
Cross-language contracts use OpenAPI and serialized fixtures. Add storage or
queue interfaces around actual application operations when a second backend
exists, rather than introducing a generic backend framework.

Sources advertise capabilities derived from stored scopes and available
contents. Observation refresh is unavailable. The API and UI enforce those
capabilities; provenance is not a substitute for capability checks.

Production builds generate the API client, build the frontend and embed staged
assets in the binaries. Collectors require Git; servers and browsers do not.
