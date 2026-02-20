# Implementation Phases

#airgap #roadmap #phases

Back to [[00 - Project Index]] | Related: [[02 - Architecture]], [[08 - Migration Path]]

## Overview

Six phases over approximately 12 weeks. Each phase produces a working, testable increment. The app is usable from the CLI after Phase 1.

---

## Phase 1: Foundation (Weeks 1–2)

**Goal:** Working CLI that can sync OCP binaries and RHCOS images — proving the core architecture with code you already have.

### Deliverables

- [ ] Project scaffolding: Go module (`github.com/BadgerOps/airgap`), Makefile, Containerfile
- [ ] `internal/config/` — YAML config loader with validation
- [ ] `internal/store/` — SQLite schema, migrations, basic CRUD for sync_runs and file_inventory
- [ ] `internal/download/` — HTTP client with retry, exponential backoff, jitter, HTTP Range resume, streaming SHA256 validation
- [ ] `internal/download/pool.go` — concurrent download worker pool (configurable goroutine count)
- [ ] `internal/provider/provider.go` — Provider interface definition
- [ ] `internal/provider/ocp/binaries.go` — OCP binaries provider (ported from [[01 - Existing Repos Audit#ocpsync|ocpsync]])
- [ ] `internal/provider/ocp/rhcos.go` — RHCOS provider (ported from ocpsync)
- [ ] `internal/engine/sync.go` — SyncManager that orchestrates Plan → Sync → Report
- [ ] `cmd/airgap/main.go` — Cobra root command + `sync` subcommand
- [ ] `configs/airgap.example.yaml` — example config
- [ ] Unit tests for config loading, download client, checksum validation

### Key Decisions in This Phase

- Confirm `chi` vs stdlib router (can defer since no HTTP server yet)
- Confirm `modernc.org/sqlite` vs `mattn/go-sqlite3` (prefer pure Go)
- Set up CI (GitHub Actions) for build + test

### Done When

`airgap sync --provider ocp_binaries` downloads OCP client binaries with retry and checksum validation, matching the behavior of the original `ocpsync`.

---

## Phase 2: EPEL Provider + Validation (Weeks 3–4)

**Goal:** Port the EPEL sync logic from Python to Go, add validation engine, begin web UI.

### Deliverables

- [ ] `internal/provider/epel/epel.go` — EPEL provider:
  - repomd.xml parsing via `encoding/xml`
  - primary.xml.gz decompression and parsing
  - Checksum-based dedup (same algorithm as [[01 - Existing Repos Audit#epel-offline-sync|epel-offline-sync]])
  - `cleanup_removed_packages` capability (fixes known limitation)
- [ ] `internal/provider/epel/epel_test.go` — tests with fixture XML files
- [ ] `internal/engine/validate.go` — validation engine (runs Validate() across providers)
- [ ] `airgap validate` CLI command
- [ ] `internal/api/router.go` — basic chi router setup
- [ ] `internal/ui/templates/layout.html` — base layout with nav sidebar
- [ ] `internal/ui/templates/dashboard.html` — dashboard skeleton
- [ ] `internal/ui/templates/providers.html` — provider list
- [ ] `airgap serve` CLI command (starts web server)
- [ ] Embed static assets via `embed.FS`

### Done When

`airgap sync --provider epel` mirrors an EPEL repo with full dedup. `airgap validate --all` checks all local content. `airgap serve` shows a working (if minimal) dashboard.

---

## Phase 3: Export/Import Engine (Weeks 5–6)

**Goal:** The [[04 - Transfer Workflow]] — the key differentiator of this tool.

### Deliverables

- [ ] `internal/engine/export.go`:
  - Split tar writer with configurable size boundary
  - zstd compression (via `github.com/klauspost/compress/zstd`)
  - Transfer manifest JSON generation
  - Per-archive SHA256 sidecar files
  - TRANSFER-README.txt generation
- [ ] `internal/engine/import.go`:
  - Manifest parsing and archive presence verification
  - Per-archive SHA256 validation
  - Partial re-transfer support (skip already-validated archives)
  - Content extraction with provider directory structure preservation
  - RPM repo metadata rebuild (invoke `createrepo_c`)
- [ ] `airgap export` CLI command
- [ ] `airgap import` CLI command
- [ ] `internal/ui/templates/transfer.html` — export/import wizard
- [ ] Integration test: full round-trip (sync → export → import → validate)

### Done When

You can sync on Machine A, export to a directory, copy that directory, import on Machine B, and all content validates. Corrupt archive detection works.

---

## Phase 4: External Tool Wrappers (Weeks 7–8)

**Goal:** Container image and registry management via oc-mirror and mirror-registry.

### Deliverables

- [ ] `internal/provider/containers/ocmirror.go`:
  - Wraps `oc-mirror` CLI binary
  - Generates/manages ImageSetConfiguration YAML
  - Captures stdout/stderr, parses progress
  - Maps oc-mirror output into Provider interface (Plan/Sync/Validate)
- [ ] `internal/provider/containers/registry.go`:
  - Wraps `mirror-registry` CLI binary
  - install/upgrade/uninstall/status commands
  - Health check (is Quay running? Redis? Postgres?)
- [ ] `internal/engine/scheduler.go`:
  - Cron-based recurring sync jobs
  - SQLite-persisted schedule (survives restarts)
  - Per-provider schedule overrides
- [ ] `internal/ui/templates/jobs.html` — job management page
- [ ] Container image and registry management in web UI

### Done When

`airgap sync --provider container_images` wraps oc-mirror successfully. Scheduled syncs run on cron. Registry can be deployed and health-checked through the tool.

---

## Phase 5: Web UI Polish + Custom Provider (Weeks 9–10)

**Goal:** Complete, polished web UI with all features. Custom file provider for arbitrary sources.

### Deliverables

- [ ] Complete all web UI pages from [[05 - Web UI Design]]:
  - Dashboard with auto-refreshing provider cards
  - Provider detail with file browser, sync history, inline config editing
  - Job management with progress bars
  - Transfer wizard with step-by-step flow
  - Settings page with config editing
- [ ] SSE integration for live log streaming during sync/export/import
- [ ] `internal/provider/custom/files.go`:
  - Generic HTTP/S3 file sync
  - Configurable checksum URL or per-file checksums
  - Supports any URL pattern
- [ ] Log viewer page
- [ ] Dead letter queue UI (view failed files, retry individual or all)
- [ ] Error handling and user-facing error messages throughout UI

### Done When

The web UI is the primary admin interface — an operator can manage everything from the browser. Custom provider can sync arbitrary files.

---

## Phase 6: Hardening (Weeks 11–12)

**Goal:** Production-ready with tests, docs, and performance validation.

### Deliverables

- [ ] Comprehensive test suite:
  - Unit tests for every provider, engine component, config loader
  - Integration tests: full sync → export → import round-trips
  - Mock HTTP server for testing download client without network
- [ ] Containerfile optimization:
  - Multi-stage build (builder + runtime)
  - Include oc-mirror and mirror-registry binaries
  - Rootless container support
- [ ] Documentation:
  - README.md — quick start, installation, basic usage
  - User guide — detailed walkthrough of all features
  - Admin guide — deployment, configuration, troubleshooting
  - Transfer workflow guide — step-by-step with screenshots
- [ ] TRANSFER-README.txt template refinement
- [ ] Performance testing:
  - Large EPEL repo (thousands of RPMs)
  - Large RHCOS images (1GB+ per file)
  - Export/import of 100GB+ datasets
  - Concurrent download tuning
- [ ] Shell completion scripts (bash, zsh, fish)
- [ ] Makefile targets: `build`, `test`, `lint`, `container`, `release`
- [ ] GitHub Actions CI: build, test, lint, container build

### Done When

CI is green. Tests cover critical paths. Container builds and runs. An operator can follow the docs from zero to working disconnected sync.
