# Architecture

servediff is a polyglot monorepo with two server implementations and one web
application. Node remains the reference implementation while Go reaches parity.

## Project boundaries

- `apps/server`: existing Node CLI, REST server, persistence, and Git adapters.
- `apps/web`: React/Vite UI. It consumes the REST contract, never Go packages.
- `cmd/servediff`: Go composition root and CLI only.
- `internal`: Go application behavior and private adapters shared by future Go commands.
- `packages/api`: canonical OpenAPI contract, generated TypeScript client, and Go embedding shim.
- `packages/shared`: TypeScript-only review and UI behavior; not a cross-language model package.
- `test/fixtures`: serialized inputs shared across implementations.

Go HTTP and CLI code compose domain, source, and store packages. A future MCP
transport should compose those same packages; extract shared application
services once a second transport establishes the reusable behavior. Transports
must not call each other. Cross-language sharing happens through OpenAPI and
serialized fixtures, not source imports.

## Development modes

- `task dev:web`: Vite with the in-memory fixture API.
- `task dev:node`: existing Node server with the shared patch fixture.
- `task dev:go`: Go server with the same fixture and a Vite production build.
- `task dev`: Go fixture API and Vite HMR through a development proxy.

## Production builds

The Node build remains supported. The Go build generates the API client, builds
the web application, stages its output below `internal/webui`, then embeds it in
`dist/servediff-go`. Node and pnpm are build dependencies, not Go binary runtime
dependencies. Live repository mode still requires the Git executable.

## Dependency rules

- Deployable applications do not import other applications.
- Go commands contain wiring only; reusable Go code stays in `internal`.
- API wire types originate in OpenAPI.
- Shared packages must have a specific purpose and a real second consumer.
- Add another Go module only for an independently versioned/released component.
