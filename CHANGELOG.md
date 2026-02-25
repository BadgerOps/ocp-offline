# Changelog

All notable changes to this project are documented in this file.

## 0.3.4 - 2026-02-24

### Added

- Multi-architecture container image builds: release now produces `linux/amd64` and `linux/arm64` images behind a single manifest tag, mirroring the binary release platform matrix.
- QEMU emulation setup in release workflow to enable cross-platform container builds on GitHub Actions runners.

## 0.3.3 - 2026-02-24

### Fixed

- Prevented release container builds from failing when `createrepo_c` is unavailable in UBI minimal repositories.
- Runtime image now attempts `createrepo_c` install and continues cleanly when the package cannot be resolved.
- Added `.dockerignore` rules to keep large local artifact directories out of Docker build context.

## 0.3.2 - 2026-02-24

### Fixed

- Release workflow now publishes downloadable GitHub Release binary assets for:
  - Linux `x86_64`
  - Linux `arm64`
  - macOS `arm64`
- Release workflow now publishes SHA256 checksums for all released binary tarballs.
- Preserved post-merge release behavior: release assets and GHCR package publishing run only on push to `master`.

## 0.3.1 - 2026-02-24

### Fixed

- Resolved repository-wide `golangci-lint` findings surfaced by CI (`errcheck` and `staticcheck`).
- Hardened response/file/row close and write paths across download, engine, provider, server, and store components.
- Updated tests and handlers to consistently check/handle returned errors, matching CI lint requirements.
- CI now skips PR binary builds; binary build and release only run on push to `master` (post-merge).

## 0.3.0 - 2026-02-24

### Added

- Container image URL mirroring support and provider listing CLI enhancements.
- Far-side registry push workflow for container image synchronization.
- GitHub Actions CI and release automation with image publish to GHCR on merge to `master`.
- Repository-managed pre-commit hooks for formatting, lint, unit tests, and version/changelog validation.

### Changed

- Standardized release flow around changelog-driven versioning and automatic `vX.Y.Z` tagging.

## 0.2.0 - 2026-02-23

### Added

- OCP clients provider with SHA256 manifest-driven artifact discovery and download support.
- Provider configuration edit workflow in the management UI.
- Failed download management improvements including manual clear, multi-select, and clear-all actions.

### Changed

- Hardened sync path safety, HTTP limits, and container runtime handling.
- Updated sync UI behavior to only show cancel controls while a sync is active.

## 0.1.0 - 2026-02-22

### Added

- Initial `airgap` CLI/server implementation with export/import engine foundations.
- Core provider framework and configuration model for EPEL, OCP, and RHCOS content sources.
- Mirror discovery services, parsing logic, and API endpoints for upstream version discovery.
- Baseline sync, validation, and UI workflows.
