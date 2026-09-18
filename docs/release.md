# Releases

## Release

Required repository secret:

- `HOMEBREW_TAP_TOKEN`: fine-grained token with contents write and pull request
  write access to `flexdinesh/homebrew-tap`.

1. Merge release-ready code to `main`.
2. Run the **Release** workflow. It requires no inputs.
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
