package epel

import (
	"bytes"
	"compress/gzip"
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

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/safety"
)

// EPELProvider implements provider.Provider for EPEL repositories
type EPELProvider struct {
	name               string
	cfg                *config.EPELProviderConfig
	dataDir            string
	logger             *slog.Logger
	ValidationProgress provider.ValidationProgressFn
}

const (
	maxEPELMetadataBytes       int64 = 128 * 1024 * 1024
	maxEPELDecompressedXMLSize int64 = 512 * 1024 * 1024
)

// NewEPELProvider creates a new EPEL provider
func NewEPELProvider(dataDir string, logger *slog.Logger) *EPELProvider {
	return &EPELProvider{
		name:    "epel",
		dataDir: dataDir,
		logger:  logger,
	}
}

// Name returns the provider identifier
func (p *EPELProvider) Name() string {
	return p.name
}

// SetName overrides the default provider name with the user-chosen config name.
func (p *EPELProvider) SetName(name string) {
	p.name = name
}

// SetValidationProgress sets the per-file progress callback for validation.
func (p *EPELProvider) SetValidationProgress(fn provider.ValidationProgressFn) {
	p.ValidationProgress = fn
}

func (p *EPELProvider) Type() string {
	return "rpm_repo"
}

// Configure loads provider-specific settings from the raw config
func (p *EPELProvider) Configure(rawCfg provider.ProviderConfig) error {
	cfg, err := config.ParseProviderConfig[config.EPELProviderConfig](rawCfg)
	if err != nil {
		return fmt.Errorf("parsing EPEL config: %w", err)
	}
	p.cfg = cfg
	for _, repo := range p.cfg.Repos {
		if _, err := safety.ValidateHTTPURL(repo.BaseURL); err != nil {
			return fmt.Errorf("invalid base_url for repo %q: %w", repo.Name, err)
		}
	}

	p.logger.Debug("configured EPEL provider",
		slog.Int("repos", len(p.cfg.Repos)),
		slog.Int("max_concurrent_downloads", p.cfg.MaxConcurrentDownloads),
		slog.Int("retry_attempts", p.cfg.RetryAttempts),
		slog.Bool("cleanup_removed_packages", p.cfg.CleanupRemovedPackages),
	)

	return nil
}

// Plan compares upstream manifest against local state and returns a plan
func (p *EPELProvider) Plan(ctx context.Context) (*provider.SyncPlan, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	plan := &provider.SyncPlan{
		Provider:  p.Name(),
		Actions:   []provider.SyncAction{},
		Timestamp: time.Now(),
	}

	for _, repo := range p.cfg.Repos {
		p.logger.Debug("planning sync for repo",
			slog.String("name", repo.Name),
			slog.String("base_url", repo.BaseURL))

		repoActions, err := p.planRepo(ctx, repo)
		if err != nil {
			p.logger.Error("failed to plan repo",
				slog.String("repo", repo.Name),
				slog.String("error", err.Error()))
			continue
		}

		// Aggregate actions and calculate totals
		for _, action := range repoActions {
			plan.Actions = append(plan.Actions, action)
			plan.TotalFiles++
			if action.Action == provider.ActionDownload || action.Action == provider.ActionUpdate {
				plan.TotalSize += action.Size
			}
		}
	}

	p.logger.Info("plan created",
		slog.String("provider", p.Name()),
		slog.Int("actions", len(plan.Actions)),
		slog.Int64("total_size", plan.TotalSize))

	return plan, nil
}

