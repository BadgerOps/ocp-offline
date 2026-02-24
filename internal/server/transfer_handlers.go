package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/BadgerOps/airgap/internal/engine"
)

// handleTransfer renders the transfer wizard page.
func (s *Server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	providerNames := s.registry.Names()

	var transfers []transferJSON
	if s.store != nil {
		dbTransfers, err := s.store.ListTransfers(20)
		if err != nil {
			s.logger.Warn("failed to list transfers", "error", err)
		} else {
			for _, t := range dbTransfers {
				transfers = append(transfers, transferJSON{
					ID:           t.ID,
					Direction:    t.Direction,
					Path:         t.Path,
					Providers:    t.Providers,
					ArchiveCount: t.ArchiveCount,
					TotalSize:    t.TotalSize,
					Status:       t.Status,
					ErrorMessage: t.ErrorMessage,
					StartTime:    t.StartTime,
					EndTime:      t.EndTime,
				})
			}
		}
	}

	data := map[string]interface{}{
		"Title":     "Transfer",
		"Providers": providerNames,
		"Transfers": transfers,
	}

	s.renderTemplate(w, "templates/transfer.html", data)
}

// transferJSON is the JSON representation of a transfer.
type transferJSON struct {
	ID           int64     `json:"id"`
	Direction    string    `json:"direction"`
	Path         string    `json:"path"`
	Providers    string    `json:"providers"`
	ArchiveCount int       `json:"archive_count"`
	TotalSize    int64     `json:"total_size"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
}

// handleAPITransfers returns JSON list of recent transfers.
func (s *Server) handleAPITransfers(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]transferJSON{})
		return
	}

	dbTransfers, err := s.store.ListTransfers(50)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	transfers := make([]transferJSON, 0, len(dbTransfers))
	for _, t := range dbTransfers {
		transfers = append(transfers, transferJSON{
			ID:           t.ID,
			Direction:    t.Direction,
			Path:         t.Path,
			Providers:    t.Providers,
			ArchiveCount: t.ArchiveCount,
			TotalSize:    t.TotalSize,
			Status:       t.Status,
			ErrorMessage: t.ErrorMessage,
			StartTime:    t.StartTime,
			EndTime:      t.EndTime,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transfers)
}

// handleAPITransferExport handles an export request from the transfer form.
// Returns an HTML fragment for HTMX swap.
func (s *Server) handleAPITransferExport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeTransferFragment(w, false, "Invalid form data: "+err.Error())
		return
	}

	outputDir := r.FormValue("output_dir")
	if outputDir == "" {
		writeTransferFragment(w, false, "Output directory is required")
		return
	}

	providers := r.Form["providers"]
	if len(providers) == 0 {
		writeTransferFragment(w, false, "At least one provider must be selected")
		return
	}

	splitSize := int64(4 * 1024 * 1024 * 1024) // 4GB default

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	report, err := s.engine.Export(ctx, engine.ExportOptions{
		OutputDir:   outputDir,
		Providers:   providers,
		SplitSize:   splitSize,
		Compression: "zstd",
	})
	if err != nil {
		writeTransferFragment(w, false, "Export failed: "+err.Error())
		return
	}

	msg := fmt.Sprintf("Export completed: %d archives, %d files, %s",
		len(report.Archives), report.TotalFiles, formatBytes(report.TotalSize))
	writeTransferFragment(w, true, msg)
}

// handleAPITransferImport handles an import request from the transfer form.
// Returns an HTML fragment for HTMX swap.
func (s *Server) handleAPITransferImport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeTransferFragment(w, false, "Invalid form data: "+err.Error())
		return
	}

	sourceDir := r.FormValue("source_dir")
	if sourceDir == "" {
		writeTransferFragment(w, false, "Source directory is required")
		return
	}

	verifyOnly := r.FormValue("verify_only") == "on"
	force := r.FormValue("force") == "on"
	skipValidated := r.FormValue("skip_validated") == "on"

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	report, err := s.engine.Import(ctx, engine.ImportOptions{
		SourceDir:     sourceDir,
		VerifyOnly:    verifyOnly,
		Force:         force,
		SkipValidated: skipValidated,
	})
	if err != nil {
		errMsg := "Import failed: " + err.Error()
		if report != nil {
			errMsg += fmt.Sprintf(" (validated: %d, failed: %d)", report.ArchivesValidated, report.ArchivesFailed)
		}
		writeTransferFragment(w, false, errMsg)
		return
	}

	msg := fmt.Sprintf("Import completed: %d archives validated, %d files extracted, %s",
		report.ArchivesValidated, report.FilesExtracted, formatBytes(report.TotalSize))
	if report.ArchivesSkipped > 0 {
		msg += fmt.Sprintf(", %d archives skipped", report.ArchivesSkipped)
	}
	writeTransferFragment(w, true, msg)
}

// writeTransferFragment writes an HTML fragment for HTMX swap.
func writeTransferFragment(w http.ResponseWriter, success bool, message string) {
	w.Header().Set("Content-Type", "text/html")
	class := "success"
	icon := "&#10003;"
	if !success {
		class = "error"
		icon = "&#10007;"
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	fmt.Fprintf(w, `<div class="alert alert-%s">%s %s</div>`, class, icon, html.EscapeString(message))
}
