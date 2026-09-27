# Releases

## Stable releases

Only the manual **Release** workflow creates stable version tags and GitHub
Releases. It checks out the latest `main` when the run starts and marks the
release as GitHub's latest stable release. Dispatches from other branches are
skipped.

Install the latest stable release or a specific version:

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@latest
go install github.com/flexdinesh/servediff/cmd/servediff@v0.1.1
```

Required repository secret:

- `HOMEBREW_TAP_TOKEN`: fine-grained token with contents write and pull request
  write access to `flexdinesh/homebrew-tap`.

1. Merge release-ready code to `main`.
2. Run the **Release** workflow with `main` selected. It requires no inputs.
3. The workflow verifies the repository, creates the tag, and publishes the
   GitHub Release with GoReleaser.
4. It generates `Formula/servediff.rb` and opens or updates a pull request in
   `flexdinesh/homebrew-tap`.
5. Merge the tap pull request after its Homebrew checks pass.

Each release contains checksums plus Linux and macOS archives for amd64 and
arm64. Windows archives are also published. Archives include the native binary,
README, and license. The embedded web application needs no installed Node.js
runtime.

The tap branch is deterministic per version, such as `servediff-v0.1.0`.
Rerunning a release whose tag still points to current `main` reuses the existing
GitHub artifacts and updates the same tap pull request. Published artifacts are
not rebuilt or replaced.

The tap repository owns Homebrew style, strict audit, install, and formula test
checks before merge.

## Development releases

Every push to `main` runs **Release dev**, which creates or updates the `dev`
branch to the latest `main` commit. Runs are serialized and resolve `main` when
they start, so a queued run cannot move `dev` back to an older push.

```sh
go install github.com/flexdinesh/servediff/cmd/servediff@dev
```

`dev` is an automatically managed mirror; do not commit to it directly. This
workflow uses `GITHUB_TOKEN` with contents write permission and publishes no
version tags or GitHub Releases. Stable `@latest` installs remain on the highest
stable version tag. Go resolves `@dev` to the commit's pseudo-version, or its
stable version if that commit is already tagged. Module proxies may briefly
cache branch lookups; use `GOPROXY=direct` when an immediate refresh is needed.

## Version series

`.release-version` contains the active `major.minor` release series. It starts
at `0.1`, so the first release is `v0.1.0`; later releases automatically select
`v0.1.1`, `v0.1.2`, and so on. A rerun from the same commit reuses its existing
tag and release.

To begin a new minor or major series, change `.release-version` in the repo. For
example, changing it to `0.2` makes the next release `v0.2.0`; changing it to
`1.0` makes the next release `v1.0.0`. Later releases continue incrementing that
series' patch number.

## Embedded assets

Release-ready frontend assets are committed under `internal/webui/dist` so
`go install github.com/flexdinesh/servediff/cmd/servediff@latest` produces a
complete binary. CI rebuilds these assets and rejects drift. Frontend changes
must include the regenerated assets:

```sh
task web:stage
```