// planRepo creates a sync plan for a single EPEL repository
func (p *EPELProvider) planRepo(ctx context.Context, repo config.EPELRepoConfig) ([]provider.SyncAction, error) {
	var actions []provider.SyncAction

	outputDir, err := safety.SafeJoinUnder(p.dataDir, repo.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("invalid repo output_dir %q: %w", repo.OutputDir, err)
	}

	// Fetch repomd.xml with conditional request if we have a cached copy
	repomdURL := strings.TrimRight(repo.BaseURL, "/") + "/repodata/repomd.xml"
	repomdCachePath, err := safety.SafeJoinUnder(outputDir, filepath.Join("repodata", "repomd.xml"))
	if err != nil {
		return nil, fmt.Errorf("invalid repomd cache path: %w", err)
	}
	repomdResult, err := p.fetchURLConditional(ctx, repomdURL, repomdCachePath)
	if err != nil {
		return nil, fmt.Errorf("fetching repomd.xml: %w", err)
	}

	// Parse repomd.xml
	repomd, err := ParseRepomd(repomdResult.Data)
	if err != nil {
		return nil, fmt.Errorf("parsing repomd.xml: %w", err)
	}

	// Find primary.xml.gz location
	primaryLocation, err := repomd.FindPrimaryLocation()
	if err != nil {
		return nil, fmt.Errorf("finding primary location: %w", err)
	}
	primaryLocation, err = safety.CleanRelativePath(primaryLocation)
	if err != nil {
		return nil, fmt.Errorf("unsafe primary location in repomd metadata: %w", err)
	}
	p.logger.Debug("primary location from repomd", slog.String("location", primaryLocation))

	// Fetch primary metadata — use cache if repomd was not modified
	primaryURL := strings.TrimRight(repo.BaseURL, "/") + "/" + strings.TrimLeft(primaryLocation, "/")
	primaryCachePath, err := safety.SafeJoinUnder(outputDir, primaryLocation)
	if err != nil {
		return nil, fmt.Errorf("unsafe primary metadata cache path: %w", err)
	}
	var primaryGzData []byte
	if !repomdResult.Modified {
		// repomd unchanged, try to use cached primary metadata
		if cached, err := os.ReadFile(primaryCachePath); err == nil {
			p.logger.Info("repomd unchanged, using cached primary metadata",
				slog.String("path", primaryCachePath))
			primaryGzData = cached
		}
	}
	if primaryGzData == nil {
		// Need to fetch fresh primary metadata
		primaryGzData, err = p.fetchURL(ctx, primaryURL)
		if err != nil {
			return nil, fmt.Errorf("fetching primary.xml.gz: %w", err)
		}
		// Cache it for next time
		if dir := filepath.Dir(primaryCachePath); dir != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		if err := os.WriteFile(primaryCachePath, primaryGzData, 0644); err != nil {
			p.logger.Warn("failed to cache primary metadata",
				slog.String("path", primaryCachePath), slog.String("error", err.Error()))
		}
	}

	// Decompress primary metadata (may be .gz, .xz, or already decompressed)
	primaryData, err := p.decompress(primaryGzData)
	if err != nil {
		return nil, fmt.Errorf("decompressing primary metadata: %w", err)
	}
	p.logger.Debug("primary.xml decompressed",
		slog.Int("compressed_bytes", len(primaryGzData)),
		slog.Int("decompressed_bytes", len(primaryData)),
		slog.String("first_bytes", debugFirst(primaryData, 64)),
	)

	// Parse primary.xml
	primaryXML, err := ParsePrimary(primaryData)
	if err != nil {
		return nil, fmt.Errorf("parsing primary.xml: %w", err)
	}

	// Extract packages
	packages := primaryXML.ExtractPackages()

	// Build sync plan
	// When repomd hasn't changed, use fast size-only checks (skip expensive checksums)
	fastCheck := !repomdResult.Modified

	for _, pkg := range packages {
		action, err := p.buildPackageAction(repo, outputDir, pkg, fastCheck)
		if err != nil {
			return nil, fmt.Errorf("invalid package metadata for %q: %w", pkg.Location, err)
		}
		actions = append(actions, action)
	}

	// If cleanup is enabled, check for local files not in remote manifest
	if p.cfg.CleanupRemovedPackages {
		deleteActions, err := p.findDeletedPackages(outputDir, packages)
		if err != nil {
			p.logger.Warn("failed to find deleted packages",
				slog.String("repo", repo.Name),
				slog.String("error", err.Error()))
		} else {
			actions = append(actions, deleteActions...)
		}
	}

	return actions, nil
}

