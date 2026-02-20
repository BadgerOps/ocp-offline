# Transfer Workflow

#airgap #transfer #export #import

Back to [[00 - Project Index]] | Related: [[02 - Architecture]], [[06 - CLI Reference]]

## Overview

The transfer workflow is the critical new capability that doesn't exist in any of the [[01 - Existing Repos Audit|current repos]]. It packages synced content for physical media transfer from an internet-connected machine (Machine A) to a disconnected machine (Machine B).

## The Two-Machine Model

```
┌─────────────────────┐         ┌─────────────────────┐
│   Machine A          │  USB/   │   Machine B          │
│   (Internet)         │  disk   │   (Air-gapped)       │
│                      │ ─────► │                      │
│  airgap sync --all   │         │  airgap import       │
│  airgap export       │         │  airgap serve        │
└─────────────────────┘         └─────────────────────┘
```

Both machines run the same `airgap` binary. Machine A uses the sync + export commands. Machine B uses import + serve. The web UI works on both sides.

## Export Process (Machine A)

### CLI

```bash
airgap export --to /mnt/usb                        # export all enabled providers
airgap export --to /mnt/usb --provider epel,rhcos   # export subset
airgap export --to /mnt/usb --split-size 10GB       # override split size
```

### Steps

1. **Snapshot sync state** — query the SQLite `file_inventory` table for all files belonging to the selected providers. This captures what's downloaded, versions, and checksums at the exact moment of export.

2. **Generate transfer manifest** — `airgap-manifest.json`:
   ```json
   {
     "version": "1.0",
     "created": "2026-02-19T14:30:00Z",
     "source_host": "sync-server.example.com",
     "providers": {
       "epel": {
         "repos": ["epel-9", "epel-8"],
         "file_count": 4521,
         "total_size": 18739281920
       },
       "ocp_binaries": {
         "versions": ["latest-4.18"],
         "file_count": 12,
         "total_size": 2147483648
       }
     },
     "archives": [
       {
         "name": "airgap-transfer-001.tar.zst",
         "size": 26843545600,
         "sha256": "abc123...",
         "files": ["epel/9/Packages/a-*.rpm", "..."]
       },
       {
         "name": "airgap-transfer-002.tar.zst",
         "size": 19327352832,
         "sha256": "def456...",
         "files": ["ocp-clients/latest-4.18/*", "..."]
       }
     ],
     "total_archives": 2,
     "total_size": 46170898432,
     "file_inventory": [
       {"path": "epel/9/Packages/ansible-core-2.15.0-1.el9.x86_64.rpm", "size": 3145728, "sha256": "..."},
       "..."
     ]
   }
   ```

3. **Create split tar archives** — custom tar writer that tracks cumulative bytes and rolls to a new archive file when `split_size` is reached:
   - `airgap-transfer-001.tar.zst`
   - `airgap-transfer-002.tar.zst`
   - etc.
   - Compression: zstd by default (3-5x faster than gzip at similar ratios)
   - The manifest JSON is embedded in archive 001 AND written as a standalone file for redundancy

4. **Generate per-archive checksums** — `.sha256` sidecar file for each archive part. Used during import to catch corrupt copies without extracting.

5. **Write TRANSFER-README.txt** — human-readable instructions for the person physically carrying the disk:
   ```
   AIRGAP TRANSFER PACKAGE
   =======================
   Created: 2026-02-19 14:30 UTC
   Source: sync-server.example.com
   Archives: 2 parts
   Total size: 43 GB

   TO IMPORT:
   1. Mount this disk on the disconnected machine
   2. Run: airgap import --from /mnt/usb
   3. The tool will validate all archives before extracting

   IF AN ARCHIVE IS CORRUPT:
   - The import tool will tell you which archive(s) failed
   - Re-copy only the failed archive from the source machine
   - Re-run: airgap import --from /mnt/usb
   ```

### Resulting Disk Layout

```
/mnt/usb/
├── airgap-manifest.json              # standalone manifest (readable)
├── airgap-manifest.json.sha256       # manifest checksum
├── airgap-transfer-001.tar.zst       # split archive part 1
├── airgap-transfer-001.tar.zst.sha256
├── airgap-transfer-002.tar.zst       # split archive part 2
├── airgap-transfer-002.tar.zst.sha256
└── TRANSFER-README.txt               # human-readable guide
```

## Import Process (Machine B)

### CLI

```bash
airgap import --from /mnt/usb                # full import
airgap import --from /mnt/usb --verify-only  # just check integrity
airgap import --from /mnt/usb --force         # skip checksum verification (not recommended)
```

### Steps

1. **Read manifest** — parse `airgap-manifest.json`, verify all expected archive parts are present on the media.

2. **Validate archives** — compute SHA256 of each `.tar.zst` file, compare against manifest. Report per-archive pass/fail:
   ```
   Validating archives...
   [OK]   airgap-transfer-001.tar.zst (25.0 GB) ✓
   [FAIL] airgap-transfer-002.tar.zst — expected sha256 def456..., got 789abc...

   1 of 2 archives failed validation.
   Re-copy the failed archive(s) from source and re-run import.
   ```

3. **Partial re-transfer support** — if only some archives are corrupt, the admin re-copies just those files from the source disk. On re-run, already-validated archives are skipped (checksums cached in local state).

4. **Extract content** — decompress and untar each archive into `data_dir`, preserving the provider directory structure.

5. **Rebuild metadata**:
   - RPM repos: run `createrepo` (or `createrepo_c`) to rebuild `repodata/`
   - Container images: load into local registry via `oc-mirror` or `skopeo`
   - Update the local SQLite `file_inventory` with the imported files

6. **Update state DB** — record the import in `transfers` table (timestamp, archive count, manifest hash, provider list).

## Incremental Transfers

Future enhancement (post-v1): support differential exports that only include files changed since the last export. The manifest would include a `based_on` field referencing the previous manifest hash, and the import would merge rather than replace.

For v1, each export is a full snapshot of the selected providers. This is simpler and more robust — if any previous import was incomplete, a full re-export fixes it.

## Web UI Integration

See [[05 - Web UI Design#Transfer Page]] for the export/import wizard UI. Key features:

- Provider selection checkboxes
- Split size slider/input
- Destination path browser
- Real-time progress bar during archive creation
- Per-archive validation status during import
- Transfer history log

## Split Archive Implementation Notes

The custom tar writer (in `internal/engine/export.go`) works like this:

```go
// Pseudocode
archiveNum := 1
currentSize := 0
writer := newArchiveWriter(archiveNum)

for _, file := range filesToExport {
    if currentSize + file.Size > splitSize {
        writer.Close()
        archiveNum++
        currentSize = 0
        writer = newArchiveWriter(archiveNum)
    }
    writer.AddFile(file)
    currentSize += file.Size
}
writer.Close()
```

Each archive is independently decompressible — you don't need all parts to extract any single part. The manifest tells you which files are in which archive.

This is different from `split` on a single tar (where you need all parts to reconstruct). Our approach means a corrupt archive only affects the files in that archive, not the entire transfer.
