# Releases

## Stable releases

Only the manual **Release** workflow creates stable version tags and GitHub
Releases. It checks out the latest `main` when the run starts and marks the
release as GitHub's latest stable release. Dispatches from other branches are
skipped.

Full test suites run locally through the pre-push hook. Release verification
runs static checks, rebuilds generated files, rejects drift, and smoke-tests the
distribution before publishing, using the same `mise run check:ci` task as PR CI.
Release steps run through `release:*` mise tasks and pnpm scripts; GoReleaser
is installed only by `mise run release:publish`.

Install the latest stable release or a specific version:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx@latest
go install github.com/flexdinesh/diffx/cmd/diffx@v0.2.0
```

Required repository secret:

- `HOMEBREW_TAP_TOKEN`: fine-grained token with contents write and pull request
  write access to `flexdinesh/homebrew-tap`.

1. Merge release-ready code to `main`.
2. Run the **Release** workflow with `main` selected. It requires no inputs.
3. The workflow verifies the repository, creates the tag, and publishes the
   GitHub Release with GoReleaser.
4. It generates `Formula/diffx.rb` and opens or updates a pull request in
   `flexdinesh/homebrew-tap`.
5. Merge the tap pull request after its Homebrew checks pass.

Each release contains checksums plus Linux and macOS archives for amd64 and
arm64. Windows archives are also published. Archives include the native binary,
README, and license. The embedded web application needs no installed Node.js
runtime.

Stop running binaries before upgrading. Schema 9 intentionally resets older
versioned databases on first open; current state survives subsequent starts.
Future or unrecognized schemas are refused. Upgrade server and collectors
together for ingestion protocol 4. In-memory state lasts only for one process.

The tap branch is deterministic per version, such as `diffx-v0.2.0`.
Rerunning a release whose tag still points to current `main` reuses the existing
GitHub artifacts and updates the same tap pull request. Published artifacts are
not rebuilt or replaced.

The tap repository owns Homebrew style, strict audit, install, and formula test
checks before merge.

## Development installs

Install the latest code directly from `main`, including unreleased changes:

```sh
go install github.com/flexdinesh/diffx/cmd/diffx@main
```

No publishing workflow or release tag is required. Stable `@latest` installs
remain on the highest stable version tag. Go resolves `@main` to the commit's
pseudo-version, or its stable version if that commit is already tagged. Module
proxies may briefly cache branch lookups; use `GOPROXY=direct` when an immediate
refresh is needed.

## Version series

`.release-version` contains the active `major.minor` release series. It is
`0.2`, so the first release is `v0.2.0`; later releases automatically select
`v0.2.1`, `v0.2.2`, and so on. A rerun from the same commit reuses its existing
tag and release.

To begin a new minor or major series, change `.release-version` in the repo. For
example, changing it to `0.3` makes the next release `v0.3.0`; changing it to
`1.0` makes the next release `v1.0.0`. Later releases continue incrementing that
series' patch number.

## Embedded assets

Release-ready frontend assets are committed under `internal/webui/dist` so
`go install github.com/flexdinesh/diffx/cmd/diffx@latest` produces a
complete binary. CI rebuilds these assets and rejects drift. Frontend changes
must include the regenerated assets:

```sh
mise run web:stage
```
