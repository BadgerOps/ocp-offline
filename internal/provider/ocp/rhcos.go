package ocp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/safety"
)

// RHCOSProvider implements provider.Provider for RHCOS images.
type RHCOSProvider struct {
	name                 string
	cfg                  *config.RHCOSProviderConfig
	dataDir              string
	logger               *slog.Logger
	validationProgressFn provider.ValidationProgressFn
}

// SetValidationProgress sets the callback for per-file validation progress.
func (p *RHCOSProvider) SetValidationProgress(fn provider.ValidationProgressFn) {
	p.validationProgressFn = fn
}

// NewRHCOSProvider creates a new RHCOS provider.
func NewRHCOSProvider(dataDir string, logger *slog.Logger) *RHCOSProvider {
	return &RHCOSProvider{
		name:    "rhcos",
		dataDir: dataDir,
		logger:  logger,
	}
}

// Name returns the provider identifier.
func (p *RHCOSProvider) Name() string {
	return p.name
}

// SetName overrides the default provider name with the user-chosen config name.
func (p *RHCOSProvider) SetName(name string) {
	p.name = name
}

func (p *RHCOSProvider) Type() string {
	return "rhcos"
}

// Configure loads provider-specific settings from the raw config.
func (p *RHCOSProvider) Configure(rawCfg provider.ProviderConfig) error {
	cfg, err := config.ParseProviderConfig[config.RHCOSProviderConfig](rawCfg)
	if err != nil {
		return fmt.Errorf("parsing RHCOS config: %w", err)
	}
	p.cfg = cfg
	if _, err := safety.ValidateHTTPURL(p.cfg.BaseURL); err != nil {
		return fmt.Errorf("invalid base_url: %w", err)
	}

	p.logger.Debug("configured RHCOS provider",
		slog.String("base_url", p.cfg.BaseURL),
		slog.Int("versions", len(p.cfg.Versions)),
		slog.String("output_dir", p.cfg.OutputDir),
	)

	return nil
}

// Plan compares upstream manifest against local state and returns a plan.
func (p *RHCOSProvider) Plan(ctx context.Context) (*provider.SyncPlan, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	plan := &provider.SyncPlan{
		Provider:  p.Name(),
		Actions:   []provider.SyncAction{},
		Timestamp: time.Now(),
	}

	for _, version := range p.cfg.Versions {
		p.logger.Debug("planning sync for version",
			slog.String("version", version))

		checksumURL := fmt.Sprintf("%s/%s/sha256sum.txt", strings.TrimRight(p.cfg.BaseURL, "/"), version)

		// Fetch checksum file
		checksumData, err := p.fetchChecksumFile(ctx, checksumURL)
		if err != nil {
			p.logger.Error("failed to fetch checksum file",
				slog.String("version", version),
				slog.String("url", checksumURL),
				slog.String("error", err.Error()))
			continue
		}

		// Parse checksum file
		remoteFiles := parseChecksumFile(checksumData)
		p.logger.Debug("parsed checksum file",
			slog.String("version", version),
			slog.Int("files", len(remoteFiles)))

		// Filter by ignored patterns
		filteredFiles := filterFiles(remoteFiles, p.cfg.IgnoredPatterns)
		p.logger.Debug("filtered files",
			slog.String("version", version),
			slog.Int("before", len(remoteFiles)),
			slog.Int("after", len(filteredFiles)))

		// Build sync plan for this version
		versionActions, err := buildSyncPlan(p.Name(), p.cfg.BaseURL, version, p.cfg.OutputDir, p.dataDir, filteredFiles, p.logger)
		if err != nil {
			p.logger.Error("failed to build sync plan for version",
				slog.String("version", version),
				slog.String("error", err.Error()))
			continue
		}

		// Aggregate actions and calculate totals
		for _, action := range versionActions {
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

// Sync executes the plan — actual downloads are handled by the sync engine.
func (p *RHCOSProvider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
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

	for _, action := range plan.Actions {
		switch action.Action {
		case provider.ActionSkip:
			report.Skipped++
		case provider.ActionDownload, provider.ActionUpdate:
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

// Validate checks integrity of all local content against upstream sha256sum.txt.
// For each configured version, it re-fetches the manifest and verifies local files.
func (p *RHCOSProvider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	report := &provider.ValidationReport{
		Provider:     p.Name(),
		InvalidFiles: []provider.ValidationResult{},
		Timestamp:    time.Now(),
	}

	outputRoot, err := safety.SafeJoinUnder(p.dataDir, p.cfg.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("invalid output_dir %q: %w", p.cfg.OutputDir, err)
	}

	checked := 0

	for _, version := range p.cfg.Versions {
		checksumURL := fmt.Sprintf("%s/%s/sha256sum.txt", strings.TrimRight(p.cfg.BaseURL, "/"), version)

		checksumData, err := p.fetchChecksumFile(ctx, checksumURL)
		if err != nil {
			p.logger.Warn("failed to fetch checksum file for validation",
				slog.String("version", version),
				slog.String("url", checksumURL),
				slog.String("error", err.Error()))
			continue
		}

		remoteFiles := parseChecksumFile(checksumData)
		filteredFiles := filterFiles(remoteFiles, p.cfg.IgnoredPatterns)

		versionDir, err := safety.SafeJoinUnder(outputRoot, version)
		if err != nil {
			return nil, fmt.Errorf("invalid version %q: %w", version, err)
		}

		for filename, expectedHash := range filteredFiles {
			localPath, pathErr := safety.SafeJoinUnder(versionDir, filename)
			if pathErr != nil {
				report.TotalFiles++
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:     filepath.ToSlash(filepath.Join(version, filename)),
					Expected: expectedHash,
					Actual:   "error: unsafe path: " + pathErr.Error(),
					Valid:    false,
				})
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, filepath.ToSlash(filepath.Join(version, filename)), false)
				}
				continue
			}

			relPath, err := filepath.Rel(outputRoot, localPath)
			if err != nil {
				return nil, fmt.Errorf("building relative path for %q: %w", filename, err)
			}
			relPath = filepath.ToSlash(relPath)

			downloadURL := fmt.Sprintf("%s/%s/%s", strings.TrimRight(p.cfg.BaseURL, "/"), version, filename)
			report.TotalFiles++

			// Check if file exists
			fileInfo, statErr := os.Stat(localPath)
			if os.IsNotExist(statErr) {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  expectedHash,
					Actual:    "missing",
					Valid:     false,
					URL:       downloadURL,
				})
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, false)
				}
				continue
			}
			if statErr != nil {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  expectedHash,
					Actual:    "error: " + statErr.Error(),
					Valid:     false,
					URL:       downloadURL,
				})
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, false)
				}
				continue
			}

			// Compute checksum and compare
			actualHash, hashErr := checksumLocalFile(localPath)
			if hashErr != nil {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  expectedHash,
					Actual:    "error: " + hashErr.Error(),
					Valid:     false,
					Size:      fileInfo.Size(),
					URL:       downloadURL,
				})
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, false)
				}
				continue
			}

			if actualHash == expectedHash {
				report.ValidFiles++
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, true)
				}
			} else {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  expectedHash,
					Actual:    actualHash,
					Valid:     false,
					Size:      fileInfo.Size(),
					URL:       downloadURL,
				})
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, false)
				}
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

// fetchChecksumFile downloads a sha256sum.txt file from the given URL.
func (p *RHCOSProvider) fetchChecksumFile(ctx context.Context, url string) ([]byte, error) {
	data, err := fetchWithStatusOK(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetching checksum file: %w", err)
	}

	return data, nil
}
