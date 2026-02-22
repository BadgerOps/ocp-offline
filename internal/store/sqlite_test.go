package store

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// newTestStore creates an in-memory SQLite store for testing
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ============================================================================
// Store Lifecycle Tests
// ============================================================================

func TestNew(t *testing.T) {
	store, err := New(":memory:", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer store.Close()

	if store.db == nil {
		t.Error("Expected db to be initialized")
	}

	if store.logger == nil {
		t.Error("Expected logger to be initialized")
	}
}

func TestNewInMemory(t *testing.T) {
	store, err := New(":memory:", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New(\":memory:\") failed: %v", err)
	}
	defer store.Close()

	// Verify migrations ran by checking we can create a SyncRun
	run := &SyncRun{
		Provider:  "test-provider",
		StartTime: time.Now(),
		Status:    "success",
	}
	err = store.CreateSyncRun(run)
	if err != nil {
		t.Fatalf("CreateSyncRun() failed: %v", err)
	}

	if run.ID == 0 {
		t.Error("Expected ID to be set after CreateSyncRun")
	}
}

func TestClose(t *testing.T) {
	store, err := New(":memory:", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	err = store.Close()
	if err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Verify the connection is closed by trying to use it
	_, err = store.ListSyncRuns("", 0)
	if err == nil {
		t.Error("Expected error when using closed store, but got nil")
	}
}

// ============================================================================
// SyncRun CRUD Tests
// ============================================================================

func TestCreateSyncRun(t *testing.T) {
	store := newTestStore(t)

	run := &SyncRun{
		Provider:         "test-provider",
		StartTime:        time.Now(),
		FilesDownloaded:  5,
		FilesDeleted:     2,
		FilesSkipped:     1,
		FilesFailed:      0,
		BytesTransferred: 1024000,
		Status:           "success",
	}

	err := store.CreateSyncRun(run)
	if err != nil {
		t.Fatalf("CreateSyncRun() failed: %v", err)
	}

	if run.ID == 0 {
		t.Error("Expected ID to be set after CreateSyncRun")
	}

	// Verify the record was created
	retrieved, err := store.GetSyncRun(run.ID)
	if err != nil {
		t.Fatalf("GetSyncRun() failed: %v", err)
	}

	if retrieved.Provider != run.Provider {
		t.Errorf("Provider mismatch: got %q, want %q", retrieved.Provider, run.Provider)
	}

	if retrieved.FilesDownloaded != run.FilesDownloaded {
		t.Errorf("FilesDownloaded mismatch: got %d, want %d", retrieved.FilesDownloaded, run.FilesDownloaded)
	}

	if retrieved.Status != run.Status {
		t.Errorf("Status mismatch: got %q, want %q", retrieved.Status, run.Status)
	}
}

func TestUpdateSyncRun(t *testing.T) {
	store := newTestStore(t)

	// Create a sync run
	run := &SyncRun{
		Provider:        "test-provider",
		StartTime:       time.Now(),
		Status:          "success",
		FilesDownloaded: 5,
	}

	err := store.CreateSyncRun(run)
	if err != nil {
		t.Fatalf("CreateSyncRun() failed: %v", err)
	}

	originalID := run.ID

	// Update it
	run.Status = "partial"
	run.FilesDownloaded = 10
	run.FilesFailed = 3
	run.ErrorMessage = "Some files failed"

	err = store.UpdateSyncRun(run)
	if err != nil {
		t.Fatalf("UpdateSyncRun() failed: %v", err)
	}

	// Verify changes persisted
	retrieved, err := store.GetSyncRun(originalID)
	if err != nil {
		t.Fatalf("GetSyncRun() failed: %v", err)
	}

	if retrieved.Status != "partial" {
		t.Errorf("Status not updated: got %q, want %q", retrieved.Status, "partial")
	}

	if retrieved.FilesDownloaded != 10 {
		t.Errorf("FilesDownloaded not updated: got %d, want %d", retrieved.FilesDownloaded, 10)
	}

	if retrieved.FilesFailed != 3 {
		t.Errorf("FilesFailed not updated: got %d, want %d", retrieved.FilesFailed, 3)
	}

	if retrieved.ErrorMessage != "Some files failed" {
		t.Errorf("ErrorMessage not updated: got %q, want %q", retrieved.ErrorMessage, "Some files failed")
	}
}

func TestUpdateSyncRunNotFound(t *testing.T) {
	store := newTestStore(t)

	run := &SyncRun{
		ID:       99999,
		Provider: "non-existent",
		Status:   "success",
	}

	err := store.UpdateSyncRun(run)
	if err == nil {
		t.Error("Expected error when updating non-existent SyncRun")
	}
}

func TestGetSyncRun(t *testing.T) {
	store := newTestStore(t)

	// Create a sync run
	created := &SyncRun{
		Provider:  "test-provider",
		StartTime: time.Now(),
		Status:    "success",
	}

	err := store.CreateSyncRun(created)
	if err != nil {
		t.Fatalf("CreateSyncRun() failed: %v", err)
	}

	// Retrieve it
	retrieved, err := store.GetSyncRun(created.ID)
	if err != nil {
		t.Fatalf("GetSyncRun() failed: %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("ID mismatch: got %d, want %d", retrieved.ID, created.ID)
	}

	if retrieved.Provider != created.Provider {
		t.Errorf("Provider mismatch: got %q, want %q", retrieved.Provider, created.Provider)
	}
}

func TestGetSyncRunNotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.GetSyncRun(99999)
	if err == nil {
		t.Error("Expected error when getting non-existent SyncRun")
	}
}

func TestListSyncRuns(t *testing.T) {
	store := newTestStore(t)

	// Create sync runs for different providers
	providers := []string{"provider-a", "provider-b", "provider-a"}
	runIDs := make([]int64, len(providers))

	for i, provider := range providers {
		run := &SyncRun{
			Provider:  provider,
			StartTime: time.Now().Add(time.Duration(i) * time.Hour),
			Status:    "success",
		}

		err := store.CreateSyncRun(run)
		if err != nil {
			t.Fatalf("CreateSyncRun() failed: %v", err)
		}
		runIDs[i] = run.ID
	}

	// List all runs
	allRuns, err := store.ListSyncRuns("", 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() failed: %v", err)
	}

	if len(allRuns) != 3 {
		t.Errorf("Expected 3 runs, got %d", len(allRuns))
	}

	// List runs for provider-a
	providerARuns, err := store.ListSyncRuns("provider-a", 0)
	if err != nil {
		t.Fatalf("ListSyncRuns(provider-a) failed: %v", err)
	}

	if len(providerARuns) != 2 {
		t.Errorf("Expected 2 runs for provider-a, got %d", len(providerARuns))
	}

	for _, run := range providerARuns {
		if run.Provider != "provider-a" {
			t.Errorf("Expected provider-a, got %q", run.Provider)
		}
	}

	// List runs for provider-b
	providerBRuns, err := store.ListSyncRuns("provider-b", 0)
	if err != nil {
		t.Fatalf("ListSyncRuns(provider-b) failed: %v", err)
	}

	if len(providerBRuns) != 1 {
		t.Errorf("Expected 1 run for provider-b, got %d", len(providerBRuns))
	}

	if providerBRuns[0].Provider != "provider-b" {
		t.Errorf("Expected provider-b, got %q", providerBRuns[0].Provider)
	}
}

func TestListSyncRunsOrdering(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	times := []time.Time{
		now.Add(-2 * time.Hour),
		now.Add(-1 * time.Hour),
		now,
	}

	for i, startTime := range times {
		run := &SyncRun{
			Provider:  "test",
			StartTime: startTime,
			Status:    "success",
		}

		if i > 0 && i < len(times)-1 {
			run.EndTime = startTime.Add(30 * time.Minute)
		}

		err := store.CreateSyncRun(run)
		if err != nil {
			t.Fatalf("CreateSyncRun() failed: %v", err)
		}
	}

	runs, err := store.ListSyncRuns("test", 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() failed: %v", err)
	}

	if len(runs) != 3 {
		t.Fatalf("Expected 3 runs, got %d", len(runs))
	}

	// Check descending order by start_time
	if runs[0].StartTime.Before(runs[1].StartTime) {
		t.Error("Expected runs to be ordered by start_time DESC")
	}

	if runs[1].StartTime.Before(runs[2].StartTime) {
		t.Error("Expected runs to be ordered by start_time DESC")
	}
}

func TestListSyncRunsWithLimit(t *testing.T) {
	store := newTestStore(t)

	// Create 5 sync runs
	for i := 0; i < 5; i++ {
		run := &SyncRun{
			Provider:  "test",
			StartTime: time.Now().Add(-time.Duration(i) * time.Hour),
			Status:    "success",
		}

		err := store.CreateSyncRun(run)
		if err != nil {
			t.Fatalf("CreateSyncRun() failed: %v", err)
		}
	}

	// List with limit
	runs, err := store.ListSyncRuns("", 2)
	if err != nil {
		t.Fatalf("ListSyncRuns() with limit failed: %v", err)
	}

	if len(runs) != 2 {
		t.Errorf("Expected 2 runs with limit=2, got %d", len(runs))
	}
}

// ============================================================================
// FileRecord CRUD Tests
// ============================================================================

func TestUpsertFileRecord(t *testing.T) {
	store := newTestStore(t)

	rec := &FileRecord{
		Provider:     "test-provider",
		Path:         "path/to/file.txt",
		Size:         12345,
		SHA256:       "abc123def456",
		LastModified: time.Now(),
		LastVerified: time.Now(),
	}

	err := store.UpsertFileRecord(rec)
	if err != nil {
		t.Fatalf("UpsertFileRecord() failed: %v", err)
	}

	if rec.ID == 0 {
		t.Error("Expected ID to be set after UpsertFileRecord")
	}

	// Verify the record was created
	retrieved, err := store.GetFileRecord(rec.Provider, rec.Path)
	if err != nil {
		t.Fatalf("GetFileRecord() failed: %v", err)
	}

	if retrieved.Size != rec.Size {
		t.Errorf("Size mismatch: got %d, want %d", retrieved.Size, rec.Size)
	}

	if retrieved.SHA256 != rec.SHA256 {
		t.Errorf("SHA256 mismatch: got %q, want %q", retrieved.SHA256, rec.SHA256)
	}

	if retrieved.Path != rec.Path {
		t.Errorf("Path mismatch: got %q, want %q", retrieved.Path, rec.Path)
	}
}

func TestUpsertFileRecordUpdate(t *testing.T) {
	store := newTestStore(t)

	// Create initial record
	rec := &FileRecord{
		Provider:     "test-provider",
		Path:         "path/to/file.txt",
		Size:         12345,
		SHA256:       "abc123def456",
		LastModified: time.Now(),
		LastVerified: time.Now(),
	}

	err := store.UpsertFileRecord(rec)
	if err != nil {
		t.Fatalf("UpsertFileRecord() failed: %v", err)
	}

	originalID := rec.ID

	// Upsert the same path with updated values
	rec.Size = 67890
	rec.SHA256 = "xyz789uvw123"
	rec.LastModified = time.Now().Add(time.Hour)

	err = store.UpsertFileRecord(rec)
	if err != nil {
		t.Fatalf("UpsertFileRecord() update failed: %v", err)
	}

	// Verify the record was updated, not inserted as new
	retrieved, err := store.GetFileRecord(rec.Provider, rec.Path)
	if err != nil {
		t.Fatalf("GetFileRecord() failed: %v", err)
	}

	if retrieved.ID != originalID {
		t.Errorf("ID changed on upsert: got %d, want %d", retrieved.ID, originalID)
	}

	if retrieved.Size != 67890 {
		t.Errorf("Size not updated: got %d, want %d", retrieved.Size, 67890)
	}

	if retrieved.SHA256 != "xyz789uvw123" {
		t.Errorf("SHA256 not updated: got %q, want %q", retrieved.SHA256, "xyz789uvw123")
	}
}

func TestGetFileRecord(t *testing.T) {
	store := newTestStore(t)

	rec := &FileRecord{
		Provider:     "test-provider",
		Path:         "path/to/file.txt",
		Size:         12345,
		SHA256:       "abc123def456",
		LastModified: time.Now(),
	}

	err := store.UpsertFileRecord(rec)
	if err != nil {
		t.Fatalf("UpsertFileRecord() failed: %v", err)
	}

	retrieved, err := store.GetFileRecord(rec.Provider, rec.Path)
	if err != nil {
		t.Fatalf("GetFileRecord() failed: %v", err)
	}

	if retrieved.ID != rec.ID {
		t.Errorf("ID mismatch: got %d, want %d", retrieved.ID, rec.ID)
	}

	if retrieved.Provider != rec.Provider {
		t.Errorf("Provider mismatch: got %q, want %q", retrieved.Provider, rec.Provider)
	}
}

func TestGetFileRecordNotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.GetFileRecord("non-existent", "path/to/file.txt")
	if err == nil {
		t.Error("Expected error when getting non-existent FileRecord")
	}
}

func TestListFileRecords(t *testing.T) {
	store := newTestStore(t)

	// Create file records for different providers
	files := []struct {
		provider string
		path     string
	}{
		{"provider-a", "file1.txt"},
		{"provider-a", "file2.txt"},
		{"provider-a", "file3.txt"},
		{"provider-b", "file1.txt"},
	}

	for _, f := range files {
		rec := &FileRecord{
			Provider: f.provider,
			Path:     f.path,
			Size:     1024,
		}

		err := store.UpsertFileRecord(rec)
		if err != nil {
			t.Fatalf("UpsertFileRecord() failed: %v", err)
		}
	}

	// List files for provider-a
	aFiles, err := store.ListFileRecords("provider-a")
	if err != nil {
		t.Fatalf("ListFileRecords(provider-a) failed: %v", err)
	}

	if len(aFiles) != 3 {
		t.Errorf("Expected 3 files for provider-a, got %d", len(aFiles))
	}

	for _, f := range aFiles {
		if f.Provider != "provider-a" {
			t.Errorf("Expected provider-a, got %q", f.Provider)
		}
	}

	// List files for provider-b
	bFiles, err := store.ListFileRecords("provider-b")
	if err != nil {
		t.Fatalf("ListFileRecords(provider-b) failed: %v", err)
	}

	if len(bFiles) != 1 {
		t.Errorf("Expected 1 file for provider-b, got %d", len(bFiles))
	}
}

func TestListFileRecordsOrdering(t *testing.T) {
	store := newTestStore(t)

	paths := []string{"zzz.txt", "aaa.txt", "mmm.txt"}

	for _, path := range paths {
		rec := &FileRecord{
			Provider: "test",
			Path:     path,
			Size:     1024,
		}

		err := store.UpsertFileRecord(rec)
		if err != nil {
			t.Fatalf("UpsertFileRecord() failed: %v", err)
		}
	}

	files, err := store.ListFileRecords("test")
	if err != nil {
		t.Fatalf("ListFileRecords() failed: %v", err)
	}

	// Should be ordered by path
	if files[0].Path != "aaa.txt" || files[1].Path != "mmm.txt" || files[2].Path != "zzz.txt" {
		t.Errorf("Files not ordered by path: got [%s, %s, %s]", files[0].Path, files[1].Path, files[2].Path)
	}
}

func TestDeleteFileRecord(t *testing.T) {
	store := newTestStore(t)

	rec := &FileRecord{
		Provider: "test-provider",
		Path:     "path/to/file.txt",
		Size:     1024,
	}

	err := store.UpsertFileRecord(rec)
	if err != nil {
		t.Fatalf("UpsertFileRecord() failed: %v", err)
	}

	// Delete the record
	err = store.DeleteFileRecord(rec.Provider, rec.Path)
	if err != nil {
		t.Fatalf("DeleteFileRecord() failed: %v", err)
	}

	// Verify it's gone
	_, err = store.GetFileRecord(rec.Provider, rec.Path)
	if err == nil {
		t.Error("Expected error when getting deleted FileRecord")
	}
}

func TestDeleteFileRecordNotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteFileRecord("non-existent", "path/to/file.txt")
	if err == nil {
		t.Error("Expected error when deleting non-existent FileRecord")
	}
}

