package ocp

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BadgerOps/airgap/internal/provider"
)

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

	versionDir := filepath.Join(dataDir, outputDir, version)

	for filename, expectedHash := range remoteFiles {
		localPath := filepath.Join(versionDir, filename)
		downloadURL := fmt.Sprintf("%s/%s/%s", strings.TrimRight(baseURL, "/"), version, filename)

		// Check if local file exists and has matching checksum
		if fileInfo, err := os.Stat(localPath); err == nil {
			// File exists, check checksum
			actualHash, err := checksumLocalFile(localPath)
			if err != nil {
				logger.Warn("failed to compute checksum for local file",
					slog.String("provider", providerName),
					slog.String("file", filename),
					slog.String("error", err.Error()))
				// If we can't compute checksum, treat as mismatch
				actions = append(actions, provider.SyncAction{
					Path:     filepath.Join(version, filename),
					Action:   provider.ActionUpdate,
					Size:     fileInfo.Size(),
					Checksum: expectedHash,
					Reason:   "checksum verification failed",
					URL:      downloadURL,
				})
				continue
			}

			if actualHash == expectedHash {
				// File is valid, skip
				actions = append(actions, provider.SyncAction{
					Path:     filepath.Join(version, filename),
					Action:   provider.ActionSkip,
					Size:     fileInfo.Size(),
					Checksum: expectedHash,
					Reason:   "checksum matches",
					URL:      downloadURL,
				})
			} else {
				// Checksum mismatch, update
				actions = append(actions, provider.SyncAction{
					Path:     filepath.Join(version, filename),
					Action:   provider.ActionUpdate,
					Size:     fileInfo.Size(),
					Checksum: expectedHash,
					Reason:   "checksum mismatch",
					URL:      downloadURL,
				})
			}
		} else if os.IsNotExist(err) {
			// File doesn't exist, download
			actions = append(actions, provider.SyncAction{
				Path:     filepath.Join(version, filename),
				Action:   provider.ActionDownload,
				Size:     0, // Size unknown until downloaded
				Checksum: expectedHash,
				Reason:   "new file",
				URL:      downloadURL,
			})
		} else {
			// Error checking file, treat as download needed
			logger.Warn("error checking local file",
				slog.String("provider", providerName),
				slog.String("file", filename),
				slog.String("error", err.Error()))
			actions = append(actions, provider.SyncAction{
				Path:     filepath.Join(version, filename),
				Action:   provider.ActionDownload,
				Size:     0,
				Checksum: expectedHash,
				Reason:   "error checking file",
				URL:      downloadURL,
			})
		}
	}

	return actions, nil
}
