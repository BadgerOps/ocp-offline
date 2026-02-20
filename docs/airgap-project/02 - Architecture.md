# Architecture

#airgap #architecture #design

Back to [[00 - Project Index]] | Related: [[09 - Technical Decisions]], [[03 - Configuration]]

## System Overview

`airgap` is a single Go binary with four interaction surfaces that share a common core engine:

```
┌──────────────────────────────────────────────────────────┐
│                    airgap binary (Go)                     │
├──────────┬──────────┬───────────┬────────────────────────┤
│  Web UI  │ REST API │  CLI      │  Background Scheduler  │
│ (htmx +  │ (JSON)   │ (cobra)   │  (cron-style)          │
│ alpine)  │          │           │                        │
├──────────┴──────────┴───────────┴────────────────────────┤
│                     Core Engine                           │
│  ┌────────────┐ ┌──────────┐ ┌─────────┐ ┌────────────┐ │
│  │ SyncManager│ │ Validate │ │ Export  │ │  Import    │ │
│  │            │ │ Engine   │ │ Engine  │ │  Engine    │ │
│  └─────┬──────┘ └──────────┘ └─────────┘ └────────────┘ │
│        │                                                  │
│  ┌─────┴──────────────────────────────────────────┐      │
│  │              Content Providers                   │      │
│  │  ┌───────┐ ┌────────┐ ┌──────────┐ ┌────────┐ │      │
│  │  │ RPM   │ │  OCP   │ │Container │ │ Custom │ │      │
│  │  │(EPEL) │ │Binaries│ │ Images   │ │ Files  │ │      │
│  │  └───────┘ └────────┘ └──────────┘ └────────┘ │      │
│  └────────────────────────────────────────────────┘      │
│                                                           │
│  ┌────────────────────────────────────────────────┐      │
│  │           External Tool Wrappers                │      │
│  │  ┌───────────┐  ┌────────────────┐             │      │
│  │  │ oc-mirror │  │mirror-registry │             │      │
│  │  └───────────┘  └────────────────┘             │      │
│  └────────────────────────────────────────────────┘      │
│                                                           │
│  ┌────────────────────────────────────────────────┐      │
│  │              Storage Layer                      │      │
│  │  SQLite (job state, history, config)            │      │
│  │  Filesystem (downloaded content, manifests)     │      │
│  └────────────────────────────────────────────────┘      │
└──────────────────────────────────────────────────────────┘
```

## Provider Plugin Model

The central abstraction. Each content type implements this interface:

```go
type Provider interface {
    // Name returns the provider identifier (e.g., "epel", "ocp-binaries")
    Name() string

    // Configure loads provider-specific settings from the unified config
    Configure(cfg ProviderConfig) error

    // Plan compares upstream manifest against local state, returns
    // a list of actions (download, delete, skip) without executing them
    Plan(ctx context.Context) (*SyncPlan, error)

    // Sync executes the plan — downloads, validates, retries
    Sync(ctx context.Context, plan *SyncPlan, opts SyncOptions) (*SyncReport, error)

    // Validate checks integrity of all local content against manifests
    Validate(ctx context.Context) (*ValidationReport, error)
}
```

### SyncPlan

The `Plan()` method returns a `SyncPlan` — a list of file-level actions computed by diffing the upstream manifest against local state. This is a dry-run by default, giving the admin visibility into what will change before anything downloads.

```go
type SyncPlan struct {
    Provider   string
    Actions    []SyncAction
    TotalSize  int64        // bytes to download
    TotalFiles int
    Timestamp  time.Time
}

type SyncAction struct {
    Path     string      // relative path within provider output dir
    Action   ActionType  // Download, Delete, Skip, Update
    Size     int64
    Checksum string      // expected SHA256
    Reason   string      // "new file", "checksum mismatch", "removed upstream"
}
```

### SyncReport

Returned after `Sync()` executes a plan:

```go
type SyncReport struct {
    Provider      string
    StartTime     time.Time
    EndTime       time.Time
    Downloaded    int
    Deleted       int
    Skipped       int
    Failed        []FailedFile  // goes to dead letter queue
    BytesTransferred int64
}
```

## Built-in Providers