func TestCountFileRecords(t *testing.T) {
	store := newTestStore(t)

	// Create file records
	files := []struct {
		provider string
		path     string
	}{
		{"provider-a", "file1.txt"},
		{"provider-a", "file2.txt"},
		{"provider-a", "file3.txt"},
		{"provider-b", "file1.txt"},
		{"provider-b", "file2.txt"},
	}

	for _, f := range files {
		rec := &FileRecord{
			Provider: f.provider,
			Path:     f.path,
			Size:     1024,
		}

		err := store.UpsertFileRecord(rec)
		if err != nil {
			t.Fatalf("UpsertFileRecord() failed: %v", err)
		}
	}

	// Count files for provider-a
	countA, err := store.CountFileRecords("provider-a")
	if err != nil {
		t.Fatalf("CountFileRecords(provider-a) failed: %v", err)
	}

	if countA != 3 {
		t.Errorf("Expected 3 files for provider-a, got %d", countA)
	}

	// Count files for provider-b
	countB, err := store.CountFileRecords("provider-b")
	if err != nil {
		t.Fatalf("CountFileRecords(provider-b) failed: %v", err)
	}

	if countB != 2 {
		t.Errorf("Expected 2 files for provider-b, got %d", countB)
	}

	// Count files for non-existent provider
	countNone, err := store.CountFileRecords("non-existent")
	if err != nil {
		t.Fatalf("CountFileRecords(non-existent) failed: %v", err)
	}

	if countNone != 0 {
		t.Errorf("Expected 0 files for non-existent provider, got %d", countNone)
	}
}

