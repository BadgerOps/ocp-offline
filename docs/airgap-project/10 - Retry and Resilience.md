# Retry and Resilience

#airgap #reliability #downloads

Back to [[00 - Project Index]] | Related: [[02 - Architecture]], [[09 - Technical Decisions]]

## Overview

Download reliability is critical — EPEL repos have thousands of packages, RHCOS images are 1GB+, and the tool runs unattended on a schedule. Building on patterns already proven in [[01 - Existing Repos Audit#ocpsync|ocpsync]] (exponential backoff) and [[01 - Existing Repos Audit#epel-offline-sync|epel-offline-sync]] (concurrent downloads), the unified download client in `internal/download/` provides a robust foundation for all providers.

## Download Client (`internal/download/client.go`)

### Exponential Backoff with Jitter

Carried forward from ocpsync's retry logic, enhanced with jitter to prevent thundering herd:

```go
func backoff(attempt int) time.Duration {
    base := time.Second * time.Duration(math.Pow(2, float64(attempt)))
    jitter := time.Duration(rand.Int63n(int64(base / 2)))
    return base + jitter
}
```

Retry schedule (approximate): 1s → 2-3s → 4-6s (with jitter). Default 3 attempts per file, configurable per provider via `retry_attempts` in [[03 - Configuration|config]].

### Resumable Downloads (HTTP Range)

For large files (RHCOS ISOs, QCOW2 images), if a download is interrupted mid-stream:

1. Check if a partial file exists locally
2. Send `Range: bytes={partial_size}-` header
3. If server responds `206 Partial Content`, resume from where we left off
4. If server responds `200 OK` (doesn't support Range), re-download from scratch
5. Validate final SHA256 against expected checksum

This saves significant time and bandwidth for 1GB+ files on flaky connections.

### Streaming Checksum Validation

Instead of downloading the entire file and then computing its hash, we compute the SHA256 incrementally during the download using an `io.TeeReader`:

```go
hash := sha256.New()
reader := io.TeeReader(resp.Body, hash)
io.Copy(file, reader)
computed := hex.EncodeToString(hash.Sum(nil))
```

If the checksum fails, the file is removed immediately — no disk space wasted on corrupt downloads.

### HTTP Client Configuration

```go
client := &http.Client{
    Timeout: 30 * time.Minute,  // generous for large files
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 10,
        IdleConnTimeout:     90 * time.Second,
        TLSHandshakeTimeout: 10 * time.Second,
    },
}
```

Timeout is per-request, not per-byte. For 1GB files on slow connections, 30 minutes is reasonable. The download client also monitors throughput and logs warnings if speed drops below a configurable threshold.

## Worker Pool (`internal/download/pool.go`)

Concurrent download orchestration using Go channels:

```go
type Pool struct {
    workers    int
    jobs       chan DownloadJob
    results    chan DownloadResult
    wg         sync.WaitGroup
}
```

- Workers pull jobs from a channel, download with retry, push results
- Configurable per provider: `max_concurrent_downloads` (default 8 for EPEL, lower for large files)
- Progress reporting via callback function (used by both CLI progress bars and SSE streaming)
- Graceful shutdown on context cancellation

### Concurrency Guidelines

| Provider | Recommended Workers | Rationale |
|----------|-------------------|-----------|
| EPEL | 8-16 | Many small files (RPMs are typically <50MB) |
| OCP binaries | 2-4 | Fewer, larger files |
| RHCOS | 1-2 | Very large files (1GB+), server may rate-limit |
| Custom files | 4-8 | Depends on source server capacity |

## Dead Letter Queue

Files that fail after all retries go to the dead letter queue, stored in the `failed_files` SQLite table:

```sql
CREATE TABLE failed_files (
    id INTEGER PRIMARY KEY,
    provider TEXT NOT NULL,
    file_path TEXT NOT NULL,
    url TEXT NOT NULL,
    expected_checksum TEXT,
    error TEXT NOT NULL,
    retry_count INTEGER DEFAULT 0,
    first_failure DATETIME NOT NULL,
    last_failure DATETIME NOT NULL,
    resolved BOOLEAN DEFAULT FALSE
);
```

### Dead Letter Queue Operations

**CLI:**
```bash
airgap status --failed              # list all failed files
airgap sync --retry-failed          # retry all failed files
airgap sync --retry-failed --provider epel  # retry failed for one provider
```

**Web UI:**
The [[05 - Web UI Design#Dashboard|dashboard]] shows a warning badge when the dead letter queue is non-empty. The provider detail page lists failed files with per-file retry buttons.

### Automatic Resolution

When a subsequent sync run successfully downloads a file that was previously in the dead letter queue, the queue entry is automatically marked `resolved = TRUE`. This handles transient server issues without admin intervention.

## Sync Recovery

### Interrupted Sync

If the process is killed mid-sync (power failure, OOM, etc.):

1. Partially downloaded files are left on disk (incomplete)
2. On next sync, `Plan()` checks each file's checksum against the manifest
3. Incomplete files fail checksum → scheduled for re-download
4. Complete files pass checksum → skipped
5. Net effect: the sync resumes where it left off

No special recovery logic needed — the checksum-based diff naturally handles it.

### Corrupted State DB

If `airgap.db` is corrupted:

1. Delete the DB file
2. Run `airgap sync --all` — the sync rebuilds the file inventory by scanning the data directory and re-downloading any missing files
3. The tool is designed so that the filesystem is the source of truth, not the database. The DB is an acceleration layer (avoid re-scanning manifests), not the authoritative state.

## Transfer Resilience

See [[04 - Transfer Workflow]] for details on per-archive checksums and partial re-transfer. The key resilience properties:

- Each archive is independently valid (no cross-archive dependencies)
- Per-archive SHA256 catches corrupt copies before extraction
- Only corrupt archives need re-transfer — not the entire dataset
- The manifest is duplicated (standalone file + inside archive 001) for redundancy

## Monitoring and Alerting (Future)

Post-v1 considerations:

- Prometheus metrics endpoint (`/metrics`) for sync duration, file counts, failure rates
- Webhook notifications on sync failure (email, Slack, PagerDuty)
- Health check endpoint for monitoring systems
