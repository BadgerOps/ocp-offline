# Export/Import Engine Design

**Date:** 2026-02-19
**Phase:** 3 (Export/Import Engine)
**Status:** Approved

## Summary

Implement the export/import engine that packages synced content into split tar.zst archives for physical media transfer between internet-connected and air-gapped machines.

## Decisions

- **Compression:** zstd only for v1 (validate CLI flag, reject gzip/none)
- **createrepo_c:** Not auto-run on import. Operator handles separately.
- **Validation cache:** SQLite transfers table (not sidecar files on media)
- **Archive strategy:** Independent per-split archives (not stream-split)

## Export Engine (`internal/engine/export.go`)

### Types

```go
type ExportOptions struct {
    OutputDir   string
    Providers   []string
    SplitSize   int64  // bytes, parsed from "25GB" etc.
    Compression string // "zstd" only for v1
}

type ExportReport struct {
    Archives     []ArchiveInfo
    TotalFiles   int
    TotalSize    int64
    ManifestPath string
    Duration     time.Duration
}

type ArchiveInfo struct {
    Name   string
    Size   int64
    SHA256 string
    Files  []string // relative paths
}
```

### Flow

1. Query `file_records` from store for each requested provider
2. Resolve actual file paths on disk (data_dir + provider + relative path)
3. Build manifest JSON with provider summaries and file inventory
4. Create split tar.zst archives:
   - Track cumulative uncompressed bytes per archive
   - Roll to new archive when SplitSize exceeded
   - Embed manifest in archive 001
5. Compute SHA256 of each completed archive file
6. Write `.sha256` sidecar for each archive
7. Write standalone `airgap-manifest.json` + `.sha256`
8. Write `TRANSFER-README.txt`
9. Record transfer in SQLite transfers table

### Split Archive Writer

Custom writer that wraps tar + zstd:
- Tracks bytes written to current archive
- When adding a file would exceed split size, close current archive and open next
- A single file larger than split size goes into its own archive (archive exceeds limit)
- Archive naming: `airgap-transfer-001.tar.zst`, `airgap-transfer-002.tar.zst`, etc.
- Each archive is independently decompressible

## Import Engine (`internal/engine/import.go`)

### Types

```go
type ImportOptions struct {
    SourceDir  string
    VerifyOnly bool
    Force      bool // skip checksum verification
}

type ImportReport struct {
    ArchivesValidated int
    ArchivesFailed    int
    FilesExtracted    int
    TotalSize         int64
    Duration          time.Duration
    Errors            []string
}
```

### Flow

1. Read `airgap-manifest.json` from source dir
2. Verify all expected archive files are present on disk
3. Validate each archive's SHA256 against manifest:
   - Skip if `--force`
   - Cache validated checksums in transfers table
   - On re-run, skip already-validated archives
4. If `--verify-only`, stop and report validation results
5. Extract each validated archive into data_dir:
   - Preserve provider directory structure
   - Stream decompress (zstd) + untar
6. Upsert extracted files into file_records table
7. Record transfer in transfers table

### Partial Re-transfer

If some archives fail validation:
- Report which archives failed
- Operator re-copies only those files from source
- Re-run import; previously validated archives are skipped

## Manifest Format (`airgap-manifest.json`)

```json
{
  "version": "1.0",
  "created": "2026-02-19T14:30:00Z",
  "source_host": "hostname",
  "providers": {
    "epel": { "file_count": 4521, "total_size": 18739281920 },
    "ocp_binaries": { "file_count": 12, "total_size": 2147483648 }
  },
  "archives": [
    {
      "name": "airgap-transfer-001.tar.zst",
      "size": 0,
      "sha256": "abc123...",
      "files": ["epel/9/Packages/a-foo.rpm", "..."]
    }
  ],
  "total_archives": 1,
  "total_size": 0,
  "file_inventory": [
    { "provider": "epel", "path": "9/Packages/foo.rpm", "size": 3145728, "sha256": "..." }
  ]
}
```

## Size Parsing Helper

`ParseSize("25GB") -> int64` supporting B, KB, MB, GB, TB suffixes (case-insensitive). Lives in `internal/engine/export.go` or a small util.

## Disk Layout (Export Output)

```
/mnt/usb/
├── airgap-manifest.json
├── airgap-manifest.json.sha256
├── airgap-transfer-001.tar.zst
├── airgap-transfer-001.tar.zst.sha256
├── airgap-transfer-002.tar.zst
├── airgap-transfer-002.tar.zst.sha256
└── TRANSFER-README.txt
```

## Not in v1

- Incremental/differential exports
- gzip/none compression
- Automatic createrepo_c on import
- Web UI transfer wizard (Phase 5)
- SSE progress streaming (Phase 5)

## Store Changes

- Add migration v2: `transfer_archives` table to cache per-archive validation results
- New store methods: `CreateTransferArchive`, `GetValidatedArchives`

## New Dependencies

- `github.com/klauspost/compress/zstd` for zstd compression/decompression
