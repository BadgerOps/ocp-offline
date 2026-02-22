package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ProgressFunc is called periodically to report download progress.
// bytesDownloaded is the number of bytes downloaded so far,
// totalBytes is the total size of the download (or 0 if unknown).
type ProgressFunc func(bytesDownloaded, totalBytes int64)

// DownloadOptions contains configuration for a single download.
type DownloadOptions struct {
	URL              string
	DestPath         string
	ExpectedChecksum string // SHA256 hex string, empty to skip validation
	ExpectedSize     int64  // 0 to skip size check
	RetryCount       int    // 0 defaults to 3
	OnProgress       ProgressFunc
}

// DownloadResult contains the result of a successful download.
type DownloadResult struct {
	Path     string        // Path to the downloaded file
	Size     int64         // Final file size in bytes
	SHA256   string        // SHA256 checksum in hex
	Resumed  bool          // Whether the download was resumed
	Attempts int           // Number of attempts made
	Duration time.Duration // Total download duration
}

// Client performs HTTP downloads with retry logic, resumption, and validation.
type Client struct {
	httpClient *http.Client
	logger     *slog.Logger
	userAgent  string
}

// NewClient creates a new download client with the given logger.
func NewClient(logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
				TLSHandshakeTimeout:  15 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
			},
			// No overall Timeout — body reads can take as long as needed.
			// Context cancellation still works for user-initiated cancel.
		},
		logger:    logger,
		userAgent: "airgap/1.0",
	}
}

// Download downloads a file from the given URL to the destination path.
// It supports resumable downloads, retries with exponential backoff, and checksum validation.
func (c *Client) Download(ctx context.Context, opts DownloadOptions) (*DownloadResult, error) {
	if opts.RetryCount == 0 {
		opts.RetryCount = 3
	}

	startTime := time.Now()
	var lastErr error
	var resumed bool

	for attempt := 1; attempt <= opts.RetryCount; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("download cancelled: %w", ctx.Err())
		default:
		}

		// Check if we have a partial file we can resume from
		fileSize := int64(0)
		if fi, err := os.Stat(opts.DestPath); err == nil {
			existingSize := fi.Size()
			// Only resume if the file is smaller than expected.
			// If it's >= expected size (or expected size is unknown),
			// the file is corrupt/stale — delete and start fresh.
			if opts.ExpectedSize > 0 && existingSize < opts.ExpectedSize {
				fileSize = existingSize
				resumed = true
			} else if existingSize > 0 {
				// File exists but is >= expected size or size unknown — start fresh
				_ = os.Remove(opts.DestPath)
			}
		}

		// Ensure parent directories exist
		if dir := filepath.Dir(opts.DestPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				lastErr = fmt.Errorf("failed to create directory %s: %w", dir, err)
				c.logger.Error("failed to create directory", "path", dir, "error", err)
				continue
			}
		}

		// Create or open the destination file
		flags := os.O_CREATE | os.O_WRONLY
		if fileSize > 0 {
			flags |= os.O_APPEND
		}

		file, err := os.OpenFile(opts.DestPath, flags, 0644)
		if err != nil {
			lastErr = fmt.Errorf("failed to open file: %w", err)
			c.logger.Error("failed to open file", "path", opts.DestPath, "attempt", attempt, "error", err)
			continue
		}

		// Perform the download attempt
		result, err := c.downloadAttempt(ctx, file, opts, fileSize, attempt)
		file.Close()

		if err == nil {
			result.Resumed = resumed && attempt == 1
			result.Attempts = attempt
			result.Duration = time.Since(startTime)
			return result, nil
		}

		lastErr = err
		c.logger.Warn("download attempt failed", "url", opts.URL, "attempt", attempt, "error", err)

		// Don't retry on context cancellation — keep partial file for resume
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		if shouldNotRetry(err) {
			_ = os.Remove(opts.DestPath)
			return nil, err
		}

		// Wait before retrying with exponential backoff + jitter
		if attempt < opts.RetryCount {
			delay := calculateBackoffDelay(attempt)
			c.logger.Debug("retrying download", "url", opts.URL, "delay", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, fmt.Errorf("download cancelled during retry: %w", ctx.Err())
			}
		}
	}

	// Keep partial file for resume on next sync attempt
	return nil, fmt.Errorf("download failed after %d attempts: %w", opts.RetryCount, lastErr)
}

