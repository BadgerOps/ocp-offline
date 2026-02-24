package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

// handleRedirectDashboard redirects / to /dashboard.
func (s *Server) handleRedirectDashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusMovedPermanently)
}

// handleDashboard renders the dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	statuses := s.engine.Status()

	s.syncMu.Lock()
	syncRunning := s.syncRunning
	s.syncMu.Unlock()

	data := map[string]interface{}{
		"Title":       "Dashboard",
		"Statuses":    statuses,
		"SyncRunning": syncRunning,
	}

	s.renderTemplate(w, "templates/dashboard.html", data)
}

// handleProviders renders the providers list page.
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	providerNames := s.registry.Names()
	statuses := s.engine.Status()

	var providerConfigs []providerConfigJSON
	if s.store != nil {
		configs, err := s.store.ListProviderConfigs()
		if err != nil {
			s.logger.Warn("failed to list provider configs", "error", err)
		} else {
			for _, pc := range configs {
				providerConfigs = append(providerConfigs, dbToJSON(pc))
			}
		}
	}

	data := map[string]interface{}{
		"Title":           "Providers",
		"Providers":       providerNames,
		"Statuses":        statuses,
		"ProviderConfigs": providerConfigs,
	}

	s.renderTemplate(w, "templates/providers.html", data)
}

// handleProviderDetail renders a single provider detail page.
func (s *Server) handleProviderDetail(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("name")
	if providerName == "" {
		http.Error(w, "Provider name required", http.StatusBadRequest)
		return
	}

	// Check registry first, then fall back to store (covers disabled/unsupported providers)
	_, inRegistry := s.registry.Get(providerName)
	if !inRegistry && s.store != nil {
		if _, err := s.store.GetProviderConfig(providerName); err != nil {
			http.Error(w, "Provider not found", http.StatusNotFound)
			return
		}
	}

	statuses := s.engine.Status()
	status := statuses[providerName] // zero value is fine if not in registry

	data := map[string]interface{}{
		"Title":    "Provider: " + providerName,
		"Provider": providerName,
		"Status":   status,
	}

	s.renderTemplate(w, "templates/provider_detail.html", data)
}

// handleSync renders the sync status page.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	statuses := s.engine.Status()

	s.syncMu.Lock()
	syncRunning := s.syncRunning
	s.syncMu.Unlock()

	data := map[string]interface{}{
		"Title":       "Sync Status",
		"Statuses":    statuses,
		"SyncRunning": syncRunning,
	}

	s.renderTemplate(w, "templates/dashboard.html", data)
}

// ProviderStatusJSON is the JSON representation of a provider status.
type ProviderStatusJSON struct {
	Name        string    `json:"name"`
	Enabled     bool      `json:"enabled"`
	FileCount   int       `json:"file_count"`
	TotalSize   int64     `json:"total_size"`
	LastSync    time.Time `json:"last_sync"`
	LastStatus  string    `json:"last_status"`
	FailedFiles int       `json:"failed_files"`
}