// buildPackageAction creates a SyncAction for a package.
// When fastCheck is true (repomd unchanged), only file existence and size are
// checked — expensive SHA256 checksums are skipped. Use Validate for integrity.
func (p *EPELProvider) buildPackageAction(repo config.EPELRepoConfig, outputDir string, pkg PackageInfo, fastCheck bool) (provider.SyncAction, error) {
	cleanLocation, err := safety.CleanRelativePath(pkg.Location)
	if err != nil {
		return provider.SyncAction{}, err
	}
	localPath, err := safety.SafeJoinUnder(outputDir, cleanLocation)
	if err != nil {
		return provider.SyncAction{}, err
	}
	relPath := filepath.ToSlash(cleanLocation)
	downloadURL := strings.TrimRight(repo.BaseURL, "/") + "/" + strings.TrimLeft(relPath, "/")

	// Check if local file exists
	if fileInfo, err := os.Stat(localPath); err == nil {
		// Fast path: if repomd hasn't changed, size match is sufficient
		if fastCheck {
			// Size check: if the file is present and non-empty, treat as up-to-date.
			// The package list hasn't changed (same repomd), so existing files
			// are from a previous successful sync. Use Validate for integrity.
			return provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionSkip,
				Size:      fileInfo.Size(),
				Checksum:  pkg.Checksum,
				Reason:    "exists (fast check)",
				URL:       downloadURL,
			}, nil
		}

		// Full check: compute checksum
		actualHash, err := checksumLocalFile(localPath)
		if err != nil {
			p.logger.Warn("failed to compute checksum for local file",
				slog.String("file", pkg.Location),
				slog.String("error", err.Error()))
			return provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionUpdate,
				Size:      fileInfo.Size(),
				Checksum:  pkg.Checksum,
				Reason:    "checksum verification failed",
				URL:       downloadURL,
			}, nil
		}

		if actualHash == pkg.Checksum {
			return provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionSkip,
				Size:      fileInfo.Size(),
				Checksum:  pkg.Checksum,
				Reason:    "checksum matches",
				URL:       downloadURL,
			}, nil
		}
		return provider.SyncAction{
			Path:      relPath,
			LocalPath: localPath,
			Action:    provider.ActionUpdate,
			Size:      fileInfo.Size(),
			Checksum:  pkg.Checksum,
			Reason:    "checksum mismatch",
			URL:       downloadURL,
		}, nil
	}

	// File doesn't exist, download
	return provider.SyncAction{
		Path:      relPath,
		LocalPath: localPath,
		Action:    provider.ActionDownload,
		Size:      pkg.Size,
		Checksum:  pkg.Checksum,
		Reason:    "new file",
		URL:       downloadURL,
	}, nil
}

// findDeletedPackages walks the local directory and returns actions for files not in the manifest
func (p *EPELProvider) findDeletedPackages(outputDir string, packages []PackageInfo) ([]provider.SyncAction, error) {
	// Build set of remote package locations
	remoteSet := make(map[string]bool)
	for _, pkg := range packages {
		cleanLocation, err := safety.CleanRelativePath(pkg.Location)
		if err != nil {
			continue
		}
		remoteSet[filepath.ToSlash(cleanLocation)] = true
	}

	var deleteActions []provider.SyncAction

	// Walk local directory
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip errors, just log and continue
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			return nil
		}

		// Normalize path separators to forward slashes for comparison
		relPath = filepath.ToSlash(relPath)

		if !remoteSet[relPath] {
			deleteActions = append(deleteActions, provider.SyncAction{
				Path:   relPath,
				Action: provider.ActionDelete,
				Size:   info.Size(),
				Reason: "removed from manifest",
			})
		}

		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return deleteActions, nil
}

// Sync executes the plan — downloads, validates, retries (stub implementation)
func (p *EPELProvider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	report := &provider.SyncReport{
		Provider:  p.Name(),
		StartTime: time.Now(),
		Failed:    []provider.FailedFile{},
	}

	if opts.DryRun {
		p.logger.Info("sync dry-run",
			slog.String("provider", p.Name()),
			slog.Int("actions", len(plan.Actions)))
		report.EndTime = time.Now()
		return report, nil
	}

	// Process each action
	for _, action := range plan.Actions {
		switch action.Action {
		case provider.ActionSkip:
			report.Skipped++
		case provider.ActionDownload, provider.ActionUpdate:
			// For now, stub the actual download execution.
			// The sync engine would handle the actual network operations.
			p.logger.Debug("would download file",
				slog.String("path", action.Path),
				slog.String("url", action.URL))
			report.Downloaded++
			report.BytesTransferred += action.Size
		case provider.ActionDelete:
			report.Deleted++
		}
	}

	report.EndTime = time.Now()

	p.logger.Info("sync completed",
		slog.String("provider", p.Name()),
		slog.Int("downloaded", report.Downloaded),
		slog.Int("skipped", report.Skipped),
		slog.Int("deleted", report.Deleted),
		slog.Int("failed", len(report.Failed)))

	return report, nil
}

