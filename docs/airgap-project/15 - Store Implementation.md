# Store Implementation

> Subagent output from SQLite store implementation. Preserved for posterity.

## Source Files
- `STORE_IMPLEMENTATION.md`
- `CODE_SNIPPETS.md` (store-related sections)

---

## Overview

**Database:** SQLite via `modernc.org/sqlite` (pure Go, no CGO)
**Location:** `internal/store/`
**Files:** `sqlite.go`, `models.go`, `migrations.go`

## Database Models

### SyncRun
Tracks each sync execution:
- `ID`, `ProviderName`, `StartedAt`, `CompletedAt`
- `Status` (running/completed/failed)
- Counts: `FilesDownloaded`, `FilesSkipped`, `FilesDeleted`, `FilesFailed`
- `BytesDownloaded`, `ErrorMessage`

### FileRecord
Inventory of synced files:
- `ID`, `ProviderName`, `RelativePath`, `URL`
- `Checksum`, `ChecksumType`
- `SizeBytes`, `LastSyncRunID`
- `CreatedAt`, `UpdatedAt`

### FailedFileRecord
Dead letter queue for failures:
- `ID`, `SyncRunID`, `ProviderName`
- `RelativePath`, `URL`, `ErrorMessage`
- `RetryCount`, `LastRetryAt`, `CreatedAt`

### Job (scheduling)
- `ID`, `Name`, `ProviderName`, `Schedule`
- `Enabled`, `LastRunAt`, `NextRunAt`

### Transfer (export/import)
- `ID`, `Type`, `ProviderName`
- `ArchivePath`, `Status`
- `TotalFiles`, `TotalBytes`
- `StartedAt`, `CompletedAt`

## Store API

```go
// Lifecycle
func New(dbPath string, logger *slog.Logger) (*Store, error)
func (s *Store) Close() error

// Sync runs
func (s *Store) CreateSyncRun(run *SyncRun) error
func (s *Store) UpdateSyncRun(run *SyncRun) error
func (s *Store) GetLastSyncRun(provider string) (*SyncRun, error)

// File records
func (s *Store) UpsertFileRecord(fileRec *FileRecord) error
func (s *Store) GetFileRecord(provider, path string) (*FileRecord, error)
func (s *Store) ListFileRecords(provider string) ([]*FileRecord, error)
func (s *Store) DeleteFileRecord(provider, path string) error

// Failed files
func (s *Store) AddFailedFile(failedRec *FailedFileRecord) error
func (s *Store) ListFailedFiles(provider string) ([]*FailedFileRecord, error)

// Statistics
func (s *Store) GetProviderStats(provider string) (*ProviderStats, error)
```

## Migrations

Schema versioning via `schema_version` table. Migrations run automatically on store initialization.

## Key Design Points

- Pure Go SQLite (no CGO required) — simplifies cross-compilation
- In-memory database support for testing (`":memory:"`)
- Automatic schema migrations on startup
- Upsert pattern for file records (insert or update on conflict)
- Dead letter queue for retry analysis

---

*This note consolidates: STORE_IMPLEMENTATION.md, store-related CODE_SNIPPETS.md*
