package ocp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	ocpsvc "github.com/BadgerOps/airgap/internal/ocp"
	"github.com/BadgerOps/airgap/internal/provider"
)

// ClientsProvider implements provider.Provider for OCP client binaries
// (oc and openshift-install) with channel-based auto-discovery and
// platform filtering.
type ClientsProvider struct {
	name                 string
	cfg                  *config.OCPClientsProviderConfig
	dataDir              string
	logger               *slog.Logger
	clientSvc            *ocpsvc.ClientService
	validationProgressFn provider.ValidationProgressFn
}

// SetValidationProgress sets the callback for per-file validation progress.
func (p *ClientsProvider) SetValidationProgress(fn provider.ValidationProgressFn) {
	p.validationProgressFn = fn
}

// NewClientsProvider creates a new OCP clients provider.
func NewClientsProvider(dataDir string, logger *slog.Logger) *ClientsProvider {
	return &ClientsProvider{
		name:    "ocp_clients",
		dataDir: dataDir,
		logger:  logger,
	}
}

// Name returns the provider identifier.
func (p *ClientsProvider) Name() string {
	return p.name
}

// SetName overrides the default provider name with the user-chosen config name.
func (p *ClientsProvider) SetName(name string) {
	p.name = name
}

// Type returns the provider type string.
func (p *ClientsProvider) Type() string {
	return "ocp_clients"
}

// Configure loads provider-specific settings from the raw config.
func (p *ClientsProvider) Configure(rawCfg provider.ProviderConfig) error {
	cfg, err := config.ParseProviderConfig[config.OCPClientsProviderConfig](rawCfg)
	if err != nil {
		return fmt.Errorf("parsing OCP clients config: %w", err)
	}

	// Apply defaults
	if cfg.OutputDir == "" {
		cfg.OutputDir = "ocp-clients"
	}
	if len(cfg.Platforms) == 0 {
		cfg.Platforms = []string{"linux", "linux-arm64"}
	}

	p.cfg = cfg
	p.clientSvc = ocpsvc.NewClientService(p.logger)

	p.logger.Debug("configured OCP clients provider",
		slog.Int("channels", len(p.cfg.Channels)),
		slog.Int("versions", len(p.cfg.Versions)),
		slog.Any("platforms", p.cfg.Platforms),
		slog.String("output_dir", p.cfg.OutputDir),
	)

	return nil
}

// Plan compares upstream artifacts against local state and returns a sync plan.
// For each configured channel, it discovers the latest releases via the graph API.
// For each pinned version, it includes it directly.
// Artifacts are filtered by the configured platforms.
func (p *ClientsProvider) Plan(ctx context.Context) (*provider.SyncPlan, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	plan := &provider.SyncPlan{
		Provider:  p.Name(),
		Actions:   []provider.SyncAction{},
		Timestamp: time.Now(),
	}

	// Collect all versions to sync (from channels + pinned versions)
	versions, err := p.resolveVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving versions: %w", err)
	}

	if len(versions) == 0 {
		p.logger.Info("no versions to sync", "provider", p.Name())
		return plan, nil
	}

	// For each version, fetch sha256sum.txt manifest and use it as the source of truth
	for _, version := range versions {
		manifest, err := p.clientSvc.FetchManifest(ctx, version)
		if err != nil {
			p.logger.Error("failed to fetch manifest for version",
				slog.String("version", version),
				slog.String("error", err.Error()))
			continue
		}

		// Filter artifacts by configured platforms
		artifacts := ocpsvc.FilterArtifactsByPlatform(manifest.Artifacts, p.cfg.Platforms)
		if len(artifacts) == 0 {
			continue
		}

		// Build sync actions for each artifact
		for _, artifact := range artifacts {
			localPath := filepath.Join(p.dataDir, p.cfg.OutputDir, version, artifact.Name)
			relPath := filepath.Join(version, artifact.Name)

			action := p.planArtifact(artifact, localPath, relPath, artifact.Checksum)
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

// planArtifact creates a SyncAction for a single artifact by comparing against local state.
func (p *ClientsProvider) planArtifact(artifact ocpsvc.ClientArtifact, localPath, relPath, expectedHash string) provider.SyncAction {
	// Check if local file exists
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionDownload,
				Size:      0,
				Checksum:  expectedHash,
				Reason:    "new file",
				URL:       artifact.URL,
			}
		}
		return provider.SyncAction{
			Path:      relPath,
			LocalPath: localPath,
			Action:    provider.ActionDownload,
			Size:      0,
			Checksum:  expectedHash,
			Reason:    "error checking file",
			URL:       artifact.URL,
		}
	}

	// File exists — if we have a checksum, verify it
	if expectedHash != "" {
		actualHash, err := checksumLocalFile(localPath)
		if err != nil {
			return provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionUpdate,
				Size:      fileInfo.Size(),
				Checksum:  expectedHash,
				Reason:    "checksum verification failed",
				URL:       artifact.URL,
			}
		}
		if actualHash == expectedHash {
			return provider.SyncAction{
				Path:      relPath,
				LocalPath: localPath,
				Action:    provider.ActionSkip,
				Size:      fileInfo.Size(),
				Checksum:  expectedHash,
				Reason:    "checksum matches",
				URL:       artifact.URL,
			}
		}
		return provider.SyncAction{
			Path:      relPath,
			LocalPath: localPath,
			Action:    provider.ActionUpdate,
			Size:      fileInfo.Size(),
			Checksum:  expectedHash,
			Reason:    "checksum mismatch",
			URL:       artifact.URL,
		}
	}

	// No checksum available — skip if file exists (assume it's good)
	return provider.SyncAction{
		Path:      relPath,
		LocalPath: localPath,
		Action:    provider.ActionSkip,
		Size:      fileInfo.Size(),
		Reason:    "file exists (no checksum available)",
		URL:       artifact.URL,
	}
}

