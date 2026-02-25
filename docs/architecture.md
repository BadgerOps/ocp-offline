# Architecture

## Overview

`airgap` is a single-process Go application that provides:
- CLI operations for sync/validate/export/import/push
- An embedded HTTP server for UI + API
- SQLite-backed state tracking

Core packages:
- `cmd/airgap`: CLI command wiring
- `internal/engine`: sync/export/import orchestration
- `internal/provider/*`: provider implementations
- `internal/store`: SQLite models, migrations, CRUD
- `internal/server`: web UI and API handlers
- `internal/download`: HTTP download client + worker pool

## Startup Flow

1. Load config from YAML (or defaults).
2. Initialize store and run migrations.
3. Seed `provider_configs` from YAML providers on first run only.
4. Load provider configs from DB.
5. Instantiate enabled providers and register them.
6. Start CLI command execution (or HTTP server for `serve`).

## Sync Flow

1. CLI/API requests sync for one provider or all.
2. Engine calls provider `Plan()` to produce `SyncAction` items.
3. Engine executes download/update actions with worker pool.
4. Engine updates `file_records`, `sync_runs`, and failed-file state.
5. Status is served from store-backed summaries.

Notes:
- Sync/push operations are serialized at server level (`syncRunning` guard).
- Progress is tracked through an in-memory `SyncTracker` used by UI polling/SSE paths.

## Transfer Flow

### Export

- Reads file inventory from `file_records`
- Builds split `airgap-transfer-XXX.tar.zst` archives
- Writes archive SHA256 sidecars
- Writes `airgap-manifest.json` (+ `.sha256`) and `TRANSFER-README.txt`
- Records transfer in `transfers`

### Import

- Reads and validates manifest + archives
- Supports verify-only and skip-validated modes
- Extracts files into `server.data_dir`
- Attempts `createrepo_c` for RPM repositories
- Upserts `file_records` from manifest inventory

## Provider Model

All providers implement:
- `Name()`
- `Type()`
- `Configure()`
- `Plan()`
- `Sync()`
- `Validate()`

Provider instances are registered by config name, allowing multiple provider configs of the same type.

## Persistence

SQLite tables include:
- `sync_runs`
- `file_records`
- `failed_files`
- `transfers`
- `transfer_archives`
- `provider_configs`

Migrations are managed in `internal/store/migrations.go`.

## HTTP Surface

Server routes include:
- UI pages (`/dashboard`, `/providers`, `/transfer`, `/ocp/clients`)
- Sync/status/provider APIs
- Provider config CRUD APIs
- Transfer APIs
- Mirror discovery/speed-test APIs
- OCP client artifact discovery/download APIs

See [http-api.md](http-api.md) for endpoint-level details.