func TestSumFileSize(t *testing.T) {
	store := newTestStore(t)

	// Create file records with known sizes
	files := []struct {
		provider string
		path     string
		size     int64
	}{
		{"provider-a", "file1.txt", 1000},
		{"provider-a", "file2.txt", 2000},
		{"provider-a", "file3.txt", 3000},
		{"provider-b", "file1.txt", 5000},
		{"provider-b", "file2.txt", 6000},
	}

	for _, f := range files {
		rec := &FileRecord{
			Provider: f.provider,
			Path:     f.path,
			Size:     f.size,
		}

		err := store.UpsertFileRecord(rec)
		if err != nil {
			t.Fatalf("UpsertFileRecord() failed: %v", err)
		}
	}

	// Sum sizes for provider-a
	sumA, err := store.SumFileSize("provider-a")
	if err != nil {
		t.Fatalf("SumFileSize(provider-a) failed: %v", err)
	}

	expectedA := int64(6000)
	if sumA != expectedA {
		t.Errorf("Expected sum %d for provider-a, got %d", expectedA, sumA)
	}

	// Sum sizes for provider-b
	sumB, err := store.SumFileSize("provider-b")
	if err != nil {
		t.Fatalf("SumFileSize(provider-b) failed: %v", err)
	}

	expectedB := int64(11000)
	if sumB != expectedB {
		t.Errorf("Expected sum %d for provider-b, got %d", expectedB, sumB)
	}

	// Sum sizes for non-existent provider
	sumNone, err := store.SumFileSize("non-existent")
	if err != nil {
		t.Fatalf("SumFileSize(non-existent) failed: %v", err)
	}

	if sumNone != 0 {
		t.Errorf("Expected sum 0 for non-existent provider, got %d", sumNone)
	}
}