// resolveVersions collects all versions to sync from channels and pinned versions.
func (p *ClientsProvider) resolveVersions(ctx context.Context) ([]string, error) {
	seen := make(map[string]bool)
	var versions []string

	// Resolve channel-based versions
	for _, channel := range p.cfg.Channels {
		releases, err := p.clientSvc.FetchReleases(ctx, channel)
		if err != nil {
			p.logger.Error("failed to fetch releases for channel",
				"channel", channel, "error", err)
			continue
		}
		for _, v := range releases.Releases {
			if !seen[v] {
				seen[v] = true
				versions = append(versions, v)
			}
		}
		p.logger.Info("resolved channel versions",
			"channel", channel,
			"count", len(releases.Releases),
			"latest", releases.Latest)
	}

	// Add pinned versions
	for _, v := range p.cfg.Versions {
		if !seen[v] {
			seen[v] = true
			versions = append(versions, v)
		}
	}

	// Sort for deterministic output
	ocpsvc.SortVersions(versions)

	return versions, nil
}

// Sync executes the plan — stub, actual downloads handled by the sync engine.
func (p *ClientsProvider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
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
	return report, nil
}

// Validate checks integrity of all local content against upstream sha256sum.txt.
// For each configured version, it fetches the manifest and verifies local files.
func (p *ClientsProvider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	report := &provider.ValidationReport{
		Provider:     p.Name(),
		InvalidFiles: []provider.ValidationResult{},
		Timestamp:    time.Now(),
	}

	// Resolve all versions (same logic as Plan)
	versions, err := p.resolveVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving versions: %w", err)
	}

	checked := 0

	for _, version := range versions {
		manifest, err := p.clientSvc.FetchManifest(ctx, version)
		if err != nil {
			p.logger.Warn("failed to fetch manifest for validation",
				slog.String("version", version),
				slog.String("error", err.Error()))
			continue
		}

		// Filter to configured platforms
		artifacts := ocpsvc.FilterArtifactsByPlatform(manifest.Artifacts, p.cfg.Platforms)

		for _, artifact := range artifacts {
			localPath := filepath.Join(p.dataDir, p.cfg.OutputDir, version, artifact.Name)
			relPath := filepath.Join(version, artifact.Name)
			report.TotalFiles++

			// Check if file exists
			fileInfo, statErr := os.Stat(localPath)
			if os.IsNotExist(statErr) {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  artifact.Checksum,
					Actual:    "missing",
					Valid:     false,
					URL:       artifact.URL,
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
					Expected:  artifact.Checksum,
					Actual:    "error: " + statErr.Error(),
					Valid:     false,
					Size:      0,
					URL:       artifact.URL,
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
					Expected:  artifact.Checksum,
					Actual:    "error: " + hashErr.Error(),
					Valid:     false,
					Size:      fileInfo.Size(),
					URL:       artifact.URL,
				})
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, false)
				}
				continue
			}

			if actualHash == artifact.Checksum {
				report.ValidFiles++
				checked++
				if p.validationProgressFn != nil {
					p.validationProgressFn(checked, report.TotalFiles, relPath, true)
				}
			} else {
				report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
					Path:      relPath,
					LocalPath: localPath,
					Expected:  artifact.Checksum,
					Actual:    actualHash,
					Valid:     false,
					Size:      fileInfo.Size(),
					URL:       artifact.URL,
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

