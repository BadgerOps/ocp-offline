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
