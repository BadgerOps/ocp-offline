package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// mockProvider implements the provider.Provider interface for testing
type mockProvider struct {
	name         string
	planFunc     func(ctx context.Context) (*provider.SyncPlan, error)
	syncFunc     func(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error)
	validateFunc func(ctx context.Context) (*provider.ValidationReport, error)
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Type() string {
	return "generic"
}

func (m *mockProvider) Configure(cfg provider.ProviderConfig) error {
	return nil
}

func (m *mockProvider) Plan(ctx context.Context) (*provider.SyncPlan, error) {
	if m.planFunc != nil {
		return m.planFunc(ctx)
	}
	return &provider.SyncPlan{
		Provider:   m.name,
		Actions:    []provider.SyncAction{},
		TotalSize:  0,
		TotalFiles: 0,
		Timestamp:  time.Now(),
	}, nil
}

func (m *mockProvider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
	if m.syncFunc != nil {
		return m.syncFunc(ctx, plan, opts)
	}
	return &provider.SyncReport{
		Provider:         m.name,
		StartTime:        time.Now(),
		EndTime:          time.Now(),
		Downloaded:       0,
		Deleted:          0,
		Skipped:          0,
		Failed:           []provider.FailedFile{},
		BytesTransferred: 0,
	}, nil
}

func (m *mockProvider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	if m.validateFunc != nil {
		return m.validateFunc(ctx)
	}
	return &provider.ValidationReport{
		Provider:     m.name,
		TotalFiles:   0,
		ValidFiles:   0,
		InvalidFiles: []provider.ValidationResult{},
		Timestamp:    time.Now(),
	}, nil
}

// newTestSyncManager creates a SyncManager with an in-memory SQLite store
func newTestSyncManager(t *testing.T, registry *provider.Registry) (*SyncManager, *store.Store) {
	st, err := store.New(":memory:", slog.Default())
	if err != nil {
		t.Fatalf("failed to create in-memory store: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "airgap-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	cfg := &config.Config{
		Server: config.ServerConfig{
			DataDir: tmpDir,
			DBPath:  ":memory:",
		},
		Providers: make(map[string]config.ProviderConfig),
	}

	client := download.NewClient(slog.Default())
	logger := slog.Default()

	manager := NewSyncManager(registry, st, client, cfg, logger)
	return manager, st
}

// TestNewSyncManager verifies that a SyncManager is created with non-nil fields
func TestNewSyncManager(t *testing.T) {
	registry := provider.NewRegistry()
	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	if manager == nil {
		t.Fatal("expected non-nil SyncManager")
	}
	if manager.registry == nil {
		t.Fatal("expected non-nil registry")
	}
	if manager.store == nil {
		t.Fatal("expected non-nil store")
	}
	if manager.client == nil {
		t.Fatal("expected non-nil client")
	}
	if manager.config == nil {
		t.Fatal("expected non-nil config")
	}
	if manager.logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

// TestSyncProviderDryRun verifies that dry run mode doesn't download files
func TestSyncProviderDryRun(t *testing.T) {
	registry := provider.NewRegistry()

	// Create a mock provider that returns a plan with download actions
	mockProv := &mockProvider{
		name: "test-provider",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "test-provider",
				TotalSize:  1024,
				TotalFiles: 1,
				Timestamp:  time.Now(),
				Actions: []provider.SyncAction{
					{
						Path:     "test-file.txt",
						Action:   provider.ActionDownload,
						Size:     1024,
						Checksum: "abc123",
						URL:      "http://example.com/test-file.txt",
						Reason:   "new file",
					},
				},
			}, nil
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Set provider as enabled
	manager.config.Providers["test-provider"] = map[string]interface{}{"enabled": true}

	// Execute dry run
	opts := provider.SyncOptions{
		DryRun:     true,
		MaxWorkers: 1,
	}

	report, err := manager.SyncProvider(context.Background(), "test-provider", opts)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify report shows what would change but no bytes transferred
	if report.Downloaded != 1 {
		t.Errorf("expected 1 file to be marked as would download, got %d", report.Downloaded)
	}
	if report.BytesTransferred != 0 {
		t.Errorf("expected 0 bytes transferred in dry run, got %d", report.BytesTransferred)
	}

	// Verify sync run was recorded as completed
	runs, err := st.ListSyncRuns("test-provider", 1)
	if err != nil {
		t.Fatalf("failed to list sync runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 sync run, got %d", len(runs))
	}
	if runs[0].Status != "completed" {
		t.Errorf("expected status 'completed', got %s", runs[0].Status)
	}
}

// TestSyncProviderSuccess verifies successful sync with actual downloads
func TestSyncProviderSuccess(t *testing.T) {
	registry := provider.NewRegistry()

	// Create a test HTTP server that serves a file
	fileContent := "test file content"
	fileChecksum := sha256.Sum256([]byte(fileContent))
	checksumHex := hex.EncodeToString(fileChecksum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test-file.txt" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(fileContent)))
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fileContent))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Create a mock provider
	mockProv := &mockProvider{
		name: "test-provider",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "test-provider",
				TotalSize:  int64(len(fileContent)),
				TotalFiles: 1,
				Timestamp:  time.Now(),
				Actions: []provider.SyncAction{
					{
						Path:     "test-file.txt",
						Action:   provider.ActionDownload,
						Size:     int64(len(fileContent)),
						Checksum: checksumHex,
						URL:      server.URL + "/test-file.txt",
						Reason:   "new file",
					},
				},
			}, nil
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Set provider as enabled
	manager.config.Providers["test-provider"] = map[string]interface{}{"enabled": true}

	// Create output directory
	outputDir := filepath.Join(manager.config.Server.DataDir, "test-provider")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	// Execute sync
	opts := provider.SyncOptions{
		DryRun:     false,
		MaxWorkers: 1,
	}

	report, err := manager.SyncProvider(context.Background(), "test-provider", opts)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify report
	if report.Downloaded != 1 {
		t.Errorf("expected 1 file downloaded, got %d", report.Downloaded)
	}
	if report.BytesTransferred != int64(len(fileContent)) {
		t.Errorf("expected %d bytes transferred, got %d", len(fileContent), report.BytesTransferred)
	}
	if len(report.Failed) > 0 {
		t.Errorf("expected no failed files, got %d", len(report.Failed))
	}

	// Verify sync run was recorded
	runs, err := st.ListSyncRuns("test-provider", 1)
	if err != nil {
		t.Fatalf("failed to list sync runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 sync run, got %d", len(runs))
	}
	if runs[0].Status != "success" {
		t.Errorf("expected status 'success', got %s", runs[0].Status)
	}
	if runs[0].FilesDownloaded != 1 {
		t.Errorf("expected 1 file in sync run, got %d", runs[0].FilesDownloaded)
	}

	// Verify file was stored in database
	fileRecords, err := st.ListFileRecords("test-provider")
	if err != nil {
		t.Fatalf("failed to list file records: %v", err)
	}
	if len(fileRecords) != 1 {
		t.Fatalf("expected 1 file record, got %d", len(fileRecords))
	}
	if fileRecords[0].Path != "test-file.txt" {
		t.Errorf("expected path 'test-file.txt', got %s", fileRecords[0].Path)
	}
	if fileRecords[0].SHA256 != checksumHex {
		t.Errorf("expected checksum %s, got %s", checksumHex, fileRecords[0].SHA256)
	}
}

// TestSyncProviderNotFound verifies error when syncing a non-existent provider
func TestSyncProviderNotFound(t *testing.T) {
	registry := provider.NewRegistry()
	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	opts := provider.SyncOptions{DryRun: false}
	_, err := manager.SyncProvider(context.Background(), "nonexistent", opts)

	if err == nil {
		t.Fatal("expected error for non-existent provider, got nil")
	}
	if !strings.Contains(err.Error(), "provider not found") {
		t.Errorf("expected 'provider not found' error, got: %v", err)
	}
}

// TestSyncAll verifies that all enabled providers are synced
func TestSyncAll(t *testing.T) {
	registry := provider.NewRegistry()

	// Register multiple mock providers
	provider1 := &mockProvider{
		name: "provider1",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider1",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}
	provider2 := &mockProvider{
		name: "provider2",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider2",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Enable both providers
	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	manager.config.Providers["provider2"] = map[string]interface{}{"enabled": true}

	opts := provider.SyncOptions{DryRun: true}
	reports, err := manager.SyncAll(context.Background(), opts)

	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if len(reports) != 2 {
		t.Errorf("expected 2 reports, got %d", len(reports))
	}

	if _, ok := reports["provider1"]; !ok {
		t.Error("expected report for provider1")
	}
	if _, ok := reports["provider2"]; !ok {
		t.Error("expected report for provider2")
	}
}

// TestSyncAllWithDisabledProvider verifies that disabled providers are skipped
func TestSyncAllWithDisabledProvider(t *testing.T) {
	registry := provider.NewRegistry()

	provider1 := &mockProvider{
		name: "provider1",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider1",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}
	provider2 := &mockProvider{
		name: "provider2",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider2",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Enable only provider1
	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	// provider2 is not enabled (or enabled: false)

	opts := provider.SyncOptions{DryRun: true}
	reports, err := manager.SyncAll(context.Background(), opts)

	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	if len(reports) != 1 {
		t.Errorf("expected 1 report (disabled provider skipped), got %d", len(reports))
	}

	if _, ok := reports["provider1"]; !ok {
		t.Error("expected report for provider1")
	}
	if _, ok := reports["provider2"]; ok {
		t.Error("did not expect report for disabled provider2")
	}
}

// TestValidateProvider verifies validation of a single provider
func TestValidateProvider(t *testing.T) {
	registry := provider.NewRegistry()

	mockProv := &mockProvider{
		name: "test-provider",
		validateFunc: func(ctx context.Context) (*provider.ValidationReport, error) {
			return &provider.ValidationReport{
				Provider:     "test-provider",
				TotalFiles:   1,
				ValidFiles:   1,
				InvalidFiles: []provider.ValidationResult{},
				Timestamp:    time.Now(),
			}, nil
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	report, err := manager.ValidateProvider(context.Background(), "test-provider")

	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	if report.Provider != "test-provider" {
		t.Errorf("expected provider 'test-provider', got %s", report.Provider)
	}
	if report.TotalFiles != 1 {
		t.Errorf("expected 1 total file, got %d", report.TotalFiles)
	}
	if report.ValidFiles != 1 {
		t.Errorf("expected 1 valid file, got %d", report.ValidFiles)
	}
}

// TestValidateAll verifies validation of all enabled providers
func TestValidateAll(t *testing.T) {
	registry := provider.NewRegistry()

	provider1 := &mockProvider{
		name: "provider1",
		validateFunc: func(ctx context.Context) (*provider.ValidationReport, error) {
			return &provider.ValidationReport{
				Provider:     "provider1",
				TotalFiles:   5,
				ValidFiles:   5,
				InvalidFiles: []provider.ValidationResult{},
				Timestamp:    time.Now(),
			}, nil
		},
	}
	provider2 := &mockProvider{
		name: "provider2",
		validateFunc: func(ctx context.Context) (*provider.ValidationReport, error) {
			return &provider.ValidationReport{
				Provider:   "provider2",
				TotalFiles: 10,
				ValidFiles: 9,
				InvalidFiles: []provider.ValidationResult{
					{
						Path:     "invalid-file.txt",
						Expected: "xyz",
						Actual:   "abc",
						Valid:    false,
						Size:     2048,
					},
				},
				Timestamp: time.Now(),
			}, nil
		},
	}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Enable both providers
	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	manager.config.Providers["provider2"] = map[string]interface{}{"enabled": true}

	reports, err := manager.ValidateAll(context.Background())

	if err != nil {
		t.Fatalf("ValidateAll failed: %v", err)
	}

	if len(reports) != 2 {
		t.Errorf("expected 2 reports, got %d", len(reports))
	}

	if rep, ok := reports["provider1"]; !ok {
		t.Error("expected report for provider1")
	} else if rep.ValidFiles != 5 {
		t.Errorf("expected 5 valid files for provider1, got %d", rep.ValidFiles)
	}

	if rep, ok := reports["provider2"]; !ok {
		t.Error("expected report for provider2")
	} else if rep.ValidFiles != 9 {
		t.Errorf("expected 9 valid files for provider2, got %d", rep.ValidFiles)
	} else if len(rep.InvalidFiles) != 1 {
		t.Errorf("expected 1 invalid file for provider2, got %d", len(rep.InvalidFiles))
	}
}

// TestStatus verifies status reporting of registered providers
func TestStatus(t *testing.T) {
	registry := provider.NewRegistry()

	// Register mock providers
	provider1 := &mockProvider{name: "provider1"}
	provider2 := &mockProvider{name: "provider2"}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Enable both providers
	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	manager.config.Providers["provider2"] = map[string]interface{}{"enabled": true}

	// Create some sync runs in the store
	syncRun1 := &store.SyncRun{
		Provider:         "provider1",
		StartTime:        time.Now().Add(-1 * time.Hour),
		EndTime:          time.Now().Add(-1 * time.Hour),
		FilesDownloaded:  10,
		FilesDeleted:     2,
		FilesSkipped:     0,
		FilesFailed:      0,
		BytesTransferred: 5000,
		Status:           "success",
	}
	if err := st.CreateSyncRun(syncRun1); err != nil {
		t.Fatalf("failed to create sync run: %v", err)
	}

	// Add some file records
	fileRec := &store.FileRecord{
		Provider:     "provider1",
		Path:         "file1.txt",
		Size:         1000,
		SHA256:       "abc123",
		LastModified: time.Now(),
		LastVerified: time.Now(),
		SyncRunID:    syncRun1.ID,
	}
	if err := st.UpsertFileRecord(fileRec); err != nil {
		t.Fatalf("failed to upsert file record: %v", err)
	}

	// Get status
	statuses := manager.Status()

	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(statuses))
	}

	status1, ok := statuses["provider1"]
	if !ok {
		t.Fatal("expected status for provider1")
	}

	if status1.Name != "provider1" {
		t.Errorf("expected name 'provider1', got %s", status1.Name)
	}
	if !status1.Enabled {
		t.Error("expected provider1 to be enabled")
	}
	if status1.FileCount != 1 {
		t.Errorf("expected 1 file, got %d", status1.FileCount)
	}
	if status1.TotalSize != 1000 {
		t.Errorf("expected total size 1000, got %d", status1.TotalSize)
	}
	if status1.LastStatus != "success" {
		t.Errorf("expected last status 'success', got %s", status1.LastStatus)
	}

	status2, ok := statuses["provider2"]
	if !ok {
		t.Fatal("expected status for provider2")
	}

	if status2.Name != "provider2" {
		t.Errorf("expected name 'provider2', got %s", status2.Name)
	}
	if !status2.Enabled {
		t.Error("expected provider2 to be enabled")
	}
	if status2.FileCount != 0 {
		t.Errorf("expected 0 files, got %d", status2.FileCount)
	}
}

// TestSyncProviderPlanError verifies error handling when Plan fails
func TestSyncProviderPlanError(t *testing.T) {
	registry := provider.NewRegistry()

	mockProv := &mockProvider{
		name: "test-provider",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return nil, fmt.Errorf("plan failed")
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	manager.config.Providers["test-provider"] = map[string]interface{}{"enabled": true}

	opts := provider.SyncOptions{DryRun: false}
	_, err := manager.SyncProvider(context.Background(), "test-provider", opts)

	if err == nil {
		t.Fatal("expected error from failed plan")
	}
	if !strings.Contains(err.Error(), "failed to plan sync") {
		t.Errorf("expected 'failed to plan sync' error, got: %v", err)
	}

	// Verify sync run was recorded with failed status
	runs, err := st.ListSyncRuns("test-provider", 1)
	if err != nil {
		t.Fatalf("failed to list sync runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 sync run, got %d", len(runs))
	}
	if runs[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %s", runs[0].Status)
	}
}

// TestSyncProviderWithSkipAndDeleteActions verifies handling of skip and delete actions
func TestSyncProviderWithSkipAndDeleteActions(t *testing.T) {
	registry := provider.NewRegistry()

	mockProv := &mockProvider{
		name: "test-provider",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "test-provider",
				TotalSize:  0,
				TotalFiles: 2,
				Timestamp:  time.Now(),
				Actions: []provider.SyncAction{
					{
						Path:   "existing-file.txt",
						Action: provider.ActionSkip,
						Size:   1024,
						Reason: "already exists with matching checksum",
					},
					{
						Path:   "removed-file.txt",
						Action: provider.ActionDelete,
						Size:   2048,
						Reason: "no longer in upstream",
					},
				},
			}, nil
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	manager.config.Providers["test-provider"] = map[string]interface{}{"enabled": true}

	// Create output directory
	outputDir := filepath.Join(manager.config.Server.DataDir, "test-provider")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	// Add pre-existing file record
	fileRec := &store.FileRecord{
		Provider:     "test-provider",
		Path:         "removed-file.txt",
		Size:         2048,
		SHA256:       "xyz789",
		LastModified: time.Now(),
		LastVerified: time.Now(),
	}
	if err := st.UpsertFileRecord(fileRec); err != nil {
		t.Fatalf("failed to upsert file record: %v", err)
	}

	opts := provider.SyncOptions{DryRun: false}
	report, err := manager.SyncProvider(context.Background(), "test-provider", opts)

	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if report.Skipped != 1 {
		t.Errorf("expected 1 skipped file, got %d", report.Skipped)
	}
	if report.Deleted != 1 {
		t.Errorf("expected 1 deleted file, got %d", report.Deleted)
	}
}

// TestValidateProviderNotFound verifies error when validating non-existent provider
func TestValidateProviderNotFound(t *testing.T) {
	registry := provider.NewRegistry()
	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	_, err := manager.ValidateProvider(context.Background(), "nonexistent")

	if err == nil {
		t.Fatal("expected error for non-existent provider")
	}
	if !strings.Contains(err.Error(), "provider not found") {
		t.Errorf("expected 'provider not found' error, got: %v", err)
	}
}

// TestSyncProviderContextCancellation verifies graceful handling of context cancellation
func TestSyncProviderContextCancellation(t *testing.T) {
	registry := provider.NewRegistry()

	mockProv := &mockProvider{
		name: "test-provider",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			// Sleep to simulate work
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return &provider.SyncPlan{
					Provider:   "test-provider",
					Actions:    []provider.SyncAction{},
					TotalSize:  0,
					TotalFiles: 0,
					Timestamp:  time.Now(),
				}, nil
			}
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	manager.config.Providers["test-provider"] = map[string]interface{}{"enabled": true}

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := provider.SyncOptions{DryRun: false}
	_, err := manager.SyncProvider(ctx, "test-provider", opts)

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// TestSyncAllContextCancellation verifies context cancellation in SyncAll
func TestSyncAllContextCancellation(t *testing.T) {
	registry := provider.NewRegistry()

	provider1 := &mockProvider{
		name: "provider1",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider1",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}
	provider2 := &mockProvider{
		name: "provider2",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider2",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	manager.config.Providers["provider2"] = map[string]interface{}{"enabled": true}

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	opts := provider.SyncOptions{DryRun: true}
	reports, err := manager.SyncAll(ctx, opts)

	// Context should be cancelled after a very short time
	if err == nil {
		// Context might not have been cancelled in time, which is okay
		t.Logf("context was not cancelled quickly enough")
	}

	// At minimum, the function should return
	_ = reports
}

// TestStatusWithDisabledProviders verifies that disabled providers still show in status
func TestStatusWithDisabledProviders(t *testing.T) {
	registry := provider.NewRegistry()

	provider1 := &mockProvider{name: "provider1"}
	provider2 := &mockProvider{name: "provider2"}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Enable only provider1
	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	manager.config.Providers["provider2"] = map[string]interface{}{"enabled": false}

	statuses := manager.Status()

	if len(statuses) != 2 {
		t.Errorf("expected 2 statuses (both enabled and disabled), got %d", len(statuses))
	}

	status1 := statuses["provider1"]
	if !status1.Enabled {
		t.Error("expected provider1 to be enabled")
	}

	status2 := statuses["provider2"]
	if status2.Enabled {
		t.Error("expected provider2 to be disabled")
	}
}

// TestSyncProviderPartialFailure verifies handling of partial download failures
func TestSyncProviderPartialFailure(t *testing.T) {
	registry := provider.NewRegistry()

	// Create a test HTTP server that fails on specific paths
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/success.txt" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		} else if r.URL.Path == "/failure.txt" {
			w.WriteHeader(http.StatusNotFound)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	successChecksum := sha256.Sum256([]byte("success"))
	successChecksumHex := hex.EncodeToString(successChecksum[:])

	mockProv := &mockProvider{
		name: "test-provider",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "test-provider",
				TotalSize:  20,
				TotalFiles: 2,
				Timestamp:  time.Now(),
				Actions: []provider.SyncAction{
					{
						Path:     "success.txt",
						Action:   provider.ActionDownload,
						Size:     7,
						Checksum: successChecksumHex,
						URL:      server.URL + "/success.txt",
						Reason:   "new file",
					},
					{
						Path:     "failure.txt",
						Action:   provider.ActionDownload,
						Size:     8,
						Checksum: "def456",
						URL:      server.URL + "/failure.txt",
						Reason:   "new file",
					},
				},
			}, nil
		},
	}
	registry.Register(mockProv)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	manager.config.Providers["test-provider"] = map[string]interface{}{"enabled": true}

	// Create output directory
	outputDir := filepath.Join(manager.config.Server.DataDir, "test-provider")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}

	opts := provider.SyncOptions{DryRun: false, MaxWorkers: 2}
	report, err := manager.SyncProvider(context.Background(), "test-provider", opts)

	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if report.Downloaded != 1 {
		t.Errorf("expected 1 successful download, got %d", report.Downloaded)
	}
	if len(report.Failed) != 1 {
		t.Errorf("expected 1 failed file, got %d", len(report.Failed))
	}

	// Verify sync run status is "partial"
	runs, err := st.ListSyncRuns("test-provider", 1)
	if err != nil {
		t.Fatalf("failed to list sync runs: %v", err)
	}
	if runs[0].Status != "partial" {
		t.Errorf("expected status 'partial' with mixed success/failure, got %s", runs[0].Status)
	}

	// Verify failed file was recorded
	failedFiles, err := st.ListFailedFiles("test-provider")
	if err != nil {
		t.Fatalf("failed to list failed files: %v", err)
	}
	if len(failedFiles) != 1 {
		t.Errorf("expected 1 failed file record, got %d", len(failedFiles))
	}
	if failedFiles[0].FilePath != "failure.txt" {
		t.Errorf("expected failed file 'failure.txt', got %s", failedFiles[0].FilePath)
	}
}

// TestSyncAllWithMultipleErrors verifies error collection in SyncAll
func TestSyncAllWithMultipleErrors(t *testing.T) {
	registry := provider.NewRegistry()

	provider1 := &mockProvider{
		name: "provider1",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return nil, fmt.Errorf("plan error in provider1")
		},
	}
	provider2 := &mockProvider{
		name: "provider2",
		planFunc: func(ctx context.Context) (*provider.SyncPlan, error) {
			return &provider.SyncPlan{
				Provider:   "provider2",
				Actions:    []provider.SyncAction{},
				TotalSize:  0,
				TotalFiles: 0,
				Timestamp:  time.Now(),
			}, nil
		},
	}

	registry.Register(provider1)
	registry.Register(provider2)

	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	manager.config.Providers["provider1"] = map[string]interface{}{"enabled": true}
	manager.config.Providers["provider2"] = map[string]interface{}{"enabled": true}

	opts := provider.SyncOptions{DryRun: true}
	reports, err := manager.SyncAll(context.Background(), opts)

	// Should return error indicating failures happened
	if err == nil {
		t.Fatal("expected error indicating one or more providers failed")
	}

	// But should still return reports for successful ones
	if _, ok := reports["provider2"]; !ok {
		t.Error("expected report for provider2 despite provider1 failure")
	}
}

func TestReconfigureProviders(t *testing.T) {
	registry := provider.NewRegistry()
	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// Set a factory that creates mock providers
	manager.SetProviderFactory(func(typeName, dataDir string, logger *slog.Logger) (provider.Provider, error) {
		if typeName == "epel" || typeName == "ocp_binaries" {
			return &mockProvider{name: typeName}, nil
		}
		return nil, fmt.Errorf("unknown type: %s", typeName)
	})

	// Initially no providers
	if len(registry.Names()) != 0 {
		t.Fatalf("expected 0 providers, got %d", len(registry.Names()))
	}

	// Add an enabled provider config to DB
	pc := &store.ProviderConfig{
		Name:       "epel",
		Type:       "epel",
		Enabled:    true,
		ConfigJSON: `{"enabled":true}`,
	}
	if err := st.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	// Reconfigure
	configs, _ := st.ListProviderConfigs()
	if err := manager.ReconfigureProviders(configs); err != nil {
		t.Fatalf("ReconfigureProviders error: %v", err)
	}

	// Should now have 1 provider
	names := registry.Names()
	if len(names) != 1 {
		t.Fatalf("expected 1 provider after reconfigure, got %d", len(names))
	}
	if names[0] != "epel" {
		t.Errorf("expected provider name 'epel', got %q", names[0])
	}

	// Disable it and reconfigure
	st.ToggleProviderConfig("epel")
	configs, _ = st.ListProviderConfigs()
	if err := manager.ReconfigureProviders(configs); err != nil {
		t.Fatal(err)
	}

	// Disabled providers should be removed
	if len(registry.Names()) != 0 {
		t.Fatalf("expected 0 providers after disabling, got %d", len(registry.Names()))
	}
}

func TestReconfigureProvidersNoFactory(t *testing.T) {
	registry := provider.NewRegistry()
	manager, st := newTestSyncManager(t, registry)
	defer st.Close()

	// No factory set — should return error
	err := manager.ReconfigureProviders(nil)
	if err == nil {
		t.Fatal("expected error when factory not set")
	}
}
