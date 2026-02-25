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
	"testing"
	"time"
)

// newTestClient creates a client with zero-delay backoff for fast tests.
func newTestClient(logger *slog.Logger) *Client {
	c := NewClient(logger)
	c.backoffFunc = func(attempt int) time.Duration { return 0 }
	return c
}

// TestNewClient creates client with logger
func TestNewClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	if client == nil {
		t.Fatal("expected client to be non-nil")
	}
	if client.httpClient == nil {
		t.Fatal("expected httpClient to be initialized")
	}
	if client.userAgent != "airgap/1.0" {
		t.Errorf("expected userAgent to be 'airgap/1.0', got %s", client.userAgent)
	}
	if client.logger == nil {
		t.Fatal("expected logger to be set")
	}
}

// TestDownloadFile sets up httptest server serving a file, download it, verify content and checksum
func TestDownloadFile(t *testing.T) {
	testContent := []byte("This is test file content for download verification")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:      server.URL,
		DestPath: destPath,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected result to be non-nil")
	}

	// Verify file was created
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("expected file to exist, got error: %v", err)
	}

	// Verify content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("content mismatch: expected %s, got %s", string(testContent), string(content))
	}

	// Verify result fields
	if result.Size != int64(len(testContent)) {
		t.Errorf("expected size %d, got %d", len(testContent), result.Size)
	}

	if result.Path != destPath {
		t.Errorf("expected path %s, got %s", destPath, result.Path)
	}

	// Verify checksum
	expectedHash := sha256.Sum256(testContent)
	expectedSHA256 := hex.EncodeToString(expectedHash[:])
	if result.SHA256 != expectedSHA256 {
		t.Errorf("checksum mismatch: expected %s, got %s", expectedSHA256, result.SHA256)
	}
}

func TestDownloadFileWithHeaders(t *testing.T) {
	testContent := []byte("header gated content")
	const authHeader = "Bearer test-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != authHeader {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("missing auth"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "header.bin")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:      server.URL,
		DestPath: destPath,
		Headers: map[string]string{
			"Authorization": authHeader,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result == nil {
		t.Fatal("expected result to be non-nil")
	}
	if result.Size != int64(len(testContent)) {
		t.Fatalf("expected size %d, got %d", len(testContent), result.Size)
	}
}

// TestDownloadFileWithChecksum downloads with expected SHA256, verify it validates
func TestDownloadFileWithChecksum(t *testing.T) {
	testContent := []byte("Content with checksum validation")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	// Calculate the correct checksum
	hash := sha256.New()
	hash.Write(testContent)
	expectedChecksum := hex.EncodeToString(hash.Sum(nil))

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_checksum.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:              server.URL,
		DestPath:         destPath,
		ExpectedChecksum: expectedChecksum,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected result to be non-nil")
	}

	if result.SHA256 != expectedChecksum {
		t.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, result.SHA256)
	}

	// Verify file exists
	if _, err := os.Stat(destPath); err != nil {
		t.Fatalf("expected file to exist, got error: %v", err)
	}
}

// TestDownloadFileChecksumMismatch downloads with wrong expected checksum, should fail
func TestDownloadFileChecksumMismatch(t *testing.T) {
	testContent := []byte("Original file content")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_bad_checksum.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	wrongChecksum := "0000000000000000000000000000000000000000000000000000000000000000"

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:              server.URL,
		DestPath:         destPath,
		ExpectedChecksum: wrongChecksum,
	})

	if err == nil {
		t.Fatal("expected error due to checksum mismatch")
	}

	if result != nil {
		t.Fatal("expected result to be nil on error")
	}

	// Verify file was cleaned up
	if _, err := os.Stat(destPath); err == nil {
		t.Fatal("expected file to be removed on checksum mismatch")
	}
}

// TestDownloadFileNotFound httptest returns 404, should error
func TestDownloadFileNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("File not found"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_404.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:      server.URL,
		DestPath: destPath,
	})

	if err == nil {
		t.Fatal("expected error for 404 status")
	}

	if result != nil {
		t.Fatal("expected result to be nil on error")
	}

	// Verify file was cleaned up
	if _, err := os.Stat(destPath); err == nil {
		t.Fatal("expected file to be removed on error")
	}
}

// TestDownloadFileServerError httptest returns 500, should error
func TestDownloadFileServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Server error"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_500.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:      server.URL,
		DestPath: destPath,
	})

	if err == nil {
		t.Fatal("expected error for 500 status")
	}

	if result != nil {
		t.Fatal("expected result to be nil on error")
	}
}

