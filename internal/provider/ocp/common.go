package ocp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/safety"
)

const maxOCPMetadataBytes int64 = 32 * 1024 * 1024

var providerHTTPClient = safety.NewHTTPClient(60 * time.Second)

func readMetadataBody(r io.Reader) ([]byte, error) {
	data, err := safety.ReadAllWithLimit(r, maxOCPMetadataBytes)
	if err != nil {
		if errors.Is(err, safety.ErrBodyTooLarge) {
			return nil, fmt.Errorf("metadata response exceeded %d bytes: %w", maxOCPMetadataBytes, err)
		}
		return nil, err
	}
	return data, nil
}

func fetchWithStatusOK(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	data, err := readMetadataBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	return data, nil
}

// parseChecksumFile parses a sha256sum.txt file into a map of filename → hash.
// Format: each line is "{hash}  {filename}"
func parseChecksumFile(data []byte) map[string]string {
	files := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			hash := parts[0]
			filename := parts[1]
			files[filename] = hash
		}
	}
	return files
}

// filterFiles removes files matching any of the ignore patterns (case-insensitive).
func filterFiles(files map[string]string, patterns []string) map[string]string {
	filtered := make(map[string]string)
	for filename, hash := range files {
		shouldIgnore := false
		lowerFilename := strings.ToLower(filename)
		for _, pattern := range patterns {
			if strings.Contains(lowerFilename, strings.ToLower(pattern)) {
				shouldIgnore = true
				break
			}
		}
		if !shouldIgnore {
			filtered[filename] = hash
		}
	}
	return filtered
}

// checksumLocalFile computes the SHA256 checksum of a local file.
func checksumLocalFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// buildSyncPlan compares remote manifest against local files and returns a plan.
func buildSyncPlan(providerName, baseURL, version, outputDir, dataDir string, remoteFiles map[string]string, logger *slog.Logger) ([]provider.SyncAction, error) {
	var actions []provider.SyncAction

	outputRoot, err := safety.SafeJoinUnder(dataDir, outputDir)
	if err != nil {
		return nil, fmt.Errorf("invalid output directory %q: %w", outputDir, err)
	}
	versionDir, err := safety.SafeJoinUnder(outputRoot, version)
	if err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", version, err)
	}

	for filename, expectedHash := range remoteFiles {
		localPath, err := safety.SafeJoinUnder(versionDir, filename)
		if err != nil {
			return nil, fmt.Errorf("unsafe remote filename %q: %w", filename, err)
		}
		relPath, err := filepath.Rel(outputRoot, localPath)
		if err != nil {
			return nil, fmt.Errorf("building relative path for %q: %w", filename, err)
		}
		relPath = filepath.ToSlash(relPath)
		downloadURL := fmt.Sprintf("%s/%s/%s", strings.TrimRight(baseURL, "/"), version, filename)

		// Check if local file exists and has matching checksum
		if fileInfo, err := os.Stat(localPath); err == nil {
			actualHash, err := checksumLocalFile(localPath)
			if err != nil {
				logger.Warn("failed to compute checksum for local file",
					slog.String("provider", providerName),
					slog.String("file", filename),
					slog.String("error", err.Error()))
				actions = append(actions, provider.SyncAction{
					Path:      relPath,
					LocalPath: localPath,
					Action:    provider.ActionUpdate,
					Size:      fileInfo.Size(),
					Checksum:  expectedHash,
					Reason:    "checksum verification failed",
					URL:       downloadURL,
				})
				continue
			}

			if actualHash == expectedHash {
				actions = append(actions, provider.SyncAction{
					Path:      relPath,
					LocalPath: localPath,
					Action:    provider.ActionSkip,
					Size:      fileInfo.Size(),
					Checksum:  expectedHash,
					Reason:    "checksum matches",
					URL:       downloadURL,
				})
			} else {
				actions = append(actions, provider.SyncAction{
					Path:      relPath,
					LocalPath: localPath,
					Action:    provider.ActionUpdate,
					Size:      fileInfo.Size(),
					Checksum:  expectedHash,
					Reason:    "checksum mismatch",
					URL:       downloadURL,
				})
			}
		} else if os.IsNotExist(err) {
			actions = append(actions, provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionDownload,
				Size:      0,
				Checksum:  expectedHash,
				Reason:    "new file",
				URL:       downloadURL,
			})
		} else {
			logger.Warn("error checking local file",
				slog.String("provider", providerName),
				slog.String("file", filename),
				slog.String("error", err.Error()))
			actions = append(actions, provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionDownload,
				Size:      0,
				Checksum:  expectedHash,
				Reason:    "error checking file",
				URL:       downloadURL,
			})
		}
	}

	return actions, nil
}
