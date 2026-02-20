package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

// ProviderFactory creates a provider instance given a type name and data dir.
type ProviderFactory func(typeName, dataDir string, logger *slog.Logger) (provider.Provider, error)

// SyncManager orchestrates providers and connects them to the download client and store.
type SyncManager struct {
	registry        *provider.Registry
	store           *store.Store
	client          *download.Client
	config          *config.Config
	logger          *slog.Logger
	mu              sync.RWMutex
	providerFactory ProviderFactory
}

// ProviderStatus summarizes a provider's state.
type ProviderStatus struct {
	Name        string
	Enabled     bool
	FileCount   int
	TotalSize   int64
	LastSync    time.Time
	LastStatus  string
	FailedFiles int
}

// NewSyncManager creates a new SyncManager.
func NewSyncManager(
	registry *provider.Registry,
	st *store.Store,
	client *download.Client,
	cfg *config.Config,
	logger *slog.Logger,
) *SyncManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &SyncManager{
		registry: registry,
		store:    st,
		client:   client,
		config:   cfg,
		logger:   logger,
	}
}

// SetProviderFactory sets the factory used by ReconfigureProviders.
func (m *SyncManager) SetProviderFactory(f ProviderFactory) {
	m.providerFactory = f
}

// SyncProvider synchronizes a single provider.
// It orchestrates planning, downloading, storing, and cleanup operations.
func (m *SyncManager) SyncProvider(ctx context.Context, name string, opts provider.SyncOptions) (*provider.SyncReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.logger.Info("starting sync", "provider", name, "dry_run", opts.DryRun)

	// Look up provider in registry
	p, ok := m.registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}

	startTime := time.Now()

	// Create a SyncRun record in the store with status "running"
	syncRun := &store.SyncRun{
		Provider:  name,
		StartTime: startTime,
		Status:    "running",
	}

	if err := m.store.CreateSyncRun(syncRun); err != nil {
		m.logger.Error("failed to create sync run record", "provider", name, "error", err)
		return nil, fmt.Errorf("failed to create sync run: %w", err)
	}

	// Call provider.Plan() to get the SyncPlan
	plan, err := p.Plan(ctx)
	if err != nil {
		syncRun.Status = "failed"
		syncRun.ErrorMessage = err.Error()
		syncRun.EndTime = time.Now()
		_ = m.store.UpdateSyncRun(syncRun)
		m.logger.Error("failed to plan sync", "provider", name, "error", err)
		return nil, fmt.Errorf("failed to plan sync: %w", err)
	}

	m.logger.Info("sync plan generated", "provider", name, "actions", len(plan.Actions), "total_size", plan.TotalSize)

	// If DryRun, log the plan and return early
	if opts.DryRun {
		m.logger.Info("dry run mode: not executing sync", "provider", name, "actions", len(plan.Actions))
		syncRun.Status = "completed"
		syncRun.EndTime = time.Now()
		syncRun.FilesFailed = 0
		for _, action := range plan.Actions {
			switch action.Action {
			case provider.ActionDownload, provider.ActionUpdate:
				syncRun.FilesDownloaded++
			case provider.ActionDelete:
				syncRun.FilesDeleted++
			case provider.ActionSkip:
				syncRun.FilesSkipped++
			}
		}
		_ = m.store.UpdateSyncRun(syncRun)

		return &provider.SyncReport{
			Provider:         name,
			StartTime:        startTime,
			EndTime:          time.Now(),
			Downloaded:       syncRun.FilesDownloaded,
			Deleted:          syncRun.FilesDeleted,
			Skipped:          syncRun.FilesSkipped,
			Failed:           []provider.FailedFile{},
			BytesTransferred: 0,
		}, nil
	}

	// Build download.Job slice from the plan's Download/Update actions
	var downloadJobs []download.Job
	for _, action := range plan.Actions {
		if action.Action == provider.ActionDownload || action.Action == provider.ActionUpdate {
			downloadJobs = append(downloadJobs, download.Job{
				URL:              action.URL,
				DestPath:         action.Path,
				ExpectedChecksum: action.Checksum,
				ExpectedSize:     action.Size,
			})
		}
	}

	// Determine worker count
	workers := 4
	if opts.MaxWorkers > 0 {
		workers = opts.MaxWorkers
	}

	// Execute the download pool
	var downloadResults []download.Result
	if len(downloadJobs) > 0 {
		pool := download.NewPool(m.client, workers, m.logger)
		downloadResults = pool.Execute(ctx, downloadJobs)
	}

	// Track results
	downloadedCount := 0
	failedCount := 0
	skippedCount := 0
	totalBytesTransferred := int64(0)
	failedFiles := []provider.FailedFile{}

	// Create a map of download results for quick lookup
	downloadResultMap := make(map[string]*download.Result)
	for i := range downloadResults {
		downloadResultMap[downloadResults[i].Job.DestPath] = &downloadResults[i]
	}

	// Process results: upsert successful downloads, track failed ones
	for _, action := range plan.Actions {
		switch action.Action {
		case provider.ActionDownload, provider.ActionUpdate:
			result, ok := downloadResultMap[action.Path]
			if ok && result.Success {
				downloadedCount++
				totalBytesTransferred += result.Download.Size

				// Upsert FileRecord in the store
				fileRec := &store.FileRecord{
					Provider:     name,
					Path:         action.Path,
					Size:         result.Download.Size,
					SHA256:       result.Download.SHA256,
					LastModified: time.Now(),
					LastVerified: time.Now(),
					SyncRunID:    syncRun.ID,
				}

				if err := m.store.UpsertFileRecord(fileRec); err != nil {
					m.logger.Error("failed to upsert file record", "provider", name, "path", action.Path, "error", err)
				}
			} else if ok && !result.Success {
				failedCount++
				failedFiles = append(failedFiles, provider.FailedFile{
					Path:     action.Path,
					URL:      action.URL,
					Error:    result.Error.Error(),
					Attempts: 1,
				})

				// Add to FailedFileRecord
				failedRec := &store.FailedFileRecord{
					Provider:         name,
					FilePath:         action.Path,
					URL:              action.URL,
					ExpectedChecksum: action.Checksum,
					Error:            result.Error.Error(),
					RetryCount:       1,
					FirstFailure:     time.Now(),
					LastFailure:      time.Now(),
					Resolved:         false,
				}

				if err := m.store.AddFailedFile(failedRec); err != nil {
					m.logger.Error("failed to add failed file record", "provider", name, "path", action.Path, "error", err)
				}

				m.logger.Warn("download failed", "provider", name, "path", action.Path, "url", action.URL, "error", result.Error)
			}

		case provider.ActionDelete:
			// Remove local file and delete FileRecord from store
			filePath := filepath.Join(m.config.Server.DataDir, action.Path)
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				m.logger.Warn("failed to remove local file", "provider", name, "path", action.Path, "error", err)
			}

			if err := m.store.DeleteFileRecord(name, action.Path); err != nil {
				m.logger.Warn("failed to delete file record", "provider", name, "path", action.Path, "error", err)
			}

		case provider.ActionSkip:
			skippedCount++
		}
	}

	// Update the SyncRun record with final status
	syncRun.FilesDownloaded = downloadedCount
	syncRun.FilesDeleted = 0
	for _, action := range plan.Actions {
		if action.Action == provider.ActionDelete {
			syncRun.FilesDeleted++
		}
	}
	syncRun.FilesSkipped = skippedCount
	syncRun.FilesFailed = failedCount
	syncRun.BytesTransferred = totalBytesTransferred
	syncRun.EndTime = time.Now()

	if failedCount > 0 {
		syncRun.Status = "partial"
	} else {
		syncRun.Status = "success"
	}

	if err := m.store.UpdateSyncRun(syncRun); err != nil {
		m.logger.Error("failed to update sync run record", "provider", name, "error", err)
	}

	// Build and return SyncReport
	report := &provider.SyncReport{
		Provider:         name,
		StartTime:        startTime,
		EndTime:          time.Now(),
		Downloaded:       downloadedCount,
		Deleted:          syncRun.FilesDeleted,
		Skipped:          skippedCount,
		Failed:           failedFiles,
		BytesTransferred: totalBytesTransferred,
	}

	m.logger.Info("sync completed",
		"provider", name,
		"downloaded", downloadedCount,
		"deleted", syncRun.FilesDeleted,
		"skipped", skippedCount,
		"failed", failedCount,
		"bytes_transferred", totalBytesTransferred,
		"duration", report.EndTime.Sub(report.StartTime),
	)

	return report, nil
}