// TestDownloadFileRetry httptest that fails first N requests then succeeds, verify retry works
func TestDownloadFileRetry(t *testing.T) {
	testContent := []byte("Content after retries")
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 3 {
			// Fail first 2 requests
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("Service unavailable"))
			return
		}
		// Succeed on 3rd request
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_retry.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:        server.URL,
		DestPath:   destPath,
		RetryCount: 5, // Allow more retries
	})

	if err != nil {
		t.Fatalf("expected no error after retries, got %v", err)
	}

	if result == nil {
		t.Fatal("expected result to be non-nil")
	}

	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}

	// Verify content
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}

	if string(content) != string(testContent) {
		t.Errorf("content mismatch: expected %s, got %s", string(testContent), string(content))
	}
}

// TestDownloadFileResume httptest that supports Range headers, verify resume works
func TestDownloadFileResume(t *testing.T) {
	fullContent := []byte("This is the complete file content for resume testing")
	partialContent := fullContent[:20] // First 20 bytes

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := r.Header.Get("Range")

		if rangeHeader != "" {
			// Resume request with Range header
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 20-%d/%d", len(fullContent)-1, len(fullContent)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(fullContent[20:])
		} else {
			// Full download
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fullContent)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_resume.bin")

	// Pre-create a partial file
	err := os.WriteFile(destPath, partialContent, 0644)
	if err != nil {
		t.Fatalf("failed to create partial file: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:      server.URL,
		DestPath: destPath,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected result to be non-nil")
	}

	// Verify file content is complete
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}

	if string(content) != string(fullContent) {
		t.Errorf("content mismatch: expected %s, got %s", string(fullContent), string(content))
	}

	if result.Size != int64(len(fullContent)) {
		t.Errorf("expected size %d, got %d", len(fullContent), result.Size)
	}
}

// TestDownloadFileTimeout httptest with delayed response, verify timeout handling
func TestDownloadFileTimeout(t *testing.T) {
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until either request or server context is done, so
		// server.Close() doesn't wait for the full sleep duration.
		select {
		case <-r.Context().Done():
		case <-serverCtx.Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_timeout.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := client.Download(ctx, DownloadOptions{
		URL:        server.URL,
		DestPath:   destPath,
		RetryCount: 1,
	})

	if err == nil {
		t.Fatal("expected error due to timeout")
	}

	if result != nil {
		t.Fatal("expected result to be nil on timeout")
	}

	// Signal server handler to stop so server.Close() returns quickly.
	serverCancel()
}

// TestDownloadFileContextCancellation verifies that context cancellation stops the download
func TestDownloadFileContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send chunks slowly; stop when the request context is cancelled
		// so server.Close() returns promptly.
		for i := 0; i < 50; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
				_, _ = w.Write([]byte("chunk"))
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_cancel.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := client.Download(ctx, DownloadOptions{
		URL:      server.URL,
		DestPath: destPath,
	})

	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}

	if result != nil {
		t.Fatal("expected result to be nil on cancellation")
	}
}

// TestDownloadFileProgress verifies progress callback is called
func TestDownloadFileProgress(t *testing.T) {
	testContent := []byte("Content for progress tracking")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(testContent)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_progress.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	progressCallCount := 0
	onProgress := func(bytesDownloaded, totalBytes int64) {
		progressCallCount++
	}

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:        server.URL,
		DestPath:   destPath,
		OnProgress: onProgress,
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected result to be non-nil")
	}

	if progressCallCount == 0 {
		t.Fatal("expected progress callback to be called at least once")
	}
}

// TestDownloadFileSizeValidation verifies file size validation works
func TestDownloadFileSizeValidation(t *testing.T) {
	testContent := []byte("Content for size validation")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(testContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "testfile_size.bin")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := newTestClient(logger)

	// Expect a different size
	expectedSize := int64(len(testContent) + 100)

	result, err := client.Download(context.Background(), DownloadOptions{
		URL:          server.URL,
		DestPath:     destPath,
		ExpectedSize: expectedSize,
	})

	if err == nil {
		t.Fatal("expected error due to size mismatch")
	}

	if result != nil {
		t.Fatal("expected result to be nil on size mismatch")
	}

	// Verify file was cleaned up
	if _, err := os.Stat(destPath); err == nil {
		t.Fatal("expected file to be removed on size mismatch")
	}
}

// TestDownloadFileHTTPError verifies HTTPError type works correctly
func TestDownloadFileHTTPError(t *testing.T) {
	httpErr := &HTTPError{
		StatusCode: 403,
		Status:     "Forbidden",
		Body:       "Access denied",
	}

	expectedMsg := "http error 403: Forbidden"
	if httpErr.Error() != expectedMsg {
		t.Errorf("expected error message %s, got %s", expectedMsg, httpErr.Error())
	}
}