// ============================================================================
// FailedFile Operations Tests
// ============================================================================

func TestAddFailedFile(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	rec := &FailedFileRecord{
		Provider:         "test-provider",
		FilePath:         "path/to/file.txt",
		URL:              "https://example.com/file.txt",
		ExpectedChecksum: "abc123def456",
		Error:            "connection timeout",
		RetryCount:       0,
		FirstFailure:     now,
		LastFailure:      now,
		Resolved:         false,
	}

	err := store.AddFailedFile(rec)
	if err != nil {
		t.Fatalf("AddFailedFile() failed: %v", err)
	}

	if rec.ID == 0 {
		t.Error("Expected ID to be set after AddFailedFile")
	}

	// Verify the record was created
	files, err := store.ListFailedFiles(rec.Provider)
	if err != nil {
		t.Fatalf("ListFailedFiles() failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 failed file record, got %d", len(files))
	}

	if files[0].FilePath != rec.FilePath {
		t.Errorf("FilePath mismatch: got %q, want %q", files[0].FilePath, rec.FilePath)
	}

	if files[0].Error != rec.Error {
		t.Errorf("Error mismatch: got %q, want %q", files[0].Error, rec.Error)
	}
}

func TestListFailedFiles(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Add failed files for different providers
	for i := 0; i < 3; i++ {
		rec := &FailedFileRecord{
			Provider:     "provider-a",
			FilePath:     "file" + string(rune(i)),
			URL:          "https://example.com/file" + string(rune(i)),
			Error:        "error" + string(rune(i)),
			FirstFailure: now.Add(-time.Duration(i) * time.Hour),
			LastFailure:  now.Add(-time.Duration(i) * time.Hour),
			Resolved:     false,
		}

		err := store.AddFailedFile(rec)
		if err != nil {
			t.Fatalf("AddFailedFile() failed: %v", err)
		}
	}

	for i := 0; i < 2; i++ {
		rec := &FailedFileRecord{
			Provider:     "provider-b",
			FilePath:     "file" + string(rune(i)),
			URL:          "https://example.com/file" + string(rune(i)),
			Error:        "error" + string(rune(i)),
			FirstFailure: now.Add(-time.Duration(i) * time.Hour),
			LastFailure:  now.Add(-time.Duration(i) * time.Hour),
			Resolved:     false,
		}

		err := store.AddFailedFile(rec)
		if err != nil {
			t.Fatalf("AddFailedFile() failed: %v", err)
		}
	}

	// List failed files for provider-a
	aFiles, err := store.ListFailedFiles("provider-a")
	if err != nil {
		t.Fatalf("ListFailedFiles(provider-a) failed: %v", err)
	}

	if len(aFiles) != 3 {
		t.Errorf("Expected 3 failed files for provider-a, got %d", len(aFiles))
	}

	for _, f := range aFiles {
		if f.Provider != "provider-a" {
			t.Errorf("Expected provider-a, got %q", f.Provider)
		}

		if f.Resolved {
			t.Error("Expected unresolved failed files")
		}
	}

	// List failed files for provider-b
	bFiles, err := store.ListFailedFiles("provider-b")
	if err != nil {
		t.Fatalf("ListFailedFiles(provider-b) failed: %v", err)
	}

	if len(bFiles) != 2 {
		t.Errorf("Expected 2 failed files for provider-b, got %d", len(bFiles))
	}
}