// SyncAll synchronizes all enabled providers.
// It continues even if one provider fails, collecting all reports and errors.
func (m *SyncManager) SyncAll(ctx context.Context, opts provider.SyncOptions) (map[string]*provider.SyncReport, error) {
	reports := make(map[string]*provider.SyncReport)
	var hasErrors bool

	for _, name := range m.registry.Names() {
		if !m.config.ProviderEnabled(name) {
			m.logger.Debug("skipping disabled provider", "provider", name)
			continue
		}

		report, err := m.SyncProvider(ctx, name, opts)
		if err != nil {
			m.logger.Error("failed to sync provider", "provider", name, "error", err)
			hasErrors = true
			continue
		}

		reports[name] = report

		// Check for context cancellation between providers
		select {
		case <-ctx.Done():
			m.logger.Info("sync all cancelled")
			return reports, ctx.Err()
		default:
		}
	}

	if hasErrors {
		return reports, fmt.Errorf("one or more providers failed")
	}

	return reports, nil
}

// ValidateProvider validates a single provider.
func (m *SyncManager) ValidateProvider(ctx context.Context, name string) (*provider.ValidationReport, error) {
	m.logger.Info("starting validation", "provider", name)

	p, ok := m.registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", name)
	}

	report, err := p.Validate(ctx)
	if err != nil {
		m.logger.Error("validation failed", "provider", name, "error", err)
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	m.logger.Info("validation completed",
		"provider", name,
		"total_files", report.TotalFiles,
		"valid_files", report.ValidFiles,
		"invalid_files", len(report.InvalidFiles),
	)

	return report, nil
}