// handleAPIStatus returns JSON with all provider statuses.
func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	statuses := s.engine.Status()

	response := make([]ProviderStatusJSON, 0, len(statuses))
	for _, status := range statuses {
		response = append(response, ProviderStatusJSON{
			Name:        status.Name,
			Enabled:     status.Enabled,
			FileCount:   status.FileCount,
			TotalSize:   status.TotalSize,
			LastSync:    status.LastSync,
			LastStatus:  status.LastStatus,
			FailedFiles: status.FailedFiles,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("failed to encode status response", "error", err)
	}
}

// handleAPIProviders returns JSON with provider list and metadata.
func (s *Server) handleAPIProviders(w http.ResponseWriter, r *http.Request) {
	providerNames := s.registry.Names()
	all := s.registry.All()

	type ProviderInfo struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}

	response := make([]ProviderInfo, 0, len(providerNames))
	for _, name := range providerNames {
		_, exists := all[name]
		if exists {
			response = append(response, ProviderInfo{
				Name:    name,
				Enabled: s.config.ProviderEnabled(name),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("failed to encode providers response", "error", err)
	}
}

// SyncRequestBody is the expected request body for POST /api/sync.
type SyncRequestBody struct {
	Provider   string `json:"provider"`
	DryRun     bool   `json:"dry_run"`
	Force      bool   `json:"force"`
	MaxWorkers int    `json:"max_workers"`
}

// SyncResponseBody is the response from POST /api/sync.
type SyncResponseBody struct {
	Provider         string    `json:"provider"`
	Success          bool      `json:"success"`
	Message          string    `json:"message"`
	Downloaded       int       `json:"downloaded,omitempty"`
	Deleted          int       `json:"deleted,omitempty"`
	Skipped          int       `json:"skipped,omitempty"`
	Failed           int       `json:"failed,omitempty"`
	BytesTransferred int64     `json:"bytes_transferred,omitempty"`
	StartTime        time.Time `json:"start_time,omitempty"`
	EndTime          time.Time `json:"end_time,omitempty"`
}

// isHTMX returns true if the request was made by HTMX.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// writeSyncFragment writes an HTML fragment for HTMX sync responses.
func writeSyncFragment(w http.ResponseWriter, success bool, message string) {
	class := "alert-success"
	if !success {
		class = "alert-error"
	}
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<div class="alert %s">%s</div>`, class, html.EscapeString(message))
}

// parseSyncRequest extracts sync parameters from either JSON or form data.
func parseSyncRequest(r *http.Request) (SyncRequestBody, error) {
	var req SyncRequestBody

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, err
		}
		return req, nil
	}

	// HTMX sends form-encoded data from hx-vals
	if err := r.ParseForm(); err != nil {
		return req, err
	}
	req.Provider = r.FormValue("provider")
	req.DryRun = r.FormValue("dry_run") == "true"
	req.Force = r.FormValue("force") == "true"
	return req, nil
}

// handleAPISync triggers a sync for a provider asynchronously.
// Returns immediately with a progress UI (HTMX) or status JSON (API).
func (s *Server) handleAPISync(w http.ResponseWriter, r *http.Request) {
	htmx := isHTMX(r)

	req, err := parseSyncRequest(r)
	if err != nil {
		if htmx {
			writeSyncFragment(w, false, "Invalid request")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		}
		return
	}

	if req.Provider == "" {
		if htmx {
			writeSyncFragment(w, false, "Provider name required")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "provider name required"})
		}
		return
	}

	// Guard against concurrent syncs
	s.syncMu.Lock()
	if s.syncRunning {
		s.syncMu.Unlock()
		if htmx {
			writeSyncFragment(w, false, "A sync is already running")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "sync already running"})
		}
		return
	}

	// Validate provider exists before going async
	if req.Provider != "all" {
		if _, ok := s.registry.Get(req.Provider); !ok {
			s.syncMu.Unlock()
			if htmx {
				writeSyncFragment(w, false, "Provider not found: "+req.Provider)
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "provider not found"})
			}
			return
		}
	}

	opts := provider.SyncOptions{
		DryRun:     req.DryRun,
		Force:      req.Force,
		MaxWorkers: req.MaxWorkers,
	}

	// Use context.Background() so sync survives the HTTP request lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncRunning = true
	s.syncMu.Unlock()

	// Launch sync in background goroutine
	go func() {
		defer func() {
			s.syncMu.Lock()
			s.syncRunning = false
			s.syncCancel = nil
			s.syncMu.Unlock()
		}()

		if req.Provider == "all" {
			_, syncErr := s.engine.SyncAll(ctx, opts)
			if syncErr != nil {
				s.logger.Error("background sync all failed", "error", syncErr)
			}
		} else {
			_, syncErr := s.engine.SyncProvider(ctx, req.Provider, opts)
			if syncErr != nil {
				s.logger.Error("background sync failed", "provider", req.Provider, "error", syncErr)
			}
		}
	}()

	// Return immediately
	if htmx {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, progressComponentHTML(req.Provider))
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "started", "provider": req.Provider})
	}
}

// progressComponentHTML returns an Alpine.js component that connects to the SSE endpoint.
func progressComponentHTML(providerName string) string {
	return `<div x-data="syncProgress()" x-init="start()">
	<div class="alert" :class="alertClass()" style="padding: 16px;">
		<div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
			<span x-text="progress.message || 'Starting sync...'"></span>
			<span class="badge" :class="phaseBadge()" x-text="progress.phase"></span>
		</div>
		<div style="background: rgba(255,255,255,0.15); border-radius: 4px; height: 10px; overflow: hidden; margin-bottom: 8px;">
			<div style="height: 100%; border-radius: 4px; transition: width 0.3s ease; background: var(--accent); box-shadow: 0 0 8px var(--accent-glow); min-width: 2px;"
				:style="{ width: progress.percent > 0 ? progress.percent.toFixed(1) + '%' : '0%' }"></div>
		</div>
		<div style="display: flex; gap: 16px; font-size: 12px; font-family: var(--font-mono); color: var(--text-secondary); flex-wrap: wrap;">
			<span><strong x-text="progress.completed_files"></strong> / <span x-text="progress.total_files - progress.skipped_files"></span> files</span>
			<span x-show="progress.skipped_files > 0" x-text="'(' + progress.skipped_files + ' skipped)'"></span>
			<span x-text="formatBytes(progress.bytes_downloaded)"></span>
			<span x-show="progress.bytes_per_second > 0" x-text="formatBytes(progress.bytes_per_second) + '/s'"></span>
			<span x-text="progress.elapsed"></span>
			<span x-show="progress.eta" x-text="'ETA ' + progress.eta"></span>
			<span x-show="progress.failed_files > 0" style="color: var(--red);" x-text="progress.failed_files + ' failed'"></span>
		</div>
		<template x-if="progress.current_files && progress.current_files.length > 0">
			<div style="margin-top: 10px;">
				<div @click="filesExpanded = !filesExpanded"
					style="cursor: pointer; display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-muted); user-select: none;">
					<span :style="'display: inline-block; transition: transform 0.2s; transform: rotate(' + (filesExpanded ? '90deg' : '0deg') + ')'">&#9654;</span>
					<span x-text="progress.current_files.length + ' active download' + (progress.current_files.length !== 1 ? 's' : '')"></span>
				</div>
				<div x-show="filesExpanded" x-transition
					style="margin-top: 6px; font-size: 11px; font-family: var(--font-mono); color: var(--text-muted); max-height: 200px; overflow-y: auto; padding-left: 4px;">
					<template x-for="f in progress.current_files" :key="f.path">
						<div style="display: flex; justify-content: space-between; gap: 12px; padding: 2px 0; white-space: nowrap;">
							<span style="overflow: hidden; text-overflow: ellipsis;" x-text="f.path"></span>
							<span style="flex-shrink: 0;" x-text="f.total_bytes > 0 ? formatBytes(f.bytes_downloaded) + ' / ' + formatBytes(f.total_bytes) : formatBytes(f.bytes_downloaded)"></span>
						</div>
					</template>
				</div>
			</div>
		</template>
		<template x-if="progress.recent_events && progress.recent_events.length > 0">
			<div style="margin-top: 10px;">
				<div @click="logExpanded = !logExpanded"
					style="cursor: pointer; display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-muted); user-select: none;">
					<span :style="'display: inline-block; transition: transform 0.2s; transform: rotate(' + (logExpanded ? '90deg' : '0deg') + ')'">&#9654;</span>
					<span>Recent activity</span>
				</div>
				<div x-show="logExpanded" x-transition
					style="margin-top: 6px; font-size: 11px; font-family: var(--font-mono); max-height: 200px; overflow-y: auto; padding-left: 4px;">
					<template x-for="ev in progress.recent_events" :key="ev.path + ev.status">
						<div style="display: flex; align-items: center; gap: 8px; padding: 2px 0; white-space: nowrap;">
							<span x-show="ev.status === 'completed'" style="color: var(--green); flex-shrink: 0;">&#10003;</span>
							<span x-show="ev.status === 'failed'" style="color: var(--red); flex-shrink: 0;">&#10007;</span>
							<span style="overflow: hidden; text-overflow: ellipsis; color: var(--text-muted);" x-text="ev.path"></span>
							<span x-show="ev.size > 0" style="flex-shrink: 0; color: var(--text-muted);" x-text="formatBytes(ev.size)"></span>
							<span x-show="ev.error" style="flex-shrink: 0; color: var(--red);" :title="ev.error" x-text="ev.error ? ev.error.substring(0, 60) : ''"></span>
						</div>
					</template>
				</div>
			</div>
		</template>
			<template x-if="(progress.phase === 'complete' || progress.phase === 'failed') && progress.failed_files > 0">
				<div style="margin-top: 12px; padding-top: 12px; border-top: 1px solid rgba(255,255,255,0.1);"
					x-data="failedFilesPanel()" x-init="load(progress.provider)">
				<div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px;">
					<span style="font-size: 13px; font-weight: 600; color: var(--red);" x-text="'Failed Files (' + failures.length + ')'"></span>
					<button class="btn btn-sm" @click="retry(progress.provider)" :disabled="retrying"
						style="font-size: 11px;" x-text="retrying ? 'Retrying...' : 'Retry Failed'"></button>
				</div>
					<div style="font-size: 11px; font-family: var(--font-mono); max-height: 200px; overflow-y: auto;">
						<template x-for="f in failures" :key="f.ID">
							<div style="display: flex; align-items: flex-start; gap: 8px; padding: 3px 0; border-bottom: 1px solid var(--border-subtle);">
								<span style="color: var(--red); flex-shrink: 0;">&#10007;</span>
								<div style="min-width: 0;">
									<div style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-secondary);" x-text="f.FilePath"></div>
									<div style="color: var(--red); font-size: 10px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;" x-text="f.Error"></div>
								</div>
								<button class="btn btn-sm"
									@click="clear(f.ID)"
									:disabled="retrying || clearing[f.ID]"
									style="font-size: 10px; padding: 2px 6px; min-width: 56px;"
									x-text="clearing[f.ID] ? 'Clearing...' : 'Clear'"></button>
							</div>
						</template>
					</div>
				</div>
			</template>
	</div>
</div>`
}

// handleAPISyncFailures returns unresolved failed files for a provider.
func (s *Server) handleAPISyncFailures(w http.ResponseWriter, r *http.Request) {
	providerName := r.URL.Query().Get("provider")
	if providerName == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider query parameter required"})
		return
	}

	records, err := s.store.ListFailedFiles(providerName)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

// handleAPISyncFailureResolve marks one failed file record as resolved.
func (s *Server) handleAPISyncFailureResolve(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	if idStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed file id required"})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid failed file id"})
		return
	}

	if err := s.store.ResolveFailedFile(id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(err.Error(), "not found") {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed file record not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RetryRequestBody is the expected request body for POST /api/sync/retry.
type RetryRequestBody struct {
	Provider string `json:"provider"`
}

// handleAPISyncRetry retries downloading unresolved failed files for a provider.
func (s *Server) handleAPISyncRetry(w http.ResponseWriter, r *http.Request) {
	var req RetryRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	if req.Provider == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider name required"})
		return
	}

	// Guard against concurrent operations
	s.syncMu.Lock()
	if s.syncRunning {
		s.syncMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "a sync is already running"})
		return
	}

	// Fetch unresolved failed files
	records, err := s.store.ListFailedFiles(req.Provider)
	if err != nil {
		s.syncMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if len(records) == 0 {
		s.syncMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "no_failures", "message": "No failed files to retry"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncRunning = true
	s.syncMu.Unlock()

	// Launch retry in background
	go func() {
		defer func() {
			s.syncMu.Lock()
			s.syncRunning = false
			s.syncCancel = nil
			s.syncMu.Unlock()
		}()

		s.retryFailedFiles(ctx, req.Provider, records)
	}()

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, progressComponentHTML(req.Provider))
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "started", "provider": req.Provider, "files": fmt.Sprintf("%d", len(records))})
	}
}

// retryFailedFiles downloads failed files and updates the store.
func (s *Server) retryFailedFiles(ctx context.Context, providerName string, records []store.FailedFileRecord) {
	tracker := engine.NewSyncTracker(providerName)
	tracker.SetMessage(fmt.Sprintf("Retrying %d failed files", len(records)))
	tracker.SetTotals(len(records), 0)
	tracker.SetPhase(engine.PhaseDownloading)

	// Install tracker so SSE picks it up
	s.engine.SetActiveTracker(tracker)
	// NOTE: intentionally NOT clearing the tracker here. It stays set after
	// completion so SSE clients can read the terminal snapshot. It gets
	// replaced when the next sync/validate/retry starts.

	// Build download jobs from failed file records
	jobs := make([]download.Job, 0, len(records))
	recordMap := make(map[string]*store.FailedFileRecord) // key by dest path
	for i := range records {
		rec := &records[i]
		destPath := rec.DestPath
		if destPath == "" {
			// Fallback for records created before dest_path was added
			destPath = rec.FilePath
		}
		jobs = append(jobs, download.Job{
			URL:              rec.URL,
			DestPath:         destPath,
			ExpectedChecksum: rec.ExpectedChecksum,
			ExpectedSize:     rec.ExpectedSize,
		})
		recordMap[destPath] = rec
	}

	pool := download.NewPool(s.engine.Client(), 4, s.logger)
	pool.OnProgress = func(destPath string, bytesDownloaded, totalBytes int64) {
		tracker.UpdateFileProgress(destPath, bytesDownloaded, totalBytes)
	}
	pool.OnComplete = func(destPath string, size int64, success bool, errMsg string) {
		if success {
			tracker.FileCompleted(destPath, size)
		} else {
			tracker.FileFailed(destPath, errMsg)
		}
	}

	results := pool.Execute(ctx, jobs)

	resolved := 0
	stillFailed := 0
	for _, result := range results {
		rec, ok := recordMap[result.Job.DestPath]
		if !ok {
			continue
		}
		if result.Success {
			if err := s.store.ResolveFailedFile(rec.ID); err != nil {
				s.logger.Error("failed to resolve failed file", "id", rec.ID, "error", err)
			}
			resolved++
		} else {
			if err := s.store.IncrementFailedRetry(rec.ID); err != nil {
				s.logger.Error("failed to increment retry count", "id", rec.ID, "error", err)
			}
			stillFailed++
		}
	}

	if stillFailed > 0 {
		tracker.SetPhase(engine.PhaseFailed)
		tracker.SetMessage(fmt.Sprintf("Retry complete: %d resolved, %d still failing", resolved, stillFailed))
	} else {
		tracker.SetPhase(engine.PhaseComplete)
		tracker.SetMessage(fmt.Sprintf("Retry complete: all %d files resolved", resolved))
	}
}

// ScanRequestBody is the expected request body for POST /api/scan.
type ScanRequestBody struct {
	Provider string `json:"provider"`
}

// handleAPIScan triggers a local file scan for a provider asynchronously.
func (s *Server) handleAPIScan(w http.ResponseWriter, r *http.Request) {
	var req ScanRequestBody
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
	} else {
		if err := r.ParseForm(); err == nil {
			req.Provider = r.FormValue("provider")
		}
	}

	if req.Provider == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider name required"})
		return
	}

	// Guard against concurrent operations (reuse sync lock)
	s.syncMu.Lock()
	if s.syncRunning {
		s.syncMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "a sync or scan is already running"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncRunning = true
	s.syncMu.Unlock()

	// Launch scan in background goroutine
	go func() {
		defer func() {
			s.syncMu.Lock()
			s.syncRunning = false
			s.syncCancel = nil
			s.syncMu.Unlock()
		}()

		_, scanErr := s.engine.ScanLocal(ctx, req.Provider)
		if scanErr != nil {
			s.logger.Error("background scan failed", "provider", req.Provider, "error", scanErr)
		}
	}()

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, progressComponentHTML(req.Provider))
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "started", "provider": req.Provider})
	}
}

// ValidateRequestBody is the expected request body for POST /api/validate.
type ValidateRequestBody struct {
	Provider string `json:"provider"`
}

// handleAPIValidate triggers validation for a provider and returns results.
func (s *Server) handleAPIValidate(w http.ResponseWriter, r *http.Request) {
	var req ValidateRequestBody
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}
	} else {
		if err := r.ParseForm(); err == nil {
			req.Provider = r.FormValue("provider")
		}
	}

	if req.Provider == "" {
		if isHTMX(r) {
			writeSyncFragment(w, false, "Provider name required")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "provider name required"})
		}
		return
	}

	// Guard against concurrent operations
	s.syncMu.Lock()
	if s.syncRunning {
		s.syncMu.Unlock()
		if isHTMX(r) {
			writeSyncFragment(w, false, "A sync or validation is already running")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "a sync or validation is already running"})
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncRunning = true
	s.syncMu.Unlock()

	// Launch validation in background goroutine
	go func() {
		defer func() {
			s.syncMu.Lock()
			s.syncRunning = false
			s.syncCancel = nil
			s.syncMu.Unlock()
		}()

		s.runValidation(ctx, req.Provider)
	}()

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, progressComponentHTML(req.Provider))
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "started", "provider": req.Provider})
	}
}

// runValidation validates a provider's local files against upstream manifest.
func (s *Server) runValidation(ctx context.Context, providerName string) {
	tracker := engine.NewSyncTracker(providerName)
	tracker.SetMessage("Fetching manifest for " + providerName + "...")
	tracker.SetPhase(engine.PhaseDownloading)
	s.engine.SetActiveTracker(tracker)
	// NOTE: intentionally NOT clearing the tracker here. It stays set after
	// completion so SSE clients can read the terminal snapshot. It gets
	// replaced when the next sync/validate/retry starts.

	// Wire up per-file progress if the provider supports it
	if p, ok := s.registry.Get(providerName); ok {
		if vps, ok := p.(provider.ValidationProgressSetter); ok {
			vps.SetValidationProgress(func(checked, total int, path string, valid bool) {
				// Set totals on first callback (we don't know total until provider parses manifest)
				if checked == 1 {
					tracker.SetTotals(total, 0)
					tracker.SetMessage(fmt.Sprintf("Validating %d files...", total))
				}
				if valid {
					tracker.FileCompleted(path, 0)
				} else {
					tracker.FileFailed(path, "checksum mismatch or missing")
				}
			})
			// Clear callback when done to avoid holding references
			defer vps.SetValidationProgress(nil)
		}
	}

	report, err := s.engine.ValidateProvider(ctx, providerName)
	if err != nil {
		tracker.SetPhase(engine.PhaseFailed)
		tracker.SetMessage("Validation failed: " + err.Error())
		return
	}

	// Persist invalid files to failed_files table so they survive page refresh
	if len(report.InvalidFiles) > 0 && s.store != nil {
		for _, inv := range report.InvalidFiles {
			reason := "validation: checksum mismatch"
			if inv.Actual == "missing" {
				reason = "validation: file missing"
			}
			rec := &store.FailedFileRecord{
				Provider:         providerName,
				FilePath:         inv.Path,
				URL:              inv.URL,
				DestPath:         inv.LocalPath,
				ExpectedChecksum: inv.Expected,
				ExpectedSize:     inv.Size,
				Error:            reason,
				RetryCount:       0,
				FirstFailure:     time.Now(),
				LastFailure:      time.Now(),
				Resolved:         false,
			}
			if err := s.store.AddFailedFile(rec); err != nil {
				s.logger.Warn("failed to persist validation failure", "path", inv.Path, "error", err)
			}
		}
	}

	if len(report.InvalidFiles) > 0 {
		tracker.SetPhase(engine.PhaseFailed)
		tracker.SetMessage(fmt.Sprintf("Validation complete: %d valid, %d invalid of %d total",
			report.ValidFiles, len(report.InvalidFiles), report.TotalFiles))
	} else {
		tracker.SetPhase(engine.PhaseComplete)
		tracker.SetMessage(fmt.Sprintf("Validation passed: all %d files match checksums", report.TotalFiles))
	}
}

// handleAPISyncCancel cancels any running sync operation.
func (s *Server) handleAPISyncCancel(w http.ResponseWriter, r *http.Request) {
	s.syncMu.Lock()
	cancel := s.syncCancel
	s.syncMu.Unlock()

	if cancel == nil {
		if isHTMX(r) {
			writeSyncFragment(w, false, "No sync is currently running")
		} else {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "no sync running"})
		}
		return
	}

	cancel()
	s.logger.Info("sync cancelled by user")

	if isHTMX(r) {
		writeSyncFragment(w, true, "Sync cancelled")
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}
}

// handleAPISyncRunning returns whether a sync/scan is currently running.
func (s *Server) handleAPISyncRunning(w http.ResponseWriter, r *http.Request) {
	s.syncMu.Lock()
	running := s.syncRunning
	s.syncMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"running": running})
}

// handleSyncProgress streams SSE events with sync progress snapshots.
func (s *Server) handleSyncProgress(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	// Wait up to 2s for a non-terminal tracker to appear.
	// A stale tracker from a previous operation may still be set (we don't clear
	// on completion). If syncRunning is true but the tracker shows a terminal
	// phase, a new operation is starting — keep waiting for the fresh tracker.
	var tracker *engine.SyncTracker
	for i := 0; i < 20; i++ {
		tracker = s.engine.ActiveProgress()
		if tracker != nil {
			snap := tracker.Snapshot()
			isTerminal := snap.Phase == "complete" || snap.Phase == "failed" || snap.Phase == "cancelled"
			if !isTerminal {
				break // found a live tracker
			}
			// Terminal tracker — check if a new operation is starting
			s.syncMu.Lock()
			running := s.syncRunning
			s.syncMu.Unlock()
			if !running {
				break // no new operation, show the terminal state
			}
			// New operation starting but tracker not replaced yet — wait
			tracker = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if tracker == nil {
		fmt.Fprintf(w, "event: done\ndata: {\"phase\":\"complete\",\"message\":\"No sync running\",\"total_files\":0,\"completed_files\":0,\"failed_files\":0,\"skipped_files\":0,\"total_bytes\":0,\"bytes_downloaded\":0,\"bytes_per_second\":0,\"percent\":100,\"elapsed\":\"0s\",\"eta\":\"\",\"provider\":\"\",\"current_files\":[],\"recent_events\":[],\"total_retries\":0}\n\n")
		flusher.Flush()
		return
	}

	for {
		tracker = s.engine.ActiveProgress()
		if tracker == nil {
			// Tracker was cleared — send final idle event and close
			fmt.Fprintf(w, "event: done\ndata: {\"phase\":\"complete\",\"message\":\"Sync finished\",\"total_files\":0,\"completed_files\":0,\"failed_files\":0,\"skipped_files\":0,\"total_bytes\":0,\"bytes_downloaded\":0,\"bytes_per_second\":0,\"percent\":100,\"elapsed\":\"0s\",\"eta\":\"\",\"provider\":\"\",\"current_files\":[],\"recent_events\":[],\"total_retries\":0}\n\n")
			flusher.Flush()
			return
		}

		snap := tracker.Snapshot()
		data, err := json.Marshal(snap)
		if err != nil {
			s.logger.Error("failed to marshal progress", "error", err)
			return
		}

		// Use "done" event for terminal phases so client closes EventSource
		if snap.Phase == "complete" || snap.Phase == "failed" || snap.Phase == "cancelled" {
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", data)
			flusher.Flush()
			return
		}

		fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
		flusher.Flush()

		// Wait for next update or heartbeat timeout
		waitCh := tracker.Wait()
		select {
		case <-ctx.Done():
			return
		case <-waitCh:
			// New update available, loop
		case <-time.After(5 * time.Second):
			// Heartbeat: send comment to keep connection alive
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
