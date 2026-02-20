# Export/Import Engine Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement the export/import engine that packages synced content into split tar.zst archives for air-gapped transfer.

**Architecture:** Two new files in `internal/engine/` (export.go, import.go) plus a manifest package. The SyncManager gets Export() and Import() methods. The existing store Transfer model and CRUD methods are reused. A new migration adds a `transfer_archives` table for caching per-archive validation state.

**Tech Stack:** Go stdlib `archive/tar`, `github.com/klauspost/compress/zstd`, existing SQLite store, existing `store.Transfer` model.

**Design Doc:** `docs/plans/2026-02-19-export-import-design.md`

---

### Task 1: Add zstd dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add the zstd module**

Run: `cd /Users/badger/code/ocp-offline && go get github.com/klauspost/compress/zstd`

**Step 2: Verify it was added**

Run: `grep klauspost /Users/badger/code/ocp-offline/go.mod`
Expected: Line containing `github.com/klauspost/compress`

---

### Task 2: ParseSize helper + tests

**Files:**
- Create: `internal/engine/size.go`
- Create: `internal/engine/size_test.go`

**Step 1: Write the failing tests**

Create `internal/engine/size_test.go`:

```go
package engine

import (
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"100B", 100, false},
		{"1KB", 1024, false},
		{"1MB", 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		{"25GB", 25 * 1024 * 1024 * 1024, false},
		{"1TB", 1024 * 1024 * 1024 * 1024, false},
		{"500mb", 500 * 1024 * 1024, false},
		{"10gb", 10 * 1024 * 1024 * 1024, false},
		{"1024", 1024, false},
		{"", 0, true},
		{"GB", 0, true},
		{"-1GB", 0, true},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSize(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseSize(%q) expected error, got %d", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSize(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestParseSize -v`
Expected: FAIL — `ParseSize` undefined

**Step 3: Write the implementation**

Create `internal/engine/size.go`:

```go
package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSize parses a human-readable size string like "25GB" into bytes.
// Supports B, KB, MB, GB, TB suffixes (case-insensitive).
// A plain number is treated as bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	s = strings.ToUpper(s)

	multipliers := []struct {
		suffix string
		mult   int64
	}{
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}

	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			numStr := strings.TrimSuffix(s, m.suffix)
			if numStr == "" {
				return 0, fmt.Errorf("missing number in size: %s", s)
			}
			n, err := strconv.ParseInt(numStr, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number in size %q: %w", s, err)
			}
			if n < 0 {
				return 0, fmt.Errorf("negative size: %s", s)
			}
			return n * m.mult, nil
		}
	}

	// Plain number = bytes
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative size: %s", s)
	}
	return n, nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestParseSize -v`
Expected: PASS

---

### Task 3: Manifest types + JSON serialization

**Files:**
- Create: `internal/engine/manifest.go`
- Create: `internal/engine/manifest_test.go`

**Step 1: Write the failing tests**

Create `internal/engine/manifest_test.go`:

```go
package engine

import (
	"encoding/json"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	m := &TransferManifest{
		Version:    "1.0",
		Created:    time.Date(2026, 2, 19, 14, 30, 0, 0, time.UTC),
		SourceHost: "sync-server.example.com",
		Providers: map[string]ManifestProvider{
			"epel": {FileCount: 100, TotalSize: 1024000},
		},
		Archives: []ManifestArchive{
			{
				Name:   "airgap-transfer-001.tar.zst",
				Size:   512000,
				SHA256: "abc123",
				Files:  []string{"epel/9/foo.rpm"},
			},
		},
		TotalArchives: 1,
		TotalSize:     512000,
		FileInventory: []ManifestFile{
			{Provider: "epel", Path: "9/foo.rpm", Size: 1024, SHA256: "def456"},
		},
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded TransferManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Version != "1.0" {
		t.Errorf("version = %q, want %q", decoded.Version, "1.0")
	}
	if decoded.SourceHost != "sync-server.example.com" {
		t.Errorf("source_host = %q, want %q", decoded.SourceHost, "sync-server.example.com")
	}
	if len(decoded.Providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(decoded.Providers))
	}
	if decoded.Providers["epel"].FileCount != 100 {
		t.Errorf("epel file_count = %d, want 100", decoded.Providers["epel"].FileCount)
	}
	if len(decoded.Archives) != 1 {
		t.Fatalf("archives count = %d, want 1", len(decoded.Archives))
	}
	if decoded.Archives[0].SHA256 != "abc123" {
		t.Errorf("archive sha256 = %q, want %q", decoded.Archives[0].SHA256, "abc123")
	}
	if len(decoded.FileInventory) != 1 {
		t.Fatalf("file_inventory count = %d, want 1", len(decoded.FileInventory))
	}
	if decoded.FileInventory[0].Provider != "epel" {
		t.Errorf("file provider = %q, want %q", decoded.FileInventory[0].Provider, "epel")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestManifestRoundTrip -v`
Expected: FAIL — types undefined

**Step 3: Write the implementation**

Create `internal/engine/manifest.go`:

