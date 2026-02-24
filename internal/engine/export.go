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

	"github.com/BadgerOps/airgap/internal/store"
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
		if p, ok := m.registry.Get(provName); ok {
			mp.Type = p.Type()
		}
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
			_ = archiveFile.Close()
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
				_ = tarWriter.Close()
				_ = zstdWriter.Close()
				_ = archiveFile.Close()
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
	defer func() {
		_ = f.Close()
	}()

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
	defer func() {
		_ = f.Close()
	}()

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
		result[i] = ManifestArchive(a)
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