// downloadAttempt performs a single download attempt.
func (c *Client) downloadAttempt(ctx context.Context, file *os.File, opts DownloadOptions, fileSize int64, attempt int) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)

	// Set Range header if we're resuming
	if fileSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", fileSize))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle HTTP status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read error body for logging
		body, _ := io.ReadAll(resp.Body)
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
		}
	}

	// For 206 Partial Content, we're resuming; for 200 OK, we're starting fresh
	if resp.StatusCode == http.StatusPartialContent {
		// Resume is working, keep appending
	} else if resp.StatusCode == http.StatusOK {
		// Server doesn't support ranges, restart from scratch
		if fileSize > 0 {
			_ = file.Truncate(0)
			_, _ = file.Seek(0, io.SeekStart)
			fileSize = 0
		}
	}

	// Get content length from response
	totalSize := resp.ContentLength
	if totalSize > 0 && fileSize > 0 {
		totalSize += fileSize
	}
	if totalSize < 0 {
		totalSize = opts.ExpectedSize
	}

	// Create a progress wrapper around the response body
	reader := resp.Body
	if opts.OnProgress != nil {
		reader = &progressReader{
			reader:   resp.Body,
			callback: opts.OnProgress,
			current:  fileSize,
			total:    totalSize,
		}
	}

	// Write response body to file
	downloadedBytes, err := io.Copy(file, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to write to file: %w", err)
	}

	finalSize := fileSize + downloadedBytes

	// Compute SHA256 of the entire file (not just the new bytes).
	// This is necessary for resumed downloads where only a tail portion
	// was fetched in this attempt.
	sha256Hex, err := hashFile(opts.DestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash file: %w", err)
	}

	// Verify integrity: checksum is authoritative when available.
	// Size-only check is a fallback when no checksum is provided.
	if opts.ExpectedChecksum != "" {
		if sha256Hex != opts.ExpectedChecksum {
			_ = os.Remove(opts.DestPath)
			return nil, fmt.Errorf("checksum mismatch: got %s, expected %s", sha256Hex, opts.ExpectedChecksum)
		}
		// Checksum matches — if size differs the mirror metadata is stale, log but accept
		if opts.ExpectedSize > 0 && finalSize != opts.ExpectedSize {
			c.logger.Warn("size differs from metadata but checksum matches, accepting file",
				"path", opts.DestPath, "got_size", finalSize, "expected_size", opts.ExpectedSize)
		}
	} else if opts.ExpectedSize > 0 && finalSize != opts.ExpectedSize {
		// No checksum to verify — size is our only integrity check
		_ = os.Remove(opts.DestPath)
		return nil, fmt.Errorf("size mismatch: got %d bytes, expected %d", finalSize, opts.ExpectedSize)
	}

	return &DownloadResult{
		Path:     opts.DestPath,
		Size:     finalSize,
		SHA256:   sha256Hex,
		Attempts: attempt,
	}, nil
}

// hashFile computes the SHA256 hex digest of an entire file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// calculateBackoffDelay calculates exponential backoff with jitter.
// Base delay is 1s, doubles each attempt, plus random jitter up to half the delay.
func calculateBackoffDelay(attempt int) time.Duration {
	baseDelay := time.Second
	exponentialDelay := time.Duration(math.Pow(2, float64(attempt-1))) * baseDelay
	maxJitter := exponentialDelay / 2
	jitter := time.Duration(rand.Int63n(int64(maxJitter)))
	return exponentialDelay + jitter
}

// shouldNotRetry returns true if the error should not trigger a retry.
func shouldNotRetry(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		// Don't retry on 4xx errors except 429 (Too Many Requests)
		if httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 && httpErr.StatusCode != 429 {
			return true
		}
	}
	return false
}

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("http error %d: %s", e.StatusCode, e.Status)
}

// progressReader wraps a reader and calls a progress callback as data is read.
type progressReader struct {
	reader   io.Reader
	callback ProgressFunc
	current  int64
	total    int64
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.reader.Read(p)
	if n > 0 {
		pr.current += int64(n)
		if pr.callback != nil {
			pr.callback(pr.current, pr.total)
		}
	}
	return n, err
}

func (pr *progressReader) Close() error {
	if c, ok := pr.reader.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
