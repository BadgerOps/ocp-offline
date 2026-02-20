package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/BadgerOps/airgap/internal/provider"
)

// handleRedirectDashboard redirects / to /dashboard.
func (s *Server) handleRedirectDashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusMovedPermanently)
}

// handleDashboard renders the dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	statuses := s.engine.Status()

	data := map[string]interface{}{
		"Title":    "Dashboard",
		"Statuses": statuses,
	}

	if err := s.templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("failed to render dashboard", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
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

	if err := s.templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("failed to render providers", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// handleProviderDetail renders a single provider detail page.
func (s *Server) handleProviderDetail(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("name")
	if providerName == "" {
		http.Error(w, "Provider name required", http.StatusBadRequest)
		return
	}

	// Verify provider exists
	_, ok := s.registry.Get(providerName)
	if !ok {
		http.Error(w, "Provider not found", http.StatusNotFound)
		return
	}

	statuses := s.engine.Status()
	status, hasStatus := statuses[providerName]
	if !hasStatus {
		http.Error(w, "Provider status not available", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Title":    "Provider: " + providerName,
		"Provider": providerName,
		"Status":   status,
	}

	if err := s.templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("failed to render provider detail", "error", err, "provider", providerName)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

// handleSync renders the sync status page.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	statuses := s.engine.Status()

	data := map[string]interface{}{
		"Title":    "Sync Status",
		"Statuses": statuses,
	}

	if err := s.templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("failed to render sync page", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
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
	Provider        string    `json:"provider"`
	Success         bool      `json:"success"`
	Message         string    `json:"message"`
	Downloaded      int       `json:"downloaded,omitempty"`
	Deleted         int       `json:"deleted,omitempty"`
	Skipped         int       `json:"skipped,omitempty"`
	Failed          int       `json:"failed,omitempty"`
	BytesTransferred int64     `json:"bytes_transferred,omitempty"`
	StartTime       time.Time `json:"start_time,omitempty"`
	EndTime         time.Time `json:"end_time,omitempty"`
}

// handleAPISync triggers a sync for a provider and returns JSON result.
func (s *Server) handleAPISync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequestBody
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

	// Verify provider exists
	_, ok := s.registry.Get(req.Provider)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider not found"})
		return
	}

	// Build sync options
	opts := provider.SyncOptions{
		DryRun:     req.DryRun,
		Force:      req.Force,
		MaxWorkers: req.MaxWorkers,
	}

	// Execute sync
	ctx := r.Context()
	report, err := s.engine.SyncProvider(ctx, req.Provider, opts)

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(SyncResponseBody{
			Provider: req.Provider,
			Success:  false,
			Message:  err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SyncResponseBody{
		Provider:        report.Provider,
		Success:         true,
		Message:         "sync completed",
		Downloaded:      report.Downloaded,
		Deleted:         report.Deleted,
		Skipped:         report.Skipped,
		Failed:          len(report.Failed),
		BytesTransferred: report.BytesTransferred,
		StartTime:       report.StartTime,
		EndTime:         report.EndTime,
	})
}
