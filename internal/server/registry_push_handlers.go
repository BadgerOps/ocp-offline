package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/BadgerOps/airgap/internal/engine"
)

// RegistryPushRequest is the expected request body for POST /api/registry/push.
type RegistryPushRequest struct {
	SourceProvider string `json:"source_provider"`
	TargetProvider string `json:"target_provider"`
	DryRun         bool   `json:"dry_run"`
}

func parseRegistryPushRequest(r *http.Request) (RegistryPushRequest, error) {
	var req RegistryPushRequest
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, err
		}
		return req, nil
	}

	if err := r.ParseForm(); err != nil {
		return req, err
	}
	req.SourceProvider = r.FormValue("source_provider")
	req.TargetProvider = r.FormValue("target_provider")
	req.DryRun = r.FormValue("dry_run") == "true" || r.FormValue("dry_run") == "on" || r.FormValue("dry_run") == "1"
	return req, nil
}

// handleAPIRegistryPush starts an asynchronous registry push operation.
func (s *Server) handleAPIRegistryPush(w http.ResponseWriter, r *http.Request) {
	htmx := isHTMX(r)

	req, err := parseRegistryPushRequest(r)
	if err != nil {
		if htmx {
			writeSyncFragment(w, false, "Invalid request")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		}
		return
	}

	if strings.TrimSpace(req.SourceProvider) == "" || strings.TrimSpace(req.TargetProvider) == "" {
		if htmx {
			writeSyncFragment(w, false, "Source and target provider are required")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "source_provider and target_provider are required"})
		}
		return
	}

	// Guard against concurrent operations.
	s.syncMu.Lock()
	if s.syncRunning {
		s.syncMu.Unlock()
		if htmx {
			writeSyncFragment(w, false, "A sync/push operation is already running")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "operation already running"})
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncRunning = true
	s.syncMu.Unlock()

	// Launch registry push in background.
	go func() {
		defer func() {
			s.syncMu.Lock()
			s.syncRunning = false
			s.syncCancel = nil
			s.syncMu.Unlock()
		}()

		tracker := engine.NewSyncTracker(req.SourceProvider)
		tracker.SetPhase(engine.PhaseDownloading)
		tracker.SetTotals(1, 0)
		tracker.SetMessage(fmt.Sprintf("Pushing images from %s to %s...", req.SourceProvider, req.TargetProvider))
		s.engine.SetActiveTracker(tracker)

		report, pushErr := s.engine.PushContainerImages(ctx, engine.RegistryPushOptions{
			SourceProvider: req.SourceProvider,
			TargetProvider: req.TargetProvider,
			DryRun:         req.DryRun,
		})
		if pushErr != nil {
			tracker.SetPhase(engine.PhaseFailed)
			msg := "Registry push failed"
			if report != nil {
				msg = fmt.Sprintf("Registry push failed: %d/%d images pushed", report.ImagesPushed, report.ImagesTotal)
			}
			tracker.SetMessage(msg)
			s.logger.Error("registry push failed", "source_provider", req.SourceProvider, "target_provider", req.TargetProvider, "error", pushErr)
			return
		}

		tracker.FileCompleted("registry-push", 0)
		tracker.SetPhase(engine.PhaseComplete)
		if req.DryRun {
			tracker.SetMessage(fmt.Sprintf("Dry run complete: %d image(s) planned", report.ImagesTotal))
		} else {
			tracker.SetMessage(fmt.Sprintf("Registry push complete: %d/%d image(s) pushed", report.ImagesPushed, report.ImagesTotal))
		}
	}()

	if htmx {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, progressComponentHTML(req.SourceProvider))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":          "started",
		"source_provider": req.SourceProvider,
		"target_provider": req.TargetProvider,
	})
}