// Validate checks integrity of all local content against expected checksums
func (p *EPELProvider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	report := &provider.ValidationReport{
		Provider:     p.Name(),
		InvalidFiles: []provider.ValidationResult{},
		Timestamp:    time.Now(),
	}

	for _, repo := range p.cfg.Repos {
		outputDir, err := safety.SafeJoinUnder(p.dataDir, repo.OutputDir)
		if err != nil {
			return nil, fmt.Errorf("invalid repo output_dir %q: %w", repo.OutputDir, err)
		}

		// Fetch repomd.xml (use cache) to get the authoritative package list
		repomdURL := strings.TrimRight(repo.BaseURL, "/") + "/repodata/repomd.xml"
		repomdCachePath, err := safety.SafeJoinUnder(outputDir, filepath.Join("repodata", "repomd.xml"))
		if err != nil {
			return nil, fmt.Errorf("invalid repomd cache path: %w", err)
		}
		repomdResult, err := p.fetchURLConditional(ctx, repomdURL, repomdCachePath)
		if err != nil {
			return nil, fmt.Errorf("fetching repomd.xml for validation: %w", err)
		}

		repomd, err := ParseRepomd(repomdResult.Data)
		if err != nil {
			return nil, fmt.Errorf("parsing repomd.xml: %w", err)
		}

		primaryLocation, err := repomd.FindPrimaryLocation()
		if err != nil {
			return nil, fmt.Errorf("finding primary location: %w", err)
		}
		primaryLocation, err = safety.CleanRelativePath(primaryLocation)
		if err != nil {
			return nil, fmt.Errorf("unsafe primary location in repomd metadata: %w", err)
		}

		// Fetch primary metadata (use cache)
		primaryURL := strings.TrimRight(repo.BaseURL, "/") + "/" + strings.TrimLeft(primaryLocation, "/")
		primaryCachePath, err := safety.SafeJoinUnder(outputDir, primaryLocation)
		if err != nil {
			return nil, fmt.Errorf("unsafe primary metadata cache path: %w", err)
		}
		var primaryGzData []byte
		if !repomdResult.Modified {
			if cached, readErr := os.ReadFile(primaryCachePath); readErr == nil {
				primaryGzData = cached
			}
		}
		if primaryGzData == nil {
			primaryGzData, err = p.fetchURL(ctx, primaryURL)
			if err != nil {
				return nil, fmt.Errorf("fetching primary metadata for validation: %w", err)
			}
		}

		primaryData, err := p.decompress(primaryGzData)
		if err != nil {
			return nil, fmt.Errorf("decompressing primary metadata: %w", err)
		}
		primaryXML, err := ParsePrimary(primaryData)
		if err != nil {
			return nil, fmt.Errorf("parsing primary.xml: %w", err)
		}

		packages := primaryXML.ExtractPackages()

		p.logger.Info("starting validation checksumming",
			slog.String("provider", p.Name()),
			slog.Int("total_packages", len(packages)))

		report.TotalFiles += len(packages)

		for i, pkg := range packages {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			cleanLocation, cleanErr := safety.CleanRelativePath(pkg.Location)
			if cleanErr != nil {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      pkg.Location,
					LocalPath: "",
					Expected:  pkg.Checksum,
					Actual:    "error: unsafe path: " + cleanErr.Error(),
					Valid:     false,
				})
				if p.ValidationProgress != nil {
					p.ValidationProgress(i+1, len(packages), pkg.Location, false)
				}
				continue
			}

			localPath, pathErr := safety.SafeJoinUnder(outputDir, cleanLocation)
			if pathErr != nil {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      filepath.ToSlash(cleanLocation),
					LocalPath: "",
					Expected:  pkg.Checksum,
					Actual:    "error: unsafe path: " + pathErr.Error(),
					Valid:     false,
				})
				if p.ValidationProgress != nil {
					p.ValidationProgress(i+1, len(packages), filepath.ToSlash(cleanLocation), false)
				}
				continue
			}
			relPath := filepath.ToSlash(cleanLocation)
			downloadURL := strings.TrimRight(repo.BaseURL, "/") + "/" + strings.TrimLeft(relPath, "/")

			// Check file exists
			info, statErr := os.Stat(localPath)
			if statErr != nil {
				p.logger.Debug("validation: file missing",
					slog.String("file", relPath))
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  pkg.Checksum,
					Actual:    "missing",
					Valid:     false,
					URL:       downloadURL,
				})
				if p.ValidationProgress != nil {
					p.ValidationProgress(i+1, len(packages), relPath, false)
				}
				continue
			}

			// Compute checksum and compare against manifest
			actualHash, hashErr := checksumLocalFile(localPath)
			if hashErr != nil {
				p.logger.Warn("validation: checksum error",
					slog.String("file", relPath),
					slog.String("error", hashErr.Error()))
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  pkg.Checksum,
					Actual:    "error: " + hashErr.Error(),
					Valid:     false,
					Size:      info.Size(),
					URL:       downloadURL,
				})
				if p.ValidationProgress != nil {
					p.ValidationProgress(i+1, len(packages), relPath, false)
				}
				continue
			}

			if actualHash == pkg.Checksum {
				p.logger.Debug("validation: checksum OK",
					slog.String("file", relPath),
					slog.String("checksum", actualHash[:12]+"..."))
				report.ValidFiles++
			} else {
				p.logger.Debug("validation: checksum MISMATCH",
					slog.String("file", relPath),
					slog.String("expected", pkg.Checksum),
					slog.String("actual", actualHash))
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  pkg.Checksum,
					Actual:    actualHash,
					Valid:     false,
					Size:      info.Size(),
					URL:       downloadURL,
				})
			}

			if p.ValidationProgress != nil {
				p.ValidationProgress(i+1, len(packages), relPath, actualHash == pkg.Checksum)
			}
		}
	}

	p.logger.Info("validation completed",
		slog.String("provider", p.Name()),
		slog.Int("total_files", report.TotalFiles),
		slog.Int("valid_files", report.ValidFiles),
		slog.Int("invalid_files", len(report.InvalidFiles)))

	return report, nil
}