func TestListFailedFilesOrdering(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Add failed files with different failure times
	for i := 0; i < 3; i++ {
		rec := &FailedFileRecord{
			Provider:     "test",
			FilePath:     "file" + string(rune(i)),
			FirstFailure: now.Add(-time.Duration(2-i) * time.Hour),
			LastFailure:  now.Add(-time.Duration(2-i) * time.Hour),
			Resolved:     false,
		}

		err := store.AddFailedFile(rec)
		if err != nil {
			t.Fatalf("AddFailedFile() failed: %v", err)
		}
	}

	files, err := store.ListFailedFiles("test")
	if err != nil {
		t.Fatalf("ListFailedFiles() failed: %v", err)
	}

	// Should be ordered by last_failure DESC
	if !files[0].LastFailure.After(files[1].LastFailure) || !files[1].LastFailure.After(files[2].LastFailure) {
		t.Error("Failed files not ordered by last_failure DESC")
	}
}

func TestResolveFailedFile(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	rec := &FailedFileRecord{
		Provider:     "test-provider",
		FilePath:     "path/to/file.txt",
		Error:        "connection timeout",
		FirstFailure: now,
		LastFailure:  now,
		Resolved:     false,
	}

	err := store.AddFailedFile(rec)
	if err != nil {
		t.Fatalf("AddFailedFile() failed: %v", err)
	}

	// Resolve the failed file
	err = store.ResolveFailedFile(rec.ID)
	if err != nil {
		t.Fatalf("ResolveFailedFile() failed: %v", err)
	}

	// Verify it's no longer in the unresolved list
	files, err := store.ListFailedFiles(rec.Provider)
	if err != nil {
		t.Fatalf("ListFailedFiles() failed: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 unresolved failed files after resolve, got %d", len(files))
	}
}

func TestResolveFailedFileNotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.ResolveFailedFile(99999)
	if err == nil {
		t.Error("Expected error when resolving non-existent FailedFileRecord")
	}
}

func TestIncrementFailedRetry(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	rec := &FailedFileRecord{
		Provider:     "test-provider",
		FilePath:     "path/to/file.txt",
		Error:        "connection timeout",
		RetryCount:   0,
		FirstFailure: now,
		LastFailure:  now,
		Resolved:     false,
	}

	err := store.AddFailedFile(rec)
	if err != nil {
		t.Fatalf("AddFailedFile() failed: %v", err)
	}

	initialRetryCount := rec.RetryCount

	// Increment retry count
	err = store.IncrementFailedRetry(rec.ID)
	if err != nil {
		t.Fatalf("IncrementFailedRetry() failed: %v", err)
	}

	// Verify it was incremented (note: we can only verify through ListFailedFiles)
	files, err := store.ListFailedFiles(rec.Provider)
	if err != nil {
		t.Fatalf("ListFailedFiles() failed: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 failed file, got %d", len(files))
	}

	if files[0].RetryCount != initialRetryCount+1 {
		t.Errorf("RetryCount not incremented: got %d, want %d", files[0].RetryCount, initialRetryCount+1)
	}
}

func TestIncrementFailedRetryNotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.IncrementFailedRetry(99999)
	if err == nil {
		t.Error("Expected error when incrementing retry for non-existent FailedFileRecord")
	}
}

// ============================================================================
// Job Operations Tests
// ============================================================================

