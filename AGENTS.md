# AGENTS

Repository-specific guidance for coding agents and contributors.

## Scope

This repository builds `airgap`, a Go CLI/server for offline content synchronization, transfer, and serving.

## Stack

- Go `1.23`
- Cobra CLI
- SQLite via `modernc.org/sqlite`
- Server-rendered HTML (`html/template`) + static assets

## Canonical Docs

Keep these docs accurate when behavior changes:
- `README.md`
- `docs/architecture.md`
- `docs/configuration.md`
- `docs/http-api.md`
- `docs/release-process.md`
- `CHANGELOG.md`

## Common Commands

```bash
make build
make test
make lint
make fmt
```

If touching release/versioning logic:

```bash
python3 scripts/validate_versions.py
```

## Key Runtime Behavior

- Provider configs are stored in SQLite table `provider_configs`.
- YAML `providers:` are only used for first-run seeding when DB configs are empty.
- Active providers are created from enabled DB configs at startup.
- Server/API provider CRUD triggers hot-reload (`ReconfigureProviders`).

## Provider Types

Recognized types:
- `epel`
- `ocp_binaries`
- `ocp_clients`
- `rhcos`
- `container_images`
- `registry`
- `custom_files`

Notes:
- `registry` is used as a target for image push operations.
- `custom_files` is accepted as config type but not wired as a sync provider yet.

## Code Change Expectations

- Keep changes minimal and focused.
- Prefer updating docs in the same PR when behavior/flags/routes change.
- Do not add placeholder “implemented soon” docs without code backing.
- Preserve changelog heading format: `## X.Y.Z - YYYY-MM-DD` (newest first).
