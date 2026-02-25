# airgap

`airgap` is a single Go binary for syncing and serving offline content for disconnected environments.

It supports:
- RPM repository mirroring (EPEL)
- OpenShift binaries and client artifact mirroring
- Container image mirroring metadata/blob sync
- Export/import workflows for physical transfer media
- A built-in web UI + HTTP API

## Current Status

The codebase is active and functional (not just scaffolding). CLI, server, sync engine, provider registry, transfer engine, and SQLite persistence are implemented.

## Requirements

- Go `1.23+` (for local builds)
- Optional external tools:
  - `skopeo` (required for `registry push` operations)
  - `createrepo_c` (optional but recommended for RPM metadata regeneration after import)

## Build and Test

```bash
make build
make test
```

Manual build:

```bash
go build -o bin/airgap ./cmd/airgap
```

## Quick Start

1. Copy and edit the example config:

```bash
cp configs/airgap.example.yaml ./airgap.yaml
```

2. Run a sync (all enabled providers):

```bash
./bin/airgap sync
```

3. Start the UI/API server:

```bash
./bin/airgap serve
```

Default listen address is `0.0.0.0:8080`.

## Configuration

Config file discovery order:
- `./airgap.yaml`
- `/etc/airgap/airgap.yaml`
- `$HOME/.config/airgap/airgap.yaml`

Top-level sections:
- `server`
- `export`
- `schedule`
- `providers`

For full details, see [docs/configuration.md](docs/configuration.md).

## Provider Types

Provider configs are stored in SQLite (`provider_configs`). YAML provider entries are used for first-run seeding when the table is empty.

Implemented provider types:
- `epel`
- `ocp_binaries`
- `ocp_clients`
- `rhcos`
- `container_images`

Supported as config/target types:
- `registry` (used as a destination for `registry push`)
- `custom_files` (accepted config type; sync implementation is not wired yet)

## CLI Commands

- `sync`: sync one/all providers
- `validate`: validate local files against provider metadata
- `status`: provider status summary from store state
- `export`: create split `tar.zst` transfer archives + manifest
- `import`: verify/import transfer archives
- `serve`: web UI + API server
- `providers list`: list provider configs from SQLite
- `registry push`: push mirrored container images to a registry target
- `config show`: print loaded config
- `config set`: currently a stub (prints intended change; does not persist)

## Web UI and API

Main pages:
- `/dashboard`
- `/providers`
- `/providers/{name}`
- `/transfer`
- `/ocp/clients`

API routes are documented in [docs/http-api.md](docs/http-api.md).

## Architecture and Data Flow

For architecture and runtime flow, see [docs/architecture.md](docs/architecture.md).

## Release Process

Release/version workflow is changelog-driven. See [docs/release-process.md](docs/release-process.md).

## Changelog

All releases are tracked in [CHANGELOG.md](CHANGELOG.md).
