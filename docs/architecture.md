# Architecture

Diffx has one stored-data application with two compositions. This is the
canonical record of package boundaries and architectural decisions.
[System semantics](system.md) covers identities and durability;
[development](development.md) covers commands and checks.

## Modes are compositions

| Concern    | Local `diffx [PATH]`                            | Remote `diffx-server`                    |
| ---------- | ----------------------------------------------- | ---------------------------------------- |
| Collection | Selected checkout or stdin, once                | Separate finite sync or hook producer    |
| Admission  | Direct application call before serving          | Authenticated durable queue and worker   |
| Lifetime   | Foreground; authenticated singleton replacement | Foreground; graceful worker shutdown     |
| Storage    | One owner of persistent SQLite                  | One owner of persistent SQLite           |
| Queries    | Stored observations via REST/MCP                | Same application services via REST/MCP   |
| Access     | Public listener; private lifecycle credential   | Account credentials wrap the application |

Source support and review policy resolve snapshot capabilities. A mode never
invents contents, scopes or refresh support. Queue presence at the composition
root enables remote admission; no queue means no admission routes.
Stored observations cannot refresh from Git.

Private local control supports authenticated status/shutdown only. Replacement
uses lifecycle locking, a validated descriptor and graceful shutdown; a PID
alone is never authority. `diffx dev --fixture FILE` is isolated from
personal lifecycle discovery.

## Modules own behavior

- `review`: data, source contract and pure comment/patch rules.
- `collector`, `diffsource`: Git, discovery, parsing and finite collection.
- `ingestion`: versioned request/job contracts, validation and HTTP client.
- `contextservice`: owner-scoped publication, resolution and catalog.
- `reviewservice`: shared snapshot reads, comments, marks and capability checks.
- `reviewdata`: storage-independent identities, results and errors.
- `reviewstore`: SQLite transactions, stored reads, retention and queue adapter.
- `ingestionqueue`: admission/lease contract and worker failure policy.
- `submission`: immutable recovery, destination identity and delivery, shared by
  manual sync and hooks. No dependency on hook scheduling.
- `hooks`: finite scheduling, coalescing and session-aware suppression.
- `httpapi`, `mcpapi`: transport parsing, projection and error mapping.
- `serverapp`: transport/task composition; `daemon`: local lifecycle;
  `remoteserver`: remote authentication and worker composition.
- `config`, `processlock`, `browser`, `version`: configuration, process ownership,
  browser launch and build metadata; `processmetrics`: server process metrics;
  `webui`: embedded assets. `testsupport` is test-only.

Interfaces live beside consumers and require the needed operation. Optional
interface assertions must not change application behavior. Storage contracts
describe atomic application operations, not independently committed CRUD steps.

## Principles and limits

1. Share semantics, not coincidental syntax. DRY gives identity, authorization,
   replay and review rules one owner. Local direct ingestion and remote queue
   admission have different failure contracts and remain distinct.
2. Separate collection from queries. Reads survive checkout removal without Git.
   Failed collection never becomes a successful empty observation.
3. Compose policy at entry points. Keep mode branches out of domain operations;
   prefer small explicit policies over a generic feature framework.
4. Immutable content, mutable review state. Fresh matching submissions can reuse
   review state and extend retention. Exact retries cannot change retention or
   resurrect deleted content. Identity commits with the observation.
5. Authorize before resource access. Owner/context/scope checks apply equally to
   REST, MCP, historical versions, mutations and job polling.
6. Acceptance is not completion. Remote 202 means durable admission; succeeded
   means committed publication. Retries preserve payload and submission ID.
7. Cancellation and ownership are contracts. Stop workers and serving before
   releasing database ownership. Uncertain delivery stays pinned to its target.
8. Generate wire types; share examples for cross-language rules. TypeScript
   aliases OpenAPI types. Real HTTP responses need runtime schema checks.
9. Prefer boundary tests; keep valuable unit tests. Parsing, hashing, leases,
   retry policy and UI recovery have independent failure modes. Delete obsolete
   behavior tests and implementation-shape assertions, not useful small tests.
10. Add complexity for an observed requirement. No REST cache for cheap immutable
    database reads, background review daemon, or speculative infrastructure.

## Contracts and evolution

`mise run test:contracts` runs production dependency checks, storage/lifecycle/
queue/transport boundaries, frontend token guards, shared Go/TypeScript comment
examples and real HTTP schema validation. Local `mise run check` and pre-push
run these checks alongside the web unit/browser and plugin/shared suites.
`test:contracts:rules` reuses built binaries so the full local check does not
rebuild or repeat the distribution smoke test and JavaScript suites. CI and
release verification retain static checks, builds, generated-file consistency
and the distribution API smoke test; full suites and guards stay local.

`mise run check:boundaries` classifies every package under `cmd/` and `internal/`
using `tools/check-boundaries`. Unclassified packages, forbidden dependencies and
production imports of `testsupport` fail. Process execution and SQL access stay
with their explicit owners. The checker inspects production imports for the
current build platform; tests may import adapters to exercise their contracts.

## Making changes

Before implementation, identify the behavior's owner, consuming contract and
invariants from this document and [system.md](system.md). Put shared rules in
their existing owner and compose delivery/lifecycle differences at entry points.
For shared changes, verify both compositions through existing boundary tests;
add a regression test when coverage is missing. Check semantic outcomes rather
than private implementation shape.

The PR template records ownership, contract/mode implications, validation and
intentional design changes. Change a boundary policy only for a concrete new
responsibility, updating its rationale and tests in the same PR. Passing checks
does not establish cohesive modules, correct DRY abstractions or visual quality;
those remain review responsibilities. `main` requires the GitHub Actions `checks`
status through the `ci-required` ruleset. That status covers lightweight CI;
architecture/design guards and full-suite validation are enforced locally by
pre-push and recorded in the PR evidence.

## Adapter and schema evolution

New adapters must preserve ownership, atomic publication, retention, replay,
lease fencing and commit-before-ack recovery. External brokers require atomic
scheduling intent (an outbox). Multiple server processes need a storage backend
designed for that concurrency; SQLite retains one owning process.

Only public v2 routes and ingestion protocol 4 remain. Retired: `review`,
`service`, `serve`, `capture`, background daemon startup, public v1,
synchronous ingestion and legacy capture/worktree storage.

Diffx starts with fresh configuration, state and producer identities under its
own directories. There are no aliases or migration paths. Ingestion protocol 4
uses `X-Diffx-State` to pin submission and polling to the destination identity;
older protocols are rejected. Release artifacts use the explicit `diffx` project
name, independent of the checkout directory.

Schema 9 intentionally resets older versioned databases in one transaction under
the ownership lock. Current state survives subsequent starts; future or
unrecognized schemas are refused. Upgrade server and collectors together.
Versioned producer directories ignore old manual/hook pending payloads.
Stop old binaries before upgrading.