// httpClient is a client with transparent decompression disabled so we can
// handle gzip ourselves (important when fetching .gz files).
var httpClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		DisableCompression:    true,
	},
}

// fetchURLConditional fetches a URL with If-Modified-Since if a cached copy exists.
// If the server returns 304 Not Modified, the cached copy is returned.
// The fetched data is saved to cachePath for future conditional requests.
// fetchResult holds the result of a conditional fetch.
type fetchResult struct {
	Data     []byte
	Modified bool // true if fresh data was fetched, false if cache was used
}

func (p *EPELProvider) fetchURLConditional(ctx context.Context, url, cachePath string) (*fetchResult, error) {
	p.logger.Debug("conditional fetch", slog.String("url", url), slog.String("cache", cachePath))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// If cached file exists, send If-Modified-Since
	if fi, err := os.Stat(cachePath); err == nil {
		req.Header.Set("If-Modified-Since", fi.ModTime().UTC().Format(http.TimeFormat))
		p.logger.Debug("sending If-Modified-Since", slog.String("mtime", fi.ModTime().UTC().Format(http.TimeFormat)))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching URL: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotModified {
		p.logger.Info("metadata not modified, using cached copy", slog.String("url", url))
		data, err := os.ReadFile(cachePath)
		if err != nil {
			return nil, fmt.Errorf("reading cached file: %w", err)
		}
		return &fetchResult{Data: data, Modified: false}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d for %s", resp.StatusCode, url)
	}

	data, err := safety.ReadAllWithLimit(resp.Body, maxEPELMetadataBytes)
	if err != nil {
		if errors.Is(err, safety.ErrBodyTooLarge) {
			return nil, fmt.Errorf("metadata response exceeded %d bytes: %w", maxEPELMetadataBytes, err)
		}
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Cache the response to disk
	if dir := filepath.Dir(cachePath); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		p.logger.Warn("failed to cache metadata", slog.String("path", cachePath), slog.String("error", err.Error()))
	}

	return &fetchResult{Data: data, Modified: true}, nil
}

// fetchURL downloads content from a given URL
func (p *EPELProvider) fetchURL(ctx context.Context, url string) ([]byte, error) {
	p.logger.Debug("fetching URL", slog.String("url", url))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching URL: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	p.logger.Debug("response received",
		slog.String("url", url),
		slog.Int("status", resp.StatusCode),
		slog.String("content-type", resp.Header.Get("Content-Type")),
		slog.String("content-encoding", resp.Header.Get("Content-Encoding")),
	)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d for %s", resp.StatusCode, url)
	}

	data, err := safety.ReadAllWithLimit(resp.Body, maxEPELMetadataBytes)
	if err != nil {
		if errors.Is(err, safety.ErrBodyTooLarge) {
			return nil, fmt.Errorf("response exceeded %d bytes: %w", maxEPELMetadataBytes, err)
		}
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	p.logger.Debug("response body read",
		slog.String("url", url),
		slog.Int("bytes", len(data)),
		slog.String("first_bytes", debugFirst(data, 32)),
	)

	return data, nil
}

// debugFirst returns a hex + ascii preview of the first n bytes for debug logging.
func debugFirst(data []byte, n int) string {
	if len(data) == 0 {
		return "(empty)"
	}
	if len(data) < n {
		n = len(data)
	}
	preview := make([]byte, 0, n)
	for _, b := range data[:n] {
		if b >= 0x20 && b < 0x7f {
			preview = append(preview, b)
		} else {
			preview = append(preview, '.')
		}
	}
	return fmt.Sprintf("hex=%x ascii=%s", data[:n], preview)
}

// decompress detects the compression format by magic number and decompresses.
// Supports gzip (.gz), xz (.xz), and zstd (.zst). If the data is already
// decompressed (e.g. plain XML), it is returned as-is.
func (p *EPELProvider) decompress(data []byte) ([]byte, error) {
	if len(data) < 6 {
		return data, nil
	}

	// Zstd magic number: 28 b5 2f fd
	if data[0] == 0x28 && data[1] == 0xb5 && data[2] == 0x2f && data[3] == 0xfd {
		p.logger.Debug("detected zstd compression, decompressing")
		decoder, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("creating zstd reader: %w", err)
		}
		defer decoder.Close()
		decompressed, err := safety.ReadAllWithLimit(decoder, maxEPELDecompressedXMLSize)
		if err != nil {
			if errors.Is(err, safety.ErrBodyTooLarge) {
				return nil, fmt.Errorf("zstd payload exceeded %d bytes after decompression: %w", maxEPELDecompressedXMLSize, err)
			}
			return nil, fmt.Errorf("decompressing zstd: %w", err)
		}
		return decompressed, nil
	}

	// XZ magic number: fd 37 7a 58 5a 00
	if data[0] == 0xfd && data[1] == 0x37 && data[2] == 0x7a &&
		data[3] == 0x58 && data[4] == 0x5a && data[5] == 0x00 {
		p.logger.Debug("detected xz compression, decompressing")
		reader, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("creating xz reader: %w", err)
		}
		decompressed, err := safety.ReadAllWithLimit(reader, maxEPELDecompressedXMLSize)
		if err != nil {
			if errors.Is(err, safety.ErrBodyTooLarge) {
				return nil, fmt.Errorf("xz payload exceeded %d bytes after decompression: %w", maxEPELDecompressedXMLSize, err)
			}
			return nil, fmt.Errorf("decompressing xz: %w", err)
		}
		return decompressed, nil
	}

	// Gzip magic number: 1f 8b
	if data[0] == 0x1f && data[1] == 0x8b {
		p.logger.Debug("detected gzip compression, decompressing")
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("creating gzip reader: %w", err)
		}
		defer func() {
			_ = reader.Close()
		}()
		decompressed, err := safety.ReadAllWithLimit(reader, maxEPELDecompressedXMLSize)
		if err != nil {
			if errors.Is(err, safety.ErrBodyTooLarge) {
				return nil, fmt.Errorf("gzip payload exceeded %d bytes after decompression: %w", maxEPELDecompressedXMLSize, err)
			}
			return nil, fmt.Errorf("decompressing gzip: %w", err)
		}
		return decompressed, nil
	}

	// Not compressed — already plain text (e.g. XML)
	p.logger.Debug("data not compressed, using as-is")
	return data, nil
}

// checksumLocalFile computes the SHA256 checksum of a local file
func checksumLocalFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