func TestCreateJob(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	job := &Job{
		Type:      "sync",
		Provider:  "test-provider",
		CronExpr:  "0 * * * *",
		Status:    "scheduled",
		LastRun:   now,
		NextRun:   now.Add(time.Hour),
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := store.CreateJob(job)
	if err != nil {
		t.Fatalf("CreateJob() failed: %v", err)
	}

	if job.ID == 0 {
		t.Error("Expected ID to be set after CreateJob")
	}

	// Verify the record was created
	jobs, err := store.ListJobs("", 0)
	if err != nil {
		t.Fatalf("ListJobs() failed: %v", err)
	}

	if len(jobs) == 0 {
		t.Fatal("Expected at least 1 job")
	}

	found := false
	for _, j := range jobs {
		if j.ID == job.ID {
			found = true
			if j.Type != job.Type {
				t.Errorf("Type mismatch: got %q, want %q", j.Type, job.Type)
			}
			if j.Provider != job.Provider {
				t.Errorf("Provider mismatch: got %q, want %q", j.Provider, job.Provider)
			}
			break
		}
	}

	if !found {
		t.Error("Created job not found in list")
	}
}

func TestUpdateJob(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	job := &Job{
		Type:      "sync",
		Provider:  "test-provider",
		Status:    "scheduled",
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := store.CreateJob(job)
	if err != nil {
		t.Fatalf("CreateJob() failed: %v", err)
	}

	originalID := job.ID

	// Update the job
	job.Status = "completed"
	job.LastRun = now.Add(time.Hour)
	job.UpdatedAt = now.Add(time.Hour)

	err = store.UpdateJob(job)
	if err != nil {
		t.Fatalf("UpdateJob() failed: %v", err)
	}

	// Verify changes persisted
	jobs, err := store.ListJobs("", 0)
	if err != nil {
		t.Fatalf("ListJobs() failed: %v", err)
	}

	found := false
	for _, j := range jobs {
		if j.ID == originalID {
			found = true
			if j.Status != "completed" {
				t.Errorf("Status not updated: got %q, want %q", j.Status, "completed")
			}
			break
		}
	}

	if !found {
		t.Error("Updated job not found in list")
	}
}

func TestUpdateJobNotFound(t *testing.T) {
	store := newTestStore(t)

	job := &Job{
		ID:     99999,
		Type:   "sync",
		Status: "scheduled",
	}

	err := store.UpdateJob(job)
	if err == nil {
		t.Error("Expected error when updating non-existent Job")
	}
}

func TestListJobs(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Create jobs with different statuses
	jobs := []struct {
		jobType string
		status  string
	}{
		{"sync", "scheduled"},
		{"validate", "scheduled"},
		{"export", "completed"},
		{"import", "completed"},
	}

	for _, j := range jobs {
		job := &Job{
			Type:      j.jobType,
			Status:    j.status,
			CreatedAt: now,
			UpdatedAt: now,
		}

		err := store.CreateJob(job)
		if err != nil {
			t.Fatalf("CreateJob() failed: %v", err)
		}
	}

	// List all jobs
	allJobs, err := store.ListJobs("", 0)
	if err != nil {
		t.Fatalf("ListJobs() failed: %v", err)
	}

	if len(allJobs) != 4 {
		t.Errorf("Expected 4 jobs, got %d", len(allJobs))
	}

	// List scheduled jobs
	scheduledJobs, err := store.ListJobs("scheduled", 0)
	if err != nil {
		t.Fatalf("ListJobs(scheduled) failed: %v", err)
	}

	if len(scheduledJobs) != 2 {
		t.Errorf("Expected 2 scheduled jobs, got %d", len(scheduledJobs))
	}

	for _, j := range scheduledJobs {
		if j.Status != "scheduled" {
			t.Errorf("Expected scheduled status, got %q", j.Status)
		}
	}

	// List completed jobs
	completedJobs, err := store.ListJobs("completed", 0)
	if err != nil {
		t.Fatalf("ListJobs(completed) failed: %v", err)
	}

	if len(completedJobs) != 2 {
		t.Errorf("Expected 2 completed jobs, got %d", len(completedJobs))
	}
}

func TestListJobsOrdering(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Create jobs with different next_run times
	nextRuns := []time.Time{
		now.Add(3 * time.Hour),
		now.Add(1 * time.Hour),
		now.Add(2 * time.Hour),
	}

	for _, nextRun := range nextRuns {
		job := &Job{
			Type:      "sync",
			Status:    "scheduled",
			NextRun:   nextRun,
			CreatedAt: now,
			UpdatedAt: now,
		}

		err := store.CreateJob(job)
		if err != nil {
			t.Fatalf("CreateJob() failed: %v", err)
		}
	}

	jobs, err := store.ListJobs("", 0)
	if err != nil {
		t.Fatalf("ListJobs() failed: %v", err)
	}

	// Should be ordered by next_run ASC
	for i := 0; i < len(jobs)-1; i++ {
		if jobs[i].NextRun.After(jobs[i+1].NextRun) {
			t.Error("Jobs not ordered by next_run ASC")
			break
		}
	}
}

func TestListJobsWithLimit(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Create 5 jobs
	for i := 0; i < 5; i++ {
		job := &Job{
			Type:      "sync",
			Status:    "scheduled",
			NextRun:   now.Add(time.Duration(i) * time.Hour),
			CreatedAt: now,
			UpdatedAt: now,
		}

		err := store.CreateJob(job)
		if err != nil {
			t.Fatalf("CreateJob() failed: %v", err)
		}
	}

	// List with limit
	jobs, err := store.ListJobs("", 2)
	if err != nil {
		t.Fatalf("ListJobs() with limit failed: %v", err)
	}

	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs with limit=2, got %d", len(jobs))
	}
}

// ============================================================================
// Transfer Operations Tests
// ============================================================================

func TestCreateTransfer(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	transfer := &Transfer{
		Direction:    "export",
		Path:         "/path/to/export",
		Providers:    "provider-a,provider-b",
		ArchiveCount: 5,
		TotalSize:    1024000,
		ManifestHash: "abc123def456",
		Status:       "running",
		StartTime:    now,
	}

	err := store.CreateTransfer(transfer)
	if err != nil {
		t.Fatalf("CreateTransfer() failed: %v", err)
	}

	if transfer.ID == 0 {
		t.Error("Expected ID to be set after CreateTransfer")
	}

	// Verify the record was created
	transfers, err := store.ListTransfers(0)
	if err != nil {
		t.Fatalf("ListTransfers() failed: %v", err)
	}

	if len(transfers) == 0 {
		t.Fatal("Expected at least 1 transfer")
	}

	found := false
	for _, tr := range transfers {
		if tr.ID == transfer.ID {
			found = true
			if tr.Direction != transfer.Direction {
				t.Errorf("Direction mismatch: got %q, want %q", tr.Direction, transfer.Direction)
			}
			if tr.Path != transfer.Path {
				t.Errorf("Path mismatch: got %q, want %q", tr.Path, transfer.Path)
			}
			break
		}
	}

	if !found {
		t.Error("Created transfer not found in list")
	}
}

func TestUpdateTransfer(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()
	transfer := &Transfer{
		Direction: "export",
		Path:      "/path/to/export",
		Status:    "running",
		StartTime: now,
	}

	err := store.CreateTransfer(transfer)
	if err != nil {
		t.Fatalf("CreateTransfer() failed: %v", err)
	}

	originalID := transfer.ID

	// Update the transfer
	transfer.Status = "completed"
	transfer.EndTime = now.Add(time.Hour)

	err = store.UpdateTransfer(transfer)
	if err != nil {
		t.Fatalf("UpdateTransfer() failed: %v", err)
	}

	// Verify changes persisted
	transfers, err := store.ListTransfers(0)
	if err != nil {
		t.Fatalf("ListTransfers() failed: %v", err)
	}

	found := false
	for _, tr := range transfers {
		if tr.ID == originalID {
			found = true
			if tr.Status != "completed" {
				t.Errorf("Status not updated: got %q, want %q", tr.Status, "completed")
			}
			break
		}
	}

	if !found {
		t.Error("Updated transfer not found in list")
	}
}

func TestUpdateTransferNotFound(t *testing.T) {
	store := newTestStore(t)

	transfer := &Transfer{
		ID:        99999,
		Direction: "export",
		Status:    "running",
	}

	err := store.UpdateTransfer(transfer)
	if err == nil {
		t.Error("Expected error when updating non-existent Transfer")
	}
}

func TestListTransfers(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Create several transfers
	for i := 0; i < 5; i++ {
		transfer := &Transfer{
			Direction: "export",
			Path:      "/path/to/export/" + string(rune(i)),
			Status:    "completed",
			StartTime: now.Add(-time.Duration(i) * time.Hour),
		}

		err := store.CreateTransfer(transfer)
		if err != nil {
			t.Fatalf("CreateTransfer() failed: %v", err)
		}
	}

	// List all transfers
	allTransfers, err := store.ListTransfers(0)
	if err != nil {
		t.Fatalf("ListTransfers() failed: %v", err)
	}

	if len(allTransfers) != 5 {
		t.Errorf("Expected 5 transfers, got %d", len(allTransfers))
	}
}

func TestListTransfersOrdering(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Create transfers with different start times
	startTimes := []time.Time{
		now.Add(-1 * time.Hour),
		now,
		now.Add(-2 * time.Hour),
	}

	for _, startTime := range startTimes {
		transfer := &Transfer{
			Direction: "export",
			Path:      "/path/to/export",
			Status:    "completed",
			StartTime: startTime,
		}

		err := store.CreateTransfer(transfer)
		if err != nil {
			t.Fatalf("CreateTransfer() failed: %v", err)
		}
	}

	transfers, err := store.ListTransfers(0)
	if err != nil {
		t.Fatalf("ListTransfers() failed: %v", err)
	}

	// Should be ordered by start_time DESC
	for i := 0; i < len(transfers)-1; i++ {
		if transfers[i].StartTime.Before(transfers[i+1].StartTime) {
			t.Error("Transfers not ordered by start_time DESC")
			break
		}
	}
}

func TestListTransfersWithLimit(t *testing.T) {
	store := newTestStore(t)

	now := time.Now()

	// Create 5 transfers
	for i := 0; i < 5; i++ {
		transfer := &Transfer{
			Direction: "export",
			Path:      "/path/to/export/" + string(rune(i)),
			Status:    "completed",
			StartTime: now.Add(-time.Duration(i) * time.Hour),
		}

		err := store.CreateTransfer(transfer)
		if err != nil {
			t.Fatalf("CreateTransfer() failed: %v", err)
		}
	}

	// List with limit
	transfers, err := store.ListTransfers(2)
	if err != nil {
		t.Fatalf("ListTransfers() with limit failed: %v", err)
	}

	if len(transfers) != 2 {
		t.Errorf("Expected 2 transfers with limit=2, got %d", len(transfers))
	}
}

// ============================================================================
// TransferArchive Operations Tests
// ============================================================================

func TestTransferArchiveCRUD(t *testing.T) {
	s := newTestStore(t)

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
		TransferID:  transfer.ID,
		ArchiveName: "airgap-transfer-001.tar.zst",
		SHA256:      "abc123def456",
		Size:        1024000,
		Validated:   false,
		ValidatedAt: time.Time{},
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

func TestIsArchiveValidated(t *testing.T) {
	s := newTestStore(t)

	// Create a transfer
	transfer := &Transfer{
		Direction: "import",
		Path:      "/mnt/usb",
		Providers: "epel",
		Status:    "completed",
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}
	if err := s.CreateTransfer(transfer); err != nil {
		t.Fatalf("create transfer: %v", err)
	}

	archiveName := "airgap-transfer-001.tar.zst"
	sha256 := "abc123def456"

	// Before validation: should not be validated
	validated, err := s.IsArchiveValidated("/mnt/usb", archiveName, sha256)
	if err != nil {
		t.Fatalf("IsArchiveValidated error: %v", err)
	}
	if validated {
		t.Error("expected archive to not be validated yet")
	}

	// Create and mark archive as validated
	archive := &TransferArchive{
		TransferID:  transfer.ID,
		ArchiveName: archiveName,
		SHA256:      sha256,
		Size:        1024000,
		Validated:   true,
		ValidatedAt: time.Now(),
	}
	if err := s.CreateTransferArchive(archive); err != nil {
		t.Fatalf("create archive: %v", err)
	}

	// After validation: should be validated
	validated, err = s.IsArchiveValidated("/mnt/usb", archiveName, sha256)
	if err != nil {
		t.Fatalf("IsArchiveValidated error: %v", err)
	}
	if !validated {
		t.Error("expected archive to be validated")
	}

	// Wrong sha256: should not be validated
	validated, err = s.IsArchiveValidated("/mnt/usb", archiveName, "wrong-sha256")
	if err != nil {
		t.Fatalf("IsArchiveValidated error: %v", err)
	}
	if validated {
		t.Error("expected archive with wrong sha256 to not be validated")
	}

	// Wrong path: should not be validated
	validated, err = s.IsArchiveValidated("/other/path", archiveName, sha256)
	if err != nil {
		t.Fatalf("IsArchiveValidated error: %v", err)
	}
	if validated {
		t.Error("expected archive with wrong path to not be validated")
	}
}

// ============================================================================
// ProviderConfig Operations Tests
// ============================================================================

func TestProviderConfigTableExists(t *testing.T) {
	s := newTestStore(t)

	// Verify the table exists by inserting a row
	_, err := s.db.Exec(`INSERT INTO provider_configs (name, type, enabled, config_json) VALUES (?, ?, ?, ?)`,
		"test", "epel", 1, "{}")
	if err != nil {
		t.Fatalf("provider_configs table should exist after migration: %v", err)
	}
}

func TestCreateProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{
		Name:       "epel",
		Type:       "epel",
		Enabled:    true,
		ConfigJSON: `{"repos":[{"name":"epel-9","base_url":"https://example.com"}]}`,
	}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatalf("CreateProviderConfig error: %v", err)
	}
	if pc.ID == 0 {
		t.Error("expected non-zero ID after create")
	}
}