| Provider | Package | Replaces | Notes |
|----------|---------|----------|-------|
| `epel` | `internal/provider/epel/` | [[01 - Existing Repos Audit#epel-offline-sync|epel-offline-sync]] | repomd.xml/primary.xml parsing, RPM dedup |
| `ocp-binaries` | `internal/provider/ocp/` | [[01 - Existing Repos Audit#ocpsync|ocpsync]] | OCP clients from mirror.openshift.com |
| `rhcos` | `internal/provider/ocp/` | [[01 - Existing Repos Audit#ocpsync|ocpsync]] | RHCOS images, same package |
| `container-images` | `internal/provider/containers/` | [[01 - Existing Repos Audit#oc-mirror|oc-mirror]] | Wraps `oc-mirror` CLI |
| `registry` | `internal/provider/containers/` | [[01 - Existing Repos Audit#mirror-registry|mirror-registry]] | Wraps `mirror-registry` CLI |
| `custom-files` | `internal/provider/custom/` | New | Generic HTTP/S3 file sync |

## Core Engine Components

### SyncManager (`internal/engine/sync.go`)

Orchestrates provider execution. Loads enabled providers from config, runs `Plan()` then `Sync()`, records results to the SQLite store. Supports running all providers or a filtered subset.

### Export Engine (`internal/engine/export.go`)

See [[04 - Transfer Workflow]] for full detail. Creates split tar.zst archives with a transfer manifest.

### Import Engine (`internal/engine/import.go`)

See [[04 - Transfer Workflow]]. Validates archive integrity, extracts, rebuilds repo metadata.

### Validate Engine (`internal/engine/validate.go`)

Runs `Validate()` across all providers — checks every local file against its expected checksum. Reports discrepancies without fixing them (the admin decides whether to re-sync or accept).

### Scheduler (`internal/engine/scheduler.go`)

Cron-style job scheduler. Runs sync jobs on configured intervals. Uses `robfig/cron` or similar Go cron library. Jobs are persisted in SQLite so they survive restarts.

## Storage Layer

### SQLite (`internal/store/`)

Pure-Go SQLite via `modernc.org/sqlite` — no CGO, no external deps. Single file at `db_path` from config.

**Tables:**

- `sync_runs` — history of every sync execution (provider, start/end time, files downloaded/failed, bytes)
- `file_inventory` — current state of all downloaded files (path, size, checksum, provider, last_verified)
- `jobs` — scheduled and completed jobs (type, cron expression, last run, next run, status)
- `transfers` — export/import history (direction, timestamp, archive count, manifest hash)
- `failed_files` — dead letter queue (file path, provider, error, retry count, last attempt)
- `migrations` — schema version tracking

### Filesystem

Downloaded content lives under `data_dir` organized by provider:

```
/var/lib/airgap/
├── epel/
│   ├── 9/
│   │   ├── repodata/
│   │   └── Packages/
│   └── 8/
├── ocp-clients/
│   ├── latest-4.17/
│   ├── latest-4.18/
│   └── latest-4.19/
├── rhcos/
│   ├── 4.17/
│   ├── 4.18/
│   └── 4.19/
├── container-images/
│   └── (oc-mirror output)
└── airgap.db
```

## Download Client (`internal/download/`)

Shared HTTP download infrastructure used by all providers. See [[10 - Retry and Resilience]] for detail.

Key capabilities: concurrent worker pool (goroutine-based), exponential backoff with jitter, HTTP Range resume for large files, streaming SHA256 validation during download, configurable per-provider concurrency.

## Project Directory Structure

```
airgap/
├── cmd/
│   └── airgap/
│       └── main.go                  # cobra root command setup
├── internal/
│   ├── api/
│   │   ├── router.go                # chi router, mounts all routes
│   │   ├── handlers_dashboard.go
│   │   ├── handlers_providers.go
│   │   ├── handlers_jobs.go
│   │   ├── handlers_transfer.go
│   │   ├── handlers_settings.go
│   │   └── middleware.go            # logging, recovery
│   ├── config/
│   │   ├── config.go                # unified config struct + YAML loader
│   │   └── config_test.go
│   ├── engine/
│   │   ├── sync.go                  # SyncManager orchestrates providers
│   │   ├── export.go                # tar split + manifest generation
│   │   ├── import.go                # validate + extract + rebuild
│   │   ├── validate.go              # integrity checking
│   │   └── scheduler.go             # cron-based job scheduling
│   ├── provider/
│   │   ├── provider.go              # Provider interface definition
│   │   ├── epel/
│   │   │   ├── epel.go
│   │   │   └── epel_test.go
│   │   ├── ocp/
│   │   │   ├── binaries.go
│   │   │   ├── rhcos.go
│   │   │   └── ocp_test.go
│   │   ├── containers/
│   │   │   ├── ocmirror.go
│   │   │   └── registry.go
│   │   └── custom/
│   │       └── files.go
│   ├── store/
│   │   ├── sqlite.go
│   │   ├── models.go
│   │   └── migrations.go
│   ├── download/
│   │   ├── client.go
│   │   ├── pool.go
│   │   └── client_test.go
│   └── ui/
│       ├── templates/
│       │   ├── layout.html
│       │   ├── dashboard.html
│       │   ├── providers.html
│       │   ├── provider_detail.html
│       │   ├── jobs.html
│       │   ├── transfer.html
│       │   └── settings.html
│       └── static/
│           ├── htmx.min.js
│           ├── alpine.min.js
│           └── styles.css
├── configs/
│   └── airgap.example.yaml
├── Containerfile
├── Makefile
├── go.mod
├── go.sum
└── README.md
```
