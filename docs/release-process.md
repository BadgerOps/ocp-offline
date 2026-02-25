# Release Process

Releases are changelog-driven and automated on push to `master`.

## Source of Truth

Version is read from the top release heading in `CHANGELOG.md`:

```text
## X.Y.Z - YYYY-MM-DD
```

Validation is enforced by:
- `scripts/validate_versions.py`
- CI workflow checks

## CI Workflow

`CI` runs on PRs and pushes to `master`:
- changelog/version validation
- lint + unit tests
- binary build artifact on push to `master`

## Release Workflow

`Release` runs on push to `master`.

Flow:
1. Validate changelog format and ordering.
2. Read latest version from `CHANGELOG.md`.
3. Skip if tag `vX.Y.Z` already exists.
4. Create and push git tag `vX.Y.Z`.
5. Build release binaries with embedded version/commit/build-time.
6. Publish GitHub Release + assets.
7. Build/push multi-arch container image to GHCR.

## Published Artifacts

GitHub Release assets:
- `airgap_vX.Y.Z_linux_x86_64.tar.gz`
- `airgap_vX.Y.Z_linux_arm64.tar.gz`
- `airgap_vX.Y.Z_darwin_arm64.tar.gz`
- `checksums.txt`

Container image tags:
- `ghcr.io/badgerops/airgap:latest`
- `ghcr.io/badgerops/airgap:vX.Y.Z`

## Local Validation Before Release PR

```bash
python3 scripts/validate_versions.py
```