func TestCreateProviderConfigDuplicateName(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	dup := &ProviderConfig{Name: "epel", Type: "epel", Enabled: false, ConfigJSON: "{}"}
	err := s.CreateProviderConfig(dup)
	if err == nil {
		t.Fatal("expected error on duplicate name")
	}
}

func TestGetProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: `{"key":"val"}`}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProviderConfig("epel")
	if err != nil {
		t.Fatalf("GetProviderConfig error: %v", err)
	}
	if got.Name != "epel" {
		t.Errorf("name = %q, want %q", got.Name, "epel")
	}
	if got.Type != "epel" {
		t.Errorf("type = %q, want %q", got.Type, "epel")
	}
	if !got.Enabled {
		t.Error("expected enabled = true")
	}
	if got.ConfigJSON != `{"key":"val"}` {
		t.Errorf("config_json = %q, want %q", got.ConfigJSON, `{"key":"val"}`)
	}
}

func TestGetProviderConfigNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetProviderConfig("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestListProviderConfigs(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{"aaa", "zzz", "mmm"} {
		pc := &ProviderConfig{Name: name, Type: "epel", Enabled: true, ConfigJSON: "{}"}
		if err := s.CreateProviderConfig(pc); err != nil {
			t.Fatal(err)
		}
	}

	configs, err := s.ListProviderConfigs()
	if err != nil {
		t.Fatalf("ListProviderConfigs error: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}
	// Should be ordered by name
	if configs[0].Name != "aaa" || configs[1].Name != "mmm" || configs[2].Name != "zzz" {
		t.Errorf("unexpected ordering: %v, %v, %v", configs[0].Name, configs[1].Name, configs[2].Name)
	}
}

func TestUpdateProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: `{"old":true}`}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	pc.Enabled = false
	pc.ConfigJSON = `{"new":true}`
	if err := s.UpdateProviderConfig(pc); err != nil {
		t.Fatalf("UpdateProviderConfig error: %v", err)
	}

	got, err := s.GetProviderConfig("epel")
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Error("expected enabled = false after update")
	}
	if got.ConfigJSON != `{"new":true}` {
		t.Errorf("config_json = %q, want %q", got.ConfigJSON, `{"new":true}`)
	}
}

func TestDeleteProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProviderConfig("epel"); err != nil {
		t.Fatalf("DeleteProviderConfig error: %v", err)
	}

	_, err := s.GetProviderConfig("epel")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteProviderConfigNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeleteProviderConfig("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent delete")
	}
}

func TestToggleProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	// Toggle off
	if err := s.ToggleProviderConfig("epel"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProviderConfig("epel")
	if got.Enabled {
		t.Error("expected enabled = false after toggle")
	}

	// Toggle back on
	if err := s.ToggleProviderConfig("epel"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProviderConfig("epel")
	if !got.Enabled {
		t.Error("expected enabled = true after second toggle")
	}
}

func TestCountProviderConfigs(t *testing.T) {
	s := newTestStore(t)

	count, err := s.CountProviderConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	count, err = s.CountProviderConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestSeedProviderConfigs(t *testing.T) {
	s := newTestStore(t)

	yamlProviders := map[string]map[string]interface{}{
		"epel": {
			"enabled": true,
			"repos":   []interface{}{map[string]interface{}{"name": "epel-9"}},
		},
		"ocp_binaries": {
			"enabled":  true,
			"base_url": "https://mirror.openshift.com",
		},
	}

	if err := s.SeedProviderConfigs(yamlProviders); err != nil {
		t.Fatalf("SeedProviderConfigs error: %v", err)
	}

	configs, _ := s.ListProviderConfigs()
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	// Second call should be a no-op (table not empty)
	if err := s.SeedProviderConfigs(yamlProviders); err != nil {
		t.Fatalf("second SeedProviderConfigs error: %v", err)
	}

	configs, _ = s.ListProviderConfigs()
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs after no-op seed, got %d", len(configs))
	}
}
