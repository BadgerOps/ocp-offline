package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewPool creates pool with given workers
func TestNewPool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	pool := NewPool(client, 5, logger)

	if pool == nil {
		t.Fatal("expected pool to be non-nil")
	}
	if pool.client != client {
		t.Fatal("expected pool client to match")
	}
	if pool.workers != 5 {
		t.Errorf("expected 5 workers, got %d", pool.workers)
	}
	if pool.logger != logger {
		t.Fatal("expected pool logger to match")
	}
}

// TestNewPoolDefaultWorkers verifies pool defaults to 1 worker if workers <= 0
func TestNewPoolDefaultWorkers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	pool := NewPool(client, 0, logger)

	if pool.workers != 1 {
		t.Errorf("expected 1 worker (default), got %d", pool.workers)
	}

	pool2 := NewPool(client, -5, logger)

	if pool2.workers != 1 {
		t.Errorf("expected 1 worker (default), got %d", pool2.workers)
	}
}

// TestPoolExecute submits multiple download jobs to httptest server, verify all complete
func TestPoolExecute(t *testing.T) {
	testFiles := map[string][]byte{
		"file1.bin": []byte("Content of file 1"),
		"file2.bin": []byte("Content of file 2"),
		"file3.bin": []byte("Content of file 3"),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filename := r.URL.Query().Get("file")
		content, exists := testFiles[filename]
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 3, logger)

	jobs := []Job{
		{
			URL:      fmt.Sprintf("%s?file=file1.bin", server.URL),
			DestPath: filepath.Join(tmpDir, "file1.bin"),
		},
		{
			URL:      fmt.Sprintf("%s?file=file2.bin", server.URL),
			DestPath: filepath.Join(tmpDir, "file2.bin"),
		},
		{
			URL:      fmt.Sprintf("%s?file=file3.bin", server.URL),
			DestPath: filepath.Join(tmpDir, "file3.bin"),
		},
	}

	results := pool.Execute(context.Background(), jobs)

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	// Verify all succeeded
	for i, result := range results {
		if !result.Success {
			t.Errorf("result %d failed: %v", i, result.Error)
		}
		if result.Job.URL != jobs[i].URL || result.Job.DestPath != jobs[i].DestPath ||
			result.Job.ExpectedChecksum != jobs[i].ExpectedChecksum || result.Job.ExpectedSize != jobs[i].ExpectedSize {
			t.Errorf("result %d job mismatch", i)
		}
	}

	// Verify files were created with correct content
	for filename, expectedContent := range testFiles {
		destPath := filepath.Join(tmpDir, filename)
		content, err := os.ReadFile(destPath)
		if err != nil {
			t.Errorf("failed to read %s: %v", filename, err)
			continue
		}
		if string(content) != string(expectedContent) {
			t.Errorf("%s content mismatch", filename)
		}
	}
}

// TestPoolConcurrency verifies concurrent downloads actually happen
func TestPoolConcurrency(t *testing.T) {
	activeDownloads := int32(0)
	maxConcurrent := int32(0)
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&activeDownloads, 1)
		defer atomic.AddInt32(&activeDownloads, -1)

		// Track max concurrent
		mu.Lock()
		if current > maxConcurrent {
			maxConcurrent = current
		}
		mu.Unlock()

		// Simulate some work
		time.Sleep(20 * time.Millisecond)

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("download content"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	// Create pool with 4 workers
	pool := NewPool(client, 4, logger)

	// Create 10 jobs
	jobs := make([]Job, 10)
	for i := 0; i < 10; i++ {
		jobs[i] = Job{
			URL:      server.URL,
			DestPath: filepath.Join(tmpDir, fmt.Sprintf("file%d.bin", i)),
		}
	}

	results := pool.Execute(context.Background(), jobs)

	if len(results) != 10 {
		t.Errorf("expected 10 results, got %d", len(results))
	}

	// Verify concurrency was actually used
	if maxConcurrent < 2 {
		t.Errorf("expected max concurrent downloads >= 2, got %d", maxConcurrent)
	}
	if maxConcurrent > 4 {
		t.Errorf("expected max concurrent downloads <= 4 (workers), got %d", maxConcurrent)
	}
}