// ValidateAll validates all enabled providers.
// It continues even if one provider fails, collecting all reports.
func (m *SyncManager) ValidateAll(ctx context.Context) (map[string]*provider.ValidationReport, error) {
	reports := make(map[string]*provider.ValidationReport)
	var hasErrors bool

	for _, name := range m.registry.Names() {
		if !m.config.ProviderEnabled(name) {
			m.logger.Debug("skipping disabled provider", "provider", name)
			continue
		}

		report, err := m.ValidateProvider(ctx, name)
		if err != nil {
			m.logger.Error("failed to validate provider", "provider", name, "error", err)
			hasErrors = true
			continue
		}

		reports[name] = report

		// Check for context cancellation between providers
		select {
		case <-ctx.Done():
			m.logger.Info("validate all cancelled")
			return reports, ctx.Err()
		default:
		}
	}

	if hasErrors {
		return reports, fmt.Errorf("one or more providers failed validation")
	}

	return reports, nil
}

// Status returns a summary of each provider's state by querying the store.
func (m *SyncManager) Status() map[string]ProviderStatus {
	statuses := make(map[string]ProviderStatus)

	for _, name := range m.registry.Names() {
		enabled := m.config.ProviderEnabled(name)

		// Query store for file count and total size
		fileCount, err := m.store.CountFileRecords(name)
		if err != nil {
			m.logger.Warn("failed to count file records", "provider", name, "error", err)
		}

		totalSize, err := m.store.SumFileSize(name)
		if err != nil {
			m.logger.Warn("failed to sum file size", "provider", name, "error", err)
		}

		// Get the most recent sync run
		runs, err := m.store.ListSyncRuns(name, 1)
		var lastSync time.Time
		var lastStatus string

		if err == nil && len(runs) > 0 {
			lastSync = runs[0].StartTime
			lastStatus = runs[0].Status
		} else if err != nil {
			m.logger.Warn("failed to list sync runs", "provider", name, "error", err)
		}

		// Get failed file count
		failedFiles, err := m.store.ListFailedFiles(name)
		failedCount := 0
		if err == nil {
			failedCount = len(failedFiles)
		} else {
			m.logger.Warn("failed to list failed files", "provider", name, "error", err)
		}

		statuses[name] = ProviderStatus{
			Name:        name,
			Enabled:     enabled,
			FileCount:   fileCount,
			TotalSize:   totalSize,
			LastSync:    lastSync,
			LastStatus:  lastStatus,
			FailedFiles: failedCount,
		}
	}

	return statuses
}

// ReconfigureProviders rebuilds the provider registry from the given configs.
// Only enabled providers with a configured factory are instantiated.
// Acquires a write lock to prevent races with running syncs.
func (m *SyncManager) ReconfigureProviders(configs []store.ProviderConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.providerFactory == nil {
		return fmt.Errorf("provider factory not set")
	}

	newRegistry := provider.NewRegistry()

	for _, pc := range configs {
		if !pc.Enabled {
			continue
		}

		p, err := m.providerFactory(pc.Type, m.config.Server.DataDir, m.logger)
		if err != nil {
			m.logger.Warn("skipping provider: failed to instantiate", "name", pc.Name, "type", pc.Type, "error", err)
			continue
		}

		var rawCfg map[string]interface{}
		if err := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); err != nil {
			m.logger.Warn("skipping provider: invalid config JSON", "name", pc.Name, "error", err)
			continue
		}

		if err := p.Configure(rawCfg); err != nil {
			m.logger.Warn("skipping provider: configure failed", "name", pc.Name, "error", err)
			continue
		}

		newRegistry.Register(p)
	}

	// Swap registry contents
	for _, name := range m.registry.Names() {
		m.registry.Remove(name)
	}
	for _, p := range newRegistry.All() {
		m.registry.Register(p)
	}

	// Update config.Providers map so ProviderEnabled() works
	m.config.Providers = make(map[string]config.ProviderConfig)
	for _, pc := range configs {
		var rawCfg map[string]interface{}
		if err := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); err == nil {
			m.config.Providers[pc.Name] = rawCfg
		}
	}

	m.logger.Info("providers reconfigured", "active", len(newRegistry.Names()))
	return nil
}