```go
package engine

import "time"

// TransferManifest describes a complete export for transfer to an air-gapped environment.
type TransferManifest struct {
	Version       string                      `json:"version"`
	Created       time.Time                   `json:"created"`
	SourceHost    string                      `json:"source_host"`
	Providers     map[string]ManifestProvider `json:"providers"`
	Archives      []ManifestArchive           `json:"archives"`
	TotalArchives int                         `json:"total_archives"`
	TotalSize     int64                       `json:"total_size"`
	FileInventory []ManifestFile              `json:"file_inventory"`
}

// ManifestProvider summarizes one provider's contribution to the export.
type ManifestProvider struct {
	FileCount int   `json:"file_count"`
	TotalSize int64 `json:"total_size"`
}

// ManifestArchive describes a single split archive in the export.
type ManifestArchive struct {
	Name   string   `json:"name"`
	Size   int64    `json:"size"`
	SHA256 string   `json:"sha256"`
	Files  []string `json:"files"`
}

// ManifestFile is one entry in the full file inventory.
type ManifestFile struct {
	Provider string `json:"provider"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestManifestRoundTrip -v`
Expected: PASS

---

### Task 4: Store migration for transfer_archives

**Files:**
- Modify: `internal/store/models.go` — add `TransferArchive` model
- Modify: `internal/store/migrations.go` — add migration v2
- Modify: `internal/store/sqlite.go` — add CRUD for transfer_archives
- Modify: `internal/store/sqlite_test.go` — add tests

**Step 1: Write the failing test**

Add to `internal/store/sqlite_test.go`:

```go
func TestTransferArchiveCRUD(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	// Create a parent transfer first
	transfer := &Transfer{
		Direction: "export",
		Path:      "/mnt/usb",
		Providers: "epel,rhcos",
		Status:    "running",
		StartTime: time.Now(),
	}
	if err := s.CreateTransfer(transfer); err != nil {
		t.Fatalf("create transfer: %v", err)
	}

	// Create a transfer archive
	archive := &TransferArchive{
		TransferID:    transfer.ID,
		ArchiveName:   "airgap-transfer-001.tar.zst",
		SHA256:        "abc123def456",
		Size:          1024000,
		Validated:     false,
		ValidatedAt:   time.Time{},
	}
	if err := s.CreateTransferArchive(archive); err != nil {
		t.Fatalf("create transfer archive: %v", err)
	}
	if archive.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Mark as validated
	if err := s.MarkArchiveValidated(archive.ID); err != nil {
		t.Fatalf("mark validated: %v", err)
	}

	// List archives for transfer
	archives, err := s.ListTransferArchives(transfer.ID)
	if err != nil {
		t.Fatalf("list archives: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("expected 1 archive, got %d", len(archives))
	}
	if !archives[0].Validated {
		t.Error("expected archive to be validated")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/store/ -run TestTransferArchiveCRUD -v`
Expected: FAIL — `TransferArchive` undefined

**Step 3: Add the model to models.go**

Add to `internal/store/models.go`:

```go
// TransferArchive tracks per-archive validation state during import
type TransferArchive struct {
	ID          int64
	TransferID  int64
	ArchiveName string
	SHA256      string
	Size        int64
	Validated   bool
	ValidatedAt time.Time
}
```

**Step 4: Add migration v2 to migrations.go**

Append a new entry to the `migrations` slice in `internal/store/migrations.go`:

```go
{
	version: 2,
	sql: `
		CREATE TABLE transfer_archives (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			transfer_id INTEGER NOT NULL,
			archive_name TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size INTEGER DEFAULT 0,
			validated BOOLEAN DEFAULT 0,
			validated_at DATETIME,
			FOREIGN KEY(transfer_id) REFERENCES transfers(id)
		);
	`,
},
```

**Step 5: Add store methods to sqlite.go**

Add to `internal/store/sqlite.go` after the Transfer section:

```go
// ============================================================================
// TransferArchive Operations
// ============================================================================

// CreateTransferArchive inserts a new TransferArchive and sets its ID
func (s *Store) CreateTransferArchive(a *TransferArchive) error {
	const query = `
		INSERT INTO transfer_archives (
			transfer_id, archive_name, sha256, size, validated, validated_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(
		query,
		a.TransferID, a.ArchiveName, a.SHA256, a.Size,
		a.Validated, a.ValidatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert transfer archive: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	a.ID = id
	return nil
}

// MarkArchiveValidated marks a TransferArchive as validated
func (s *Store) MarkArchiveValidated(id int64) error {
	const query = `
		UPDATE transfer_archives
		SET validated = 1, validated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`

	result, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to mark archive validated: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("transfer archive not found: %d", id)
	}

	return nil
}

// ListTransferArchives retrieves all archives for a transfer
func (s *Store) ListTransferArchives(transferID int64) ([]TransferArchive, error) {
	const query = `
		SELECT id, transfer_id, archive_name, sha256, size, validated, validated_at
		FROM transfer_archives WHERE transfer_id = ? ORDER BY archive_name
	`

	rows, err := s.db.Query(query, transferID)
	if err != nil {
		return nil, fmt.Errorf("failed to query transfer archives: %w", err)
	}
	defer rows.Close()

	var archives []TransferArchive
	for rows.Next() {
		a := TransferArchive{}
		err := rows.Scan(
			&a.ID, &a.TransferID, &a.ArchiveName, &a.SHA256,
			&a.Size, &a.Validated, &a.ValidatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan transfer archive: %w", err)
		}
		archives = append(archives, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transfer archives: %w", err)
	}

	return archives, nil
}
```

**Step 6: Run test to verify it passes**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/store/ -run TestTransferArchiveCRUD -v`
Expected: PASS

**Step 7: Run all store tests to check for regressions**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/store/ -v`
Expected: All PASS

---

### Task 5: Export engine implementation + tests

**Files:**
- Create: `internal/engine/export.go`
- Create: `internal/engine/export_test.go`

This is the largest task. The export engine creates split tar.zst archives from synced files.

**Step 1: Write the failing test**

Create `internal/engine/export_test.go`:

```go
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
	"github.com/klauspost/compress/zstd"
)

// setupExportTest creates a temp data dir with fake synced files and a store with matching records.
func setupExportTest(t *testing.T) (*SyncManager, string, string) {
	t.Helper()

	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create fake synced files under dataDir
	files := map[string]string{
		"epel/9/Packages/foo.rpm":   "fake-rpm-content-foo",
		"epel/9/Packages/bar.rpm":   "fake-rpm-content-bar",
		"ocp_binaries/4.18/oc":      "fake-oc-binary",
	}

	for relPath, content := range files {
		absPath := filepath.Join(dataDir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Set up store with file records
	dbPath := filepath.Join(t.TempDir(), "test.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(dbPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	for relPath, content := range files {
		parts := strings.SplitN(relPath, "/", 2)
		providerName := parts[0]
		providerPath := parts[1]
		h := sha256.Sum256([]byte(content))
		rec := &store.FileRecord{
			Provider:     providerName,
			Path:         providerPath,
			Size:         int64(len(content)),
			SHA256:       hex.EncodeToString(h[:]),
			LastModified: time.Now(),
			LastVerified: time.Now(),
		}
		if err := st.UpsertFileRecord(rec); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{
		Server: config.ServerConfig{DataDir: dataDir},
		Export: config.ExportConfig{
			SplitSize:   "1GB",
			Compression: "zstd",
		},
	}

	registry := provider.NewRegistry()
	client := download.NewClient(logger)
	mgr := NewSyncManager(registry, st, client, cfg, logger)

	return mgr, dataDir, outputDir
}

func TestExportCreatesArchivesAndManifest(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	report, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   1024 * 1024 * 1024, // 1GB — all files fit in one archive
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Should have 1 archive (files are tiny)
	if len(report.Archives) != 1 {
		t.Fatalf("expected 1 archive, got %d", len(report.Archives))
	}
	if report.TotalFiles != 3 {
		t.Errorf("expected 3 total files, got %d", report.TotalFiles)
	}

	// Verify archive file exists
	archivePath := filepath.Join(outputDir, report.Archives[0].Name)
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Fatalf("archive file not found: %s", archivePath)
	}

	// Verify .sha256 sidecar exists
	sha256Path := archivePath + ".sha256"
	if _, err := os.Stat(sha256Path); os.IsNotExist(err) {
		t.Fatalf("sha256 sidecar not found: %s", sha256Path)
	}

	// Verify manifest exists and is valid JSON
	manifestPath := filepath.Join(outputDir, "airgap-manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest TransferManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.Version != "1.0" {
		t.Errorf("manifest version = %q, want %q", manifest.Version, "1.0")
	}
	if manifest.TotalArchives != 1 {
		t.Errorf("manifest total_archives = %d, want 1", manifest.TotalArchives)
	}
	if len(manifest.FileInventory) != 3 {
		t.Errorf("manifest file_inventory count = %d, want 3", len(manifest.FileInventory))
	}

	// Verify TRANSFER-README.txt exists
	readmePath := filepath.Join(outputDir, "TRANSFER-README.txt")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		t.Fatal("TRANSFER-README.txt not found")
	}

	// Verify manifest.json.sha256 exists
	manifestSha := filepath.Join(outputDir, "airgap-manifest.json.sha256")
	if _, err := os.Stat(manifestSha); os.IsNotExist(err) {
		t.Fatal("manifest sha256 sidecar not found")
	}
}

func TestExportSplitsArchives(t *testing.T) {
	mgr, dataDir, outputDir := setupExportTest(t)

	// Write a larger file so splitting triggers
	bigContent := strings.Repeat("x", 1000)
	bigPath := filepath.Join(dataDir, "epel/9/Packages/big.rpm")
	if err := os.WriteFile(bigPath, []byte(bigContent), 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(bigContent))
	// Access store via the manager's store field — we'll need to add a helper or
	// just set the split size very small so existing files trigger splits.

	// Use a very small split size to force multiple archives
	report, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   50, // 50 bytes — will force splits
		Compression: "zstd",
	})
	_ = h // unused but shows the pattern
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if len(report.Archives) < 2 {
		t.Errorf("expected multiple archives with 50-byte split, got %d", len(report.Archives))
	}

	// Each archive should exist on disk
	for _, arch := range report.Archives {
		archPath := filepath.Join(outputDir, arch.Name)
		if _, err := os.Stat(archPath); os.IsNotExist(err) {
			t.Errorf("archive not found: %s", archPath)
		}
	}
}

func TestExportRejectsNonZstdCompression(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "gzip",
	})
	if err == nil {
		t.Fatal("expected error for gzip compression, got nil")
	}
	if !strings.Contains(err.Error(), "zstd") {
		t.Errorf("error should mention zstd, got: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestExport -v`
Expected: FAIL — `ExportOptions` and `Export` method undefined

**Step 3: Write export.go implementation**

Create `internal/engine/export.go`:

```go
package engine

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ExportOptions configures an export operation.
type ExportOptions struct {
	OutputDir   string
	Providers   []string
	SplitSize   int64
	Compression string
}

// ExportReport summarizes a completed export.
type ExportReport struct {
	Archives     []ArchiveInfo
	TotalFiles   int
	TotalSize    int64
	ManifestPath string
	Duration     time.Duration
}

// ArchiveInfo describes one split archive.
type ArchiveInfo struct {
	Name   string
	Size   int64
	SHA256 string
	Files  []string
}

// Export creates split tar.zst archives of synced content for air-gapped transfer.
func (m *SyncManager) Export(ctx context.Context, opts ExportOptions) (*ExportReport, error) {
	startTime := time.Now()

	if opts.Compression != "zstd" {
		return nil, fmt.Errorf("unsupported compression %q: only zstd is supported in v1", opts.Compression)
	}

	if opts.SplitSize <= 0 {
		return nil, fmt.Errorf("split size must be positive")
	}

	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output directory: %w", err)
	}

	// Collect files from store for requested providers
	type fileEntry struct {
		provider string
		relPath  string // relative to provider dir (from store)
		absPath  string // absolute on disk
		size     int64
		sha256   string
	}

	var allFiles []fileEntry
	providerSummary := make(map[string]ManifestProvider)

	for _, provName := range opts.Providers {
		records, err := m.store.ListFileRecords(provName)
		if err != nil {
			m.logger.Warn("failed to list files for provider", "provider", provName, "error", err)
			continue
		}

		mp := ManifestProvider{}
		for _, rec := range records {
			absPath := filepath.Join(m.config.Server.DataDir, provName, rec.Path)
			if _, err := os.Stat(absPath); os.IsNotExist(err) {
				m.logger.Warn("file in store but not on disk, skipping", "path", absPath)
				continue
			}

			allFiles = append(allFiles, fileEntry{
				provider: provName,
				relPath:  rec.Path,
				absPath:  absPath,
				size:     rec.Size,
				sha256:   rec.SHA256,
			})
			mp.FileCount++
			mp.TotalSize += rec.Size
		}
		providerSummary[provName] = mp
	}

	if len(allFiles) == 0 {
		return nil, fmt.Errorf("no files to export")
	}

	// Create split archives
	archiveNum := 1
	currentSize := int64(0)
	var archives []ArchiveInfo
	var currentFiles []string

	var tarWriter *tar.Writer
	var zstdWriter *zstd.Encoder
	var archiveFile *os.File
	var archivePath string

	openArchive := func() error {
		name := fmt.Sprintf("airgap-transfer-%03d.tar.zst", archiveNum)
		archivePath = filepath.Join(opts.OutputDir, name)

		var err error
		archiveFile, err = os.Create(archivePath)
		if err != nil {
			return fmt.Errorf("creating archive %s: %w", name, err)
		}
		zstdWriter, err = zstd.NewWriter(archiveFile)
		if err != nil {
			archiveFile.Close()
			return fmt.Errorf("creating zstd writer: %w", err)
		}
		tarWriter = tar.NewWriter(zstdWriter)
		currentFiles = nil
		currentSize = 0
		return nil
	}

	closeArchive := func() (*ArchiveInfo, error) {
		if tarWriter == nil {
			return nil, nil
		}
		if err := tarWriter.Close(); err != nil {
			return nil, fmt.Errorf("closing tar writer: %w", err)
		}
		if err := zstdWriter.Close(); err != nil {
			return nil, fmt.Errorf("closing zstd writer: %w", err)
		}
		if err := archiveFile.Close(); err != nil {
			return nil, fmt.Errorf("closing archive file: %w", err)
		}

		// Compute SHA256 of the archive
		hash, size, err := hashFile(archivePath)
		if err != nil {
			return nil, fmt.Errorf("hashing archive: %w", err)
		}

		name := filepath.Base(archivePath)
		info := &ArchiveInfo{
			Name:   name,
			Size:   size,
			SHA256: hash,
			Files:  currentFiles,
		}

		// Write .sha256 sidecar
		sidecar := archivePath + ".sha256"
		content := fmt.Sprintf("%s  %s\n", hash, name)
		if err := os.WriteFile(sidecar, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("writing sha256 sidecar: %w", err)
		}

		tarWriter = nil
		zstdWriter = nil
		archiveFile = nil
		archiveNum++
		return info, nil
	}

	// Open first archive
	if err := openArchive(); err != nil {
		return nil, err
	}

	for _, f := range allFiles {
		select {
		case <-ctx.Done():
			// Clean up on cancel
			if tarWriter != nil {
				tarWriter.Close()
				zstdWriter.Close()
				archiveFile.Close()
			}
			return nil, ctx.Err()
		default:
		}

		// Roll to next archive if this file would exceed split size
		// (unless current archive is empty — a single large file must go somewhere)
		if currentSize > 0 && currentSize+f.size > opts.SplitSize {
			info, err := closeArchive()
			if err != nil {
				return nil, err
			}
			archives = append(archives, *info)
			if err := openArchive(); err != nil {
				return nil, err
			}
		}

		// Add file to tar
		tarPath := filepath.Join(f.provider, f.relPath)
		if err := addFileToTar(tarWriter, f.absPath, tarPath); err != nil {
			return nil, fmt.Errorf("adding %s to archive: %w", tarPath, err)
		}
		currentFiles = append(currentFiles, tarPath)
		currentSize += f.size
	}

	// Close final archive
	info, err := closeArchive()
	if err != nil {
		return nil, err
	}
	if info != nil {
		archives = append(archives, *info)
	}

	// Build manifest
	hostname, _ := os.Hostname()
	var fileInventory []ManifestFile
	var totalSize int64
	for _, f := range allFiles {
		fileInventory = append(fileInventory, ManifestFile{
			Provider: f.provider,
			Path:     f.relPath,
			Size:     f.size,
			SHA256:   f.sha256,
		})
		totalSize += f.size
	}

	manifest := &TransferManifest{
		Version:       "1.0",
		Created:       time.Now().UTC(),
		SourceHost:    hostname,
		Providers:     providerSummary,
		Archives:      archivesToManifest(archives),
		TotalArchives: len(archives),
		TotalSize:     totalSize,
		FileInventory: fileInventory,
	}

	// Write manifest JSON
	manifestPath := filepath.Join(opts.OutputDir, "airgap-manifest.json")
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}

	// Write manifest .sha256
	manifestHash, _, err := hashFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("hashing manifest: %w", err)
	}
	manifestSidecar := manifestPath + ".sha256"
	sidecarContent := fmt.Sprintf("%s  %s\n", manifestHash, "airgap-manifest.json")
	if err := os.WriteFile(manifestSidecar, []byte(sidecarContent), 0o644); err != nil {
		return nil, fmt.Errorf("writing manifest sha256: %w", err)
	}

	// Write TRANSFER-README.txt
	readmePath := filepath.Join(opts.OutputDir, "TRANSFER-README.txt")
	readme := generateTransferReadme(manifest)
	if err := os.WriteFile(readmePath, []byte(readme), 0o644); err != nil {
		return nil, fmt.Errorf("writing TRANSFER-README.txt: %w", err)
	}

	// Record in store
	transfer := &store.Transfer{
		Direction:    "export",
		Path:         opts.OutputDir,
		Providers:    strings.Join(opts.Providers, ","),
		ArchiveCount: len(archives),
		TotalSize:    totalSize,
		ManifestHash: manifestHash,
		Status:       "completed",
		StartTime:    startTime,
		EndTime:      time.Now(),
	}
	if err := m.store.CreateTransfer(transfer); err != nil {
		m.logger.Warn("failed to record transfer in store", "error", err)
	}

	duration := time.Since(startTime)
	m.logger.Info("export completed",
		"archives", len(archives),
		"files", len(allFiles),
		"total_size", totalSize,
		"duration", duration,
	)

	return &ExportReport{
		Archives:     archives,
		TotalFiles:   len(allFiles),
		TotalSize:    totalSize,
		ManifestPath: manifestPath,
		Duration:     duration,
	}, nil
}

// addFileToTar adds a single file to a tar archive.
func addFileToTar(tw *tar.Writer, srcPath, tarPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:    tarPath,
		Size:    stat.Size(),
		Mode:    int64(stat.Mode()),
		ModTime: stat.ModTime(),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if _, err := io.Copy(tw, f); err != nil {
		return err
	}
	return nil
}

// hashFile computes the SHA256 of a file, returning hex string and size.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// archivesToManifest converts ArchiveInfo slice to ManifestArchive slice.
func archivesToManifest(archives []ArchiveInfo) []ManifestArchive {
	result := make([]ManifestArchive, len(archives))
	for i, a := range archives {
		result[i] = ManifestArchive{
			Name:   a.Name,
			Size:   a.Size,
			SHA256: a.SHA256,
			Files:  a.Files,
		}
	}
	return result
}

func formatSizeReadme(bytes int64) string {
	const (
		gb = 1024 * 1024 * 1024
		mb = 1024 * 1024
	)
	if bytes >= gb {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
}

// generateTransferReadme creates the human-readable README for transfer media.
func generateTransferReadme(m *TransferManifest) string {
	var b strings.Builder
	b.WriteString("AIRGAP TRANSFER PACKAGE\n")
	b.WriteString("=======================\n")
	b.WriteString(fmt.Sprintf("Created: %s\n", m.Created.Format("2006-01-02 15:04 UTC")))
	b.WriteString(fmt.Sprintf("Source: %s\n", m.SourceHost))
	b.WriteString(fmt.Sprintf("Archives: %d parts\n", m.TotalArchives))
	b.WriteString(fmt.Sprintf("Total size: %s\n", formatSizeReadme(m.TotalSize)))
	b.WriteString(fmt.Sprintf("Files: %d\n", len(m.FileInventory)))
	b.WriteString("\nProviders included:\n")
	for name, p := range m.Providers {
		b.WriteString(fmt.Sprintf("  - %s (%d files, %s)\n", name, p.FileCount, formatSizeReadme(p.TotalSize)))
	}
	b.WriteString("\nTO IMPORT:\n")
	b.WriteString("1. Mount this disk on the disconnected machine\n")
	b.WriteString("2. Run: airgap import --from /mnt/usb\n")
	b.WriteString("3. The tool will validate all archives before extracting\n")
	b.WriteString("\nIF AN ARCHIVE IS CORRUPT:\n")
	b.WriteString("- The import tool will tell you which archive(s) failed\n")
	b.WriteString("- Re-copy only the failed archive from the source machine\n")
	b.WriteString("- Re-run: airgap import --from /mnt/usb\n")
	return b.String()
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestExport -v -timeout 60s`
Expected: All 3 tests PASS

---

### Task 6: Import engine implementation + tests

**Files:**
- Create: `internal/engine/import.go`
- Add to: `internal/engine/export_test.go` (import tests go here for round-trip testing)

**Step 1: Write the failing tests**

Add to `internal/engine/export_test.go`:

```go
func TestImportRoundTrip(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Export first
	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Create a fresh data dir for import target
	importDataDir := t.TempDir()
	mgr.config.Server.DataDir = importDataDir

	// Import
	report, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir:  outputDir,
		VerifyOnly: false,
		Force:      false,
	})
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}

	if report.ArchivesFailed != 0 {
		t.Errorf("expected 0 failed archives, got %d", report.ArchivesFailed)
	}
	if report.FilesExtracted != 3 {
		t.Errorf("expected 3 files extracted, got %d", report.FilesExtracted)
	}

	// Verify files exist in the new data dir
	expectedFiles := []string{
		"epel/9/Packages/foo.rpm",
		"epel/9/Packages/bar.rpm",
		"ocp_binaries/4.18/oc",
	}
	for _, f := range expectedFiles {
		p := filepath.Join(importDataDir, f)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("expected file not found after import: %s", f)
		}
	}
}

func TestImportVerifyOnly(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Export
	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	importDataDir := t.TempDir()
	mgr.config.Server.DataDir = importDataDir

	report, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir:  outputDir,
		VerifyOnly: true,
	})
	if err != nil {
		t.Fatalf("Import() verify-only error: %v", err)
	}

	if report.ArchivesValidated != 1 {
		t.Errorf("expected 1 archive validated, got %d", report.ArchivesValidated)
	}
	if report.FilesExtracted != 0 {
		t.Errorf("verify-only should extract 0 files, got %d", report.FilesExtracted)
	}
}

func TestImportDetectsCorruptArchive(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Export
	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Corrupt the archive
	archivePath := filepath.Join(outputDir, "airgap-transfer-001.tar.zst")
	if err := os.WriteFile(archivePath, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}

	importDataDir := t.TempDir()
	mgr.config.Server.DataDir = importDataDir

	report, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir: outputDir,
	})
	// Import should return an error or report with failures
	if err == nil && report.ArchivesFailed == 0 {
		t.Fatal("expected failure for corrupted archive")
	}
}

func TestImportMissingManifest(t *testing.T) {
	mgr, _, _ := setupExportTest(t)

	emptyDir := t.TempDir()
	_, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir: emptyDir,
	})
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestImport -v`
Expected: FAIL — `Import` method and `ImportOptions` undefined

**Step 3: Write import.go implementation**

Create `internal/engine/import.go`:

```go
package engine

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/store"
	"github.com/klauspost/compress/zstd"
)

// ImportOptions configures an import operation.
type ImportOptions struct {
	SourceDir  string
	VerifyOnly bool
	Force      bool
}

// ImportReport summarizes a completed import.
type ImportReport struct {
	ArchivesValidated int
	ArchivesFailed    int
	FilesExtracted    int
	TotalSize         int64
	Duration          time.Duration
	Errors            []string
}

// Import reads an airgap transfer package and extracts its contents.
func (m *SyncManager) Import(ctx context.Context, opts ImportOptions) (*ImportReport, error) {
	startTime := time.Now()

	// Read manifest
	manifestPath := filepath.Join(opts.SourceDir, "airgap-manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	var manifest TransferManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	m.logger.Info("import starting",
		"source", opts.SourceDir,
		"archives", manifest.TotalArchives,
		"files", len(manifest.FileInventory),
	)

	// Verify all archive files are present
	for _, arch := range manifest.Archives {
		archPath := filepath.Join(opts.SourceDir, arch.Name)
		if _, err := os.Stat(archPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("archive not found: %s", arch.Name)
		}
	}

	// Create a transfer record
	transfer := &store.Transfer{
		Direction: "import",
		Path:      opts.SourceDir,
		Status:    "running",
		StartTime: startTime,
	}
	if err := m.store.CreateTransfer(transfer); err != nil {
		m.logger.Warn("failed to record transfer", "error", err)
	}

	report := &ImportReport{}

	// Validate archives
	for _, arch := range manifest.Archives {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		archPath := filepath.Join(opts.SourceDir, arch.Name)

		if !opts.Force {
			m.logger.Info("validating archive", "name", arch.Name)
			actualHash, _, err := hashFile(archPath)
			if err != nil {
				report.ArchivesFailed++
				report.Errors = append(report.Errors, fmt.Sprintf("hashing %s: %v", arch.Name, err))
				continue
			}

			if actualHash != arch.SHA256 {
				report.ArchivesFailed++
				report.Errors = append(report.Errors,
					fmt.Sprintf("%s: expected sha256 %s, got %s", arch.Name, arch.SHA256, actualHash))
				continue
			}

			m.logger.Info("archive validated", "name", arch.Name)
		}

		report.ArchivesValidated++

		// Record archive validation in store
		if transfer.ID != 0 {
			ta := &store.TransferArchive{
				TransferID:  transfer.ID,
				ArchiveName: arch.Name,
				SHA256:      arch.SHA256,
				Size:        arch.Size,
				Validated:   true,
				ValidatedAt: time.Now(),
			}
			if err := m.store.CreateTransferArchive(ta); err != nil {
				m.logger.Warn("failed to record archive validation", "error", err)
			}
		}
	}

	// If any archives failed, stop
	if report.ArchivesFailed > 0 {
		report.Duration = time.Since(startTime)
		if transfer.ID != 0 {
			transfer.Status = "failed"
			transfer.ErrorMessage = fmt.Sprintf("%d archive(s) failed validation", report.ArchivesFailed)
			transfer.EndTime = time.Now()
			_ = m.store.UpdateTransfer(transfer)
		}
		return report, fmt.Errorf("%d archive(s) failed validation", report.ArchivesFailed)
	}

	// If verify-only, stop here
	if opts.VerifyOnly {
		report.Duration = time.Since(startTime)
		if transfer.ID != 0 {
			transfer.Status = "completed"
			transfer.ArchiveCount = report.ArchivesValidated
			transfer.EndTime = time.Now()
			_ = m.store.UpdateTransfer(transfer)
		}
		m.logger.Info("verify-only complete", "validated", report.ArchivesValidated)
		return report, nil
	}

	// Extract archives
	for _, arch := range manifest.Archives {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		archPath := filepath.Join(opts.SourceDir, arch.Name)
		m.logger.Info("extracting archive", "name", arch.Name)

		extracted, size, err := m.extractArchive(archPath)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("extracting %s: %v", arch.Name, err))
			continue
		}

		report.FilesExtracted += extracted
		report.TotalSize += size
	}

	// Upsert file records from manifest inventory
	for _, f := range manifest.FileInventory {
		absPath := filepath.Join(m.config.Server.DataDir, f.Provider, f.Path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			continue // file wasn't extracted (maybe from a failed archive)
		}

		rec := &store.FileRecord{
			Provider:     f.Provider,
			Path:         f.Path,
			Size:         f.Size,
			SHA256:       f.SHA256,
			LastModified: time.Now(),
			LastVerified: time.Now(),
		}
		if err := m.store.UpsertFileRecord(rec); err != nil {
			m.logger.Warn("failed to upsert file record", "path", f.Path, "error", err)
		}
	}

	report.Duration = time.Since(startTime)

	// Update transfer record
	if transfer.ID != 0 {
		transfer.Status = "completed"
		transfer.ArchiveCount = report.ArchivesValidated
		transfer.TotalSize = report.TotalSize
		transfer.EndTime = time.Now()
		_ = m.store.UpdateTransfer(transfer)
	}

	m.logger.Info("import completed",
		"files_extracted", report.FilesExtracted,
		"total_size", report.TotalSize,
		"duration", report.Duration,
	)

	return report, nil
}

// extractArchive decompresses and untars an archive into the data directory.
// Returns files extracted count and total bytes.
func (m *SyncManager) extractArchive(archivePath string) (int, int64, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return 0, 0, fmt.Errorf("opening archive: %w", err)
	}
	defer f.Close()

	zr, err := zstd.NewReader(f)
	if err != nil {
		return 0, 0, fmt.Errorf("creating zstd reader: %w", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)

	extracted := 0
	totalSize := int64(0)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return extracted, totalSize, fmt.Errorf("reading tar entry: %w", err)
		}

		// Skip directories
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Sanitize path to prevent directory traversal
		cleanPath := filepath.Clean(header.Name)
		if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
			return extracted, totalSize, fmt.Errorf("unsafe path in archive: %s", header.Name)
		}

		destPath := filepath.Join(m.config.Server.DataDir, cleanPath)

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return extracted, totalSize, fmt.Errorf("creating directory: %w", err)
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			return extracted, totalSize, fmt.Errorf("creating file %s: %w", destPath, err)
		}

		n, err := io.Copy(outFile, tr)
		outFile.Close()
		if err != nil {
			return extracted, totalSize, fmt.Errorf("extracting %s: %w", header.Name, err)
		}

		extracted++
		totalSize += n
	}

	return extracted, totalSize, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestImport -v -timeout 60s`
Expected: All 4 tests PASS

---

### Task 7: Wire CLI commands to engine

**Files:**
- Modify: `cmd/airgap/export.go`
- Modify: `cmd/airgap/importcmd.go`

**Step 1: Update export.go CLI to call engine**

Replace the `exportRun` function in `cmd/airgap/export.go` to call `globalEngine.Export()`:

```go
func exportRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	var providers []string
	if exportProvider != "" {
		providers = strings.Split(exportProvider, ",")
		for i, p := range providers {
			providers[i] = strings.TrimSpace(p)
		}
	} else {
		for name := range globalCfg.Providers {
			if globalCfg.ProviderEnabled(name) {
				providers = append(providers, name)
			}
		}
	}

	if len(providers) == 0 {
		log.Warn("no providers to export")
		return nil
	}

	splitSize, err := engine.ParseSize(exportSplitSize)
	if err != nil {
		return fmt.Errorf("invalid split size %q: %w", exportSplitSize, err)
	}

	fmt.Printf("Exporting to %s...\n", exportTo)
	fmt.Printf("  Providers: %v\n", providers)
	fmt.Printf("  Split size: %s\n", exportSplitSize)
	fmt.Printf("  Compression: %s\n", exportCompression)
	fmt.Println()

	report, err := globalEngine.Export(cmd.Context(), engine.ExportOptions{
		OutputDir:   exportTo,
		Providers:   providers,
		SplitSize:   splitSize,
		Compression: exportCompression,
	})
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	fmt.Printf("Export complete:\n")
	fmt.Printf("  Archives: %d\n", len(report.Archives))
	fmt.Printf("  Files: %d\n", report.TotalFiles)
	fmt.Printf("  Total size: %s\n", formatBytes(report.TotalSize))
	fmt.Printf("  Duration: %s\n", report.Duration.Round(time.Second))
	fmt.Printf("  Manifest: %s\n", report.ManifestPath)

	for _, arch := range report.Archives {
		fmt.Printf("  - %s (%s)\n", arch.Name, formatBytes(arch.Size))
	}

	return nil
}
```

Note: add `"time"` and `"github.com/BadgerOps/airgap/internal/engine"` to the imports.

**Step 2: Update importcmd.go CLI to call engine**

Replace the `importRun` function in `cmd/airgap/importcmd.go`:

```go
func importRun(cmd *cobra.Command, args []string) error {
	if globalEngine == nil {
		return fmt.Errorf("engine not initialized")
	}

	fmt.Printf("Importing from %s...\n", importFrom)
	if importVerifyOnly {
		fmt.Println("  Mode: verify only")
	}
	if importForce {
		fmt.Println("  Mode: force (skip checksum verification)")
	}
	fmt.Println()

	report, err := globalEngine.Import(cmd.Context(), engine.ImportOptions{
		SourceDir:  importFrom,
		VerifyOnly: importVerifyOnly,
		Force:      importForce,
	})
	if err != nil {
		// Still print partial report if available
		if report != nil {
			printImportReport(report)
		}
		return fmt.Errorf("import failed: %w", err)
	}

	printImportReport(report)
	return nil
}

func printImportReport(report *engine.ImportReport) {
	fmt.Printf("Import results:\n")
	fmt.Printf("  Archives validated: %d\n", report.ArchivesValidated)
	fmt.Printf("  Archives failed: %d\n", report.ArchivesFailed)
	fmt.Printf("  Files extracted: %d\n", report.FilesExtracted)
	fmt.Printf("  Total size: %s\n", formatBytes(report.TotalSize))
	fmt.Printf("  Duration: %s\n", report.Duration.Round(time.Second))
	if len(report.Errors) > 0 {
		fmt.Println("  Errors:")
		for _, e := range report.Errors {
			fmt.Printf("    - %s\n", e)
		}
	}
}
```

Note: add `"time"` and `"github.com/BadgerOps/airgap/internal/engine"` to the imports. The `formatBytes` function already exists in `cmd/airgap/status.go`.

**Step 3: Verify compilation**

Run: `cd /Users/badger/code/ocp-offline && go build ./cmd/airgap/`
Expected: Success, no errors

---

### Task 8: Run all tests

**Step 1: Run the full test suite**

Run: `cd /Users/badger/code/ocp-offline && go test ./... -timeout 120s`
Expected: All tests PASS

**Step 2: Run with race detector**

Run: `cd /Users/badger/code/ocp-offline && go test -race ./internal/engine/ -timeout 120s`
Expected: No race conditions detected

---

### Task 9: Integration test — full round-trip

**Files:**
- The round-trip test is already in Task 6 (`TestImportRoundTrip`). This task verifies it end-to-end.

**Step 1: Run the round-trip test in verbose mode**

Run: `cd /Users/badger/code/ocp-offline && go test ./internal/engine/ -run TestImportRoundTrip -v -timeout 60s`
Expected: PASS — sync → export → import → files verified

**Step 2: Build binary and test CLI help output**

Run: `cd /Users/badger/code/ocp-offline && go build -o bin/airgap ./cmd/airgap/ && ./bin/airgap export --help && ./bin/airgap import --help`
Expected: Help text shows flags and examples for both commands