// TestPoolWithFailures some jobs fail, verify results contain both successes and failures
func TestPoolWithFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail requests to /fail paths with 403 (non-retryable)
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("forbidden"))
			return
		}

		// Succeed on /ok paths
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 2, logger)

	jobs := make([]Job, 6)
	for i := 0; i < 6; i++ {
		path := "/ok"
		if i%2 == 0 {
			path = "/fail"
		}
		jobs[i] = Job{
			URL:      server.URL + path,
			DestPath: filepath.Join(tmpDir, fmt.Sprintf("file%d.bin", i)),
		}
	}

	results := pool.Execute(context.Background(), jobs)

	if len(results) != 6 {
		t.Errorf("expected 6 results, got %d", len(results))
	}

	successCount := 0
	failureCount := 0

	for _, result := range results {
		if result.Success {
			successCount++
			if result.Error != nil {
				t.Errorf("successful result should not have error")
			}
		} else {
			failureCount++
			if result.Error == nil {
				t.Errorf("failed result should have error")
			}
		}
	}

	if successCount == 0 {
		t.Fatal("expected at least some successes")
	}
	if failureCount == 0 {
		t.Fatal("expected at least some failures")
	}

	t.Logf("Results: %d successes, %d failures", successCount, failureCount)
}

// TestPoolContextCancellation cancel context mid-execution, verify pool stops
func TestPoolContextCancellation(t *testing.T) {
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send chunks slowly; respect request context so server.Close() is fast.
		for i := 0; i < 50; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
				_, _ = w.Write([]byte("chunk"))
			}
		}
	}))
	defer slowServer.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 2, logger)

	// Create multiple jobs
	jobs := make([]Job, 10)
	for i := 0; i < 10; i++ {
		jobs[i] = Job{
			URL:      slowServer.URL,
			DestPath: filepath.Join(tmpDir, fmt.Sprintf("file%d.bin", i)),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	results := pool.Execute(ctx, jobs)

	// Should have some results
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// Not all should succeed due to cancellation
	failureCount := 0
	for _, result := range results {
		if !result.Success {
			failureCount++
		}
	}

	if failureCount == 0 {
		t.Fatal("expected some failures due to context cancellation")
	}
}

// TestPoolEmptyJobs verifies pool handles empty job list
func TestPoolEmptyJobs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 3, logger)

	results := pool.Execute(context.Background(), []Job{})

	if len(results) != 0 {
		t.Errorf("expected 0 results for empty jobs, got %d", len(results))
	}
}

// TestPoolResultOrder verifies results maintain job order
func TestPoolResultOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 5, logger) // More workers than jobs

	// Create jobs with identifiable URLs
	jobs := make([]Job, 5)
	for i := 0; i < 5; i++ {
		jobs[i] = Job{
			URL:      fmt.Sprintf("%s?id=%d", server.URL, i),
			DestPath: filepath.Join(tmpDir, fmt.Sprintf("file%d.bin", i)),
		}
	}

	results := pool.Execute(context.Background(), jobs)

	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}

	// Verify order matches input
	for i, result := range results {
		if result.Job.URL != jobs[i].URL {
			t.Errorf("result %d job URL mismatch: expected %s, got %s", i, jobs[i].URL, result.Job.URL)
		}
	}
}

// TestPoolWithChecksumValidation verifies checksum validation in pool jobs
func TestPoolWithChecksumValidation(t *testing.T) {
	fileContents := map[string][]byte{
		"valid.bin": []byte("This is valid content"),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := r.URL.Query().Get("file")
		content, exists := fileContents[file]
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 2, logger)

	// Calculate correct checksum
	hash := sha256.New()
	hash.Write(fileContents["valid.bin"])
	validChecksum := hex.EncodeToString(hash.Sum(nil))
	invalidChecksum := "0000000000000000000000000000000000000000000000000000000000000000"

	jobs := []Job{
		{
			URL:              fmt.Sprintf("%s?file=valid.bin", server.URL),
			DestPath:         filepath.Join(tmpDir, "valid.bin"),
			ExpectedChecksum: validChecksum,
		},
		{
			URL:              fmt.Sprintf("%s?file=valid.bin", server.URL),
			DestPath:         filepath.Join(tmpDir, "invalid.bin"),
			ExpectedChecksum: invalidChecksum,
		},
	}

	results := pool.Execute(context.Background(), jobs)

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// First should succeed
	if !results[0].Success {
		t.Errorf("first result should succeed: %v", results[0].Error)
	}

	// Second should fail
	if results[1].Success {
		t.Errorf("second result should fail due to checksum mismatch")
	}
}

// TestPoolSingleWorker verifies pool works with single worker
func TestPoolSingleWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)
	pool := NewPool(client, 1, logger)

	jobs := make([]Job, 3)
	for i := 0; i < 3; i++ {
		jobs[i] = Job{
			URL:      server.URL,
			DestPath: filepath.Join(tmpDir, fmt.Sprintf("file%d.bin", i)),
		}
	}

	results := pool.Execute(context.Background(), jobs)

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	for i, result := range results {
		if !result.Success {
			t.Errorf("result %d failed: %v", i, result.Error)
		}
	}
}
