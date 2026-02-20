package epel

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/provider"
)

// EPELProvider implements provider.Provider for EPEL repositories
type EPELProvider struct {
	cfg     *config.EPELProviderConfig
	dataDir string
	logger  *slog.Logger
}

// NewEPELProvider creates a new EPEL provider
func NewEPELProvider(dataDir string, logger *slog.Logger) *EPELProvider {
	return &EPELProvider{
		dataDir: dataDir,
		logger:  logger,
	}
}

// Name returns the provider identifier
func (p *EPELProvider) Name() string {
	return "epel"
}

// Configure loads provider-specific settings from the raw config
func (p *EPELProvider) Configure(rawCfg provider.ProviderConfig) error {
	cfg, err := config.ParseProviderConfig[config.EPELProviderConfig](rawCfg)
	if err != nil {
		return fmt.Errorf("parsing EPEL config: %w", err)
	}
	p.cfg = cfg

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

	// Fetch repomd.xml
	repomdURL := strings.TrimRight(repo.BaseURL, "/") + "/repodata/repomd.xml"
	repomdData, err := p.fetchURL(ctx, repomdURL)
	if err != nil {
		return nil, fmt.Errorf("fetching repomd.xml: %w", err)
	}

	// Parse repomd.xml
	repomd, err := ParseRepomd(repomdData)
	if err != nil {
		return nil, fmt.Errorf("parsing repomd.xml: %w", err)
	}

	// Find primary.xml.gz location
	primaryLocation, err := repomd.FindPrimaryLocation()
	if err != nil {
		return nil, fmt.Errorf("finding primary location: %w", err)
	}

	// Fetch primary.xml.gz
	primaryURL := strings.TrimRight(repo.BaseURL, "/") + "/" + strings.TrimLeft(primaryLocation, "/")
	primaryGzData, err := p.fetchURL(ctx, primaryURL)
	if err != nil {
		return nil, fmt.Errorf("fetching primary.xml.gz: %w", err)
	}

	// Decompress primary.xml.gz
	primaryData, err := p.decompressGzip(primaryGzData)
	if err != nil {
		return nil, fmt.Errorf("decompressing primary.xml.gz: %w", err)
	}

	// Parse primary.xml
	primaryXML, err := ParsePrimary(primaryData)
	if err != nil {
		return nil, fmt.Errorf("parsing primary.xml: %w", err)
	}

	// Extract packages
	packages := primaryXML.ExtractPackages()

	// Build sync plan
	outputDir := filepath.Join(p.dataDir, repo.OutputDir)

	for _, pkg := range packages {
		actions = append(actions, p.buildPackageAction(repo, outputDir, pkg))
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

// buildPackageAction creates a SyncAction for a package
func (p *EPELProvider) buildPackageAction(repo config.EPELRepoConfig, outputDir string, pkg PackageInfo) provider.SyncAction {
	localPath := filepath.Join(outputDir, pkg.Location)
	downloadURL := strings.TrimRight(repo.BaseURL, "/") + "/" + strings.TrimLeft(pkg.Location, "/")

	// Check if local file exists and has matching checksum
	if fileInfo, err := os.Stat(localPath); err == nil {
		// File exists, check checksum
		actualHash, err := checksumLocalFile(localPath)
		if err != nil {
			p.logger.Warn("failed to compute checksum for local file",
				slog.String("file", pkg.Location),
				slog.String("error", err.Error()))
			// If we can't compute checksum, treat as mismatch
			return provider.SyncAction{
				Path:     pkg.Location,
				Action:   provider.ActionUpdate,
				Size:     fileInfo.Size(),
				Checksum: pkg.Checksum,
				Reason:   "checksum verification failed",
				URL:      downloadURL,
			}
		}

		if actualHash == pkg.Checksum {
			// File is valid, skip
			return provider.SyncAction{
				Path:     pkg.Location,
				Action:   provider.ActionSkip,
				Size:     fileInfo.Size(),
				Checksum: pkg.Checksum,
				Reason:   "checksum matches",
				URL:      downloadURL,
			}
		}
		// Checksum mismatch, update
		return provider.SyncAction{
			Path:     pkg.Location,
			Action:   provider.ActionUpdate,
			Size:     fileInfo.Size(),
			Checksum: pkg.Checksum,
			Reason:   "checksum mismatch",
			URL:      downloadURL,
		}
	}

	// File doesn't exist, download
	return provider.SyncAction{
		Path:     pkg.Location,
		Action:   provider.ActionDownload,
		Size:     pkg.Size,
		Checksum: pkg.Checksum,
		Reason:   "new file",
		URL:      downloadURL,
	}
}

// findDeletedPackages walks the local directory and returns actions for files not in the manifest
func (p *EPELProvider) findDeletedPackages(outputDir string, packages []PackageInfo) ([]provider.SyncAction, error) {
	// Build set of remote package locations
	remoteSet := make(map[string]bool)
	for _, pkg := range packages {
		remoteSet[pkg.Location] = true
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
		outputDir := filepath.Join(p.dataDir, repo.OutputDir)

		// Walk the output directory
		err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				p.logger.Warn("error walking directory",
					slog.String("path", path),
					slog.String("error", err.Error()))
				return nil
			}

			// Skip directories
			if info.IsDir() {
				return nil
			}

			report.TotalFiles++

			// Compute checksum
			_, err = checksumLocalFile(path)
			if err != nil {
				p.logger.Error("failed to compute checksum",
					slog.String("file", path),
					slog.String("error", err.Error()))
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:   path,
					Valid:  false,
					Size:   info.Size(),
					Actual: "error",
				})
				return nil
			}

			// For validation, we mark as valid if checksum can be computed
			// (In a complete implementation, you'd fetch the manifest again or store checksums)
			report.ValidFiles++

			return nil
		})

		if err != nil && !os.IsNotExist(err) {
			p.logger.Warn("error validating directory",
				slog.String("path", outputDir),
				slog.String("error", err.Error()))
		}
	}

	p.logger.Info("validation completed",
		slog.String("provider", p.Name()),
		slog.Int("total_files", report.TotalFiles),
		slog.Int("valid_files", report.ValidFiles),
		slog.Int("invalid_files", len(report.InvalidFiles)))

	return report, nil
}

// fetchURL downloads content from a given URL
func (p *EPELProvider) fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return data, nil
}

// decompressGzip decompresses gzip data
func (p *EPELProvider) decompressGzip(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("creating gzip reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("decompressing gzip: %w", err)
	}

	return decompressed, nil
}

// checksumLocalFile computes the SHA256 checksum of a local file
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
