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
)

// setupExportTest creates a temp data dir with fake synced files and a store with matching records.
func setupExportTest(t *testing.T) (*SyncManager, string, string) {
	t.Helper()

	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create fake synced files under dataDir
	files := map[string]string{
		"epel/9/Packages/foo.rpm": "fake-rpm-content-foo",
		"epel/9/Packages/bar.rpm": "fake-rpm-content-bar",
		"ocp_binaries/4.18/oc":    "fake-oc-binary",
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
	_ = h // unused but shows the pattern

	// Use a very small split size to force multiple archives
	report, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   50, // 50 bytes — will force splits
		Compression: "zstd",
	})
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

// stubProvider is a minimal Provider implementation for export tests.
type stubProvider struct {
	name     string
	typeName string
}

func (s *stubProvider) Name() string                { return s.name }
func (s *stubProvider) Type() string                { return s.typeName }
func (s *stubProvider) Configure(cfg provider.ProviderConfig) error { return nil }
func (s *stubProvider) Plan(ctx context.Context) (*provider.SyncPlan, error) { return nil, nil }
func (s *stubProvider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
	return nil, nil
}
func (s *stubProvider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	return nil, nil
}

func TestExportManifestIncludesProviderType(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Register stub providers with types
	mgr.registry.Register(&stubProvider{name: "epel", typeName: "rpm_repo"})
	mgr.registry.Register(&stubProvider{name: "ocp_binaries", typeName: "binary"})

	report, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Read and parse manifest
	manifestData, err := os.ReadFile(report.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest TransferManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if manifest.Providers["epel"].Type != "rpm_repo" {
		t.Errorf("epel type = %q, want %q", manifest.Providers["epel"].Type, "rpm_repo")
	}
	if manifest.Providers["ocp_binaries"].Type != "binary" {
		t.Errorf("ocp_binaries type = %q, want %q", manifest.Providers["ocp_binaries"].Type, "binary")
	}
}

func TestImportSkipValidatedArchives(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Export
	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// First import (validates all archives)
	importDataDir := t.TempDir()
	mgr.config.Server.DataDir = importDataDir

	report1, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir: outputDir,
	})
	if err != nil {
		t.Fatalf("first Import() error: %v", err)
	}
	if report1.ArchivesValidated != 1 {
		t.Fatalf("expected 1 archive validated on first import, got %d", report1.ArchivesValidated)
	}

	// Second import with SkipValidated
	importDataDir2 := t.TempDir()
	mgr.config.Server.DataDir = importDataDir2

	report2, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir:     outputDir,
		SkipValidated: true,
	})
	if err != nil {
		t.Fatalf("second Import() error: %v", err)
	}
	if report2.ArchivesSkipped != 1 {
		t.Errorf("expected 1 archive skipped, got %d", report2.ArchivesSkipped)
	}
	if report2.FilesExtracted != 0 {
		t.Errorf("expected 0 files extracted (skipped), got %d", report2.FilesExtracted)
	}
}

func TestImportSkipValidated_ForceOverrides(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Export
	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// First import
	importDataDir := t.TempDir()
	mgr.config.Server.DataDir = importDataDir

	_, err = mgr.Import(context.Background(), ImportOptions{
		SourceDir: outputDir,
	})
	if err != nil {
		t.Fatalf("first Import() error: %v", err)
	}

	// Second import with Force + SkipValidated — Force should override
	importDataDir2 := t.TempDir()
	mgr.config.Server.DataDir = importDataDir2

	report, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir:     outputDir,
		SkipValidated: true,
		Force:         true,
	})
	if err != nil {
		t.Fatalf("force Import() error: %v", err)
	}
	// Force skips all validation including skip-validated check
	if report.ArchivesSkipped != 0 {
		t.Errorf("expected 0 skipped with Force, got %d", report.ArchivesSkipped)
	}
	if report.FilesExtracted != 3 {
		t.Errorf("expected 3 files extracted with Force, got %d", report.FilesExtracted)
	}
}

func TestImportSucceedsWithoutCreaterepoC(t *testing.T) {
	mgr, _, outputDir := setupExportTest(t)

	// Register stub providers so export sets type
	mgr.registry.Register(&stubProvider{name: "epel", typeName: "rpm_repo"})
	mgr.registry.Register(&stubProvider{name: "ocp_binaries", typeName: "binary"})

	// Export with types
	_, err := mgr.Export(context.Background(), ExportOptions{
		OutputDir:   outputDir,
		Providers:   []string{"epel", "ocp_binaries"},
		SplitSize:   1024 * 1024 * 1024,
		Compression: "zstd",
	})
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	// Import into fresh dir — createrepo_c not in PATH won't cause failure
	importDataDir := t.TempDir()
	mgr.config.Server.DataDir = importDataDir

	// Use empty PATH so createrepo_c won't be found
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	report, err := mgr.Import(context.Background(), ImportOptions{
		SourceDir: outputDir,
	})
	if err != nil {
		t.Fatalf("Import() should succeed without createrepo_c: %v", err)
	}
	if report.FilesExtracted != 3 {
		t.Errorf("expected 3 files extracted, got %d", report.FilesExtracted)
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
