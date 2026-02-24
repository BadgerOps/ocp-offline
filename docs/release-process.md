# Release Process

`airgap` uses a single semantic version sourced from the top release heading in `CHANGELOG.md`.

## Version Sources

- Changelog: `CHANGELOG.md` (single source of truth)

CI validates changelog format and release ordering (`## X.Y.Z - YYYY-MM-DD`, newest first).

## Prepare a Release

1. Open a PR that updates `CHANGELOG.md` with the new release entry.
2. Ensure CI passes.
3. Merge to `master`.

## Auto-Release on Merge

On every push to `master`, the `Release Image` workflow:

- Reads latest version from `CHANGELOG.md`.
- Checks whether tag `vX.Y.Z` already exists.
- If missing: creates tag/release and publishes container image tags.

Published image tags:

- `ghcr.io/badgerops/airgap:latest`
- `ghcr.io/badgerops/airgap:vX.Y.Z`
