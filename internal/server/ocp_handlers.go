package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/ocp"
	"github.com/BadgerOps/airgap/internal/safety"
)

// handleOCPClients renders the OCP client downloads page.
func (s *Server) handleOCPClients(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title": "OCP Clients",
	}
	s.renderTemplate(w, "templates/ocp_clients.html", data)
}

// handleAPIOCPTracks returns available OCP channels grouped by track type.
func (s *Server) handleAPIOCPTracks(w http.ResponseWriter, r *http.Request) {
	result, err := s.ocpClients.FetchTracks(r.Context())
	if err != nil {
		s.logger.Error("failed to fetch OCP tracks", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	s.writeJSON(w, result)
}

// handleAPIOCPReleases returns patch versions for a given OCP channel.
func (s *Server) handleAPIOCPReleases(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]string{"error": "channel query parameter is required"})
		return
	}

	result, err := s.ocpClients.FetchReleases(r.Context(), channel)
	if err != nil {
		s.logger.Error("failed to fetch OCP releases", "channel", channel, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	s.writeJSON(w, result)
}

// handleAPIOCPArtifacts returns download URLs for OCP client binaries,
// fetched from the sha256sum.txt manifest for the given version.
func (s *Server) handleAPIOCPArtifacts(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("version")
	if version == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]string{"error": "version query parameter is required"})
		return
	}

	manifest, err := s.ocpClients.FetchManifest(r.Context(), version)
	if err != nil {
		s.logger.Error("failed to fetch OCP manifest", "version", version, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	s.writeJSON(w, manifest.Artifacts)
}

// OCPDownloadRequest is the request body for POST /api/ocp/download.
type OCPDownloadRequest struct {
	Version   string   `json:"version"`
	Artifacts []string `json:"artifacts"` // list of artifact names to download
}

// OCPDownloadStatus tracks the status of each artifact download.
type OCPDownloadStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pending", "downloading", "done", "error"
	Error  string `json:"error,omitempty"`
	Path   string `json:"path,omitempty"`
}

// handleAPIOCPDownload triggers server-side download of selected OCP artifacts
// to the data directory. Streams SSE progress events.
func (s *Server) handleAPIOCPDownload(w http.ResponseWriter, r *http.Request) {
	var req OCPDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]string{"error": "invalid request body"})
		return
	}

	if req.Version == "" || len(req.Artifacts) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]string{"error": "version and artifacts are required"})
		return
	}
	cleanVersion, err := safety.CleanRelativePath(req.Version)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]string{"error": "invalid version path"})
		return
	}
	req.Version = filepath.ToSlash(cleanVersion)

	// Fetch manifest to get correct filenames and checksums
	manifest, err := s.ocpClients.FetchManifest(r.Context(), req.Version)
	if err != nil {
		s.logger.Error("failed to fetch manifest for download", "version", req.Version, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(w, map[string]string{"error": "failed to fetch manifest: " + err.Error()})
		return
	}

	// Build artifact lookup from manifest
	artifactMap := make(map[string]ocp.ClientArtifact, len(manifest.Artifacts))
	for _, a := range manifest.Artifacts {
		artifactMap[a.Name] = a
	}

	// Validate requested artifacts
	var toDownload []ocp.ClientArtifact
	for _, name := range req.Artifacts {
		a, ok := artifactMap[name]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			s.writeJSON(w, map[string]string{"error": fmt.Sprintf("unknown artifact: %s", name)})
			return
		}
		toDownload = append(toDownload, a)
	}

	// Determine output directory: {dataDir}/ocp-clients/{version}/
	clientsRoot, err := safety.SafeJoinUnder(s.config.Server.DataDir, "ocp-clients")
	if err != nil {
		s.logger.Error("invalid OCP clients output root", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(w, map[string]string{"error": "invalid server data directory configuration"})
		return
	}
	destDir, err := safety.SafeJoinUnder(clientsRoot, req.Version)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		s.writeJSON(w, map[string]string{"error": "invalid version path"})
		return
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		s.logger.Error("failed to create OCP download dir", "dir", destDir, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		s.writeJSON(w, map[string]string{"error": "failed to create download directory"})
		return
	}

	// Stream SSE events
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	sendEvent := func(event string, data interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
		flusher.Flush()
	}

	// Initialize status for all artifacts
	statuses := make([]OCPDownloadStatus, len(toDownload))
	for i, a := range toDownload {
		statuses[i] = OCPDownloadStatus{Name: a.Name, Status: "pending"}
	}
	sendEvent("init", statuses)

	// Download each artifact
	client := download.NewClient(s.logger)
	ctx := r.Context()

	var wg sync.WaitGroup
	var mu sync.Mutex
	// Use a semaphore to limit concurrent downloads to avoid TLS congestion
	sem := make(chan struct{}, 2)

	for i, a := range toDownload {
		wg.Add(1)
		go func(idx int, artifact ocp.ClientArtifact) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			destPath, err := safety.SafeJoinUnder(destDir, artifact.Name)
			if err != nil {
				mu.Lock()
				statuses[idx].Status = "error"
				statuses[idx].Error = "unsafe artifact path"
				sendEvent("progress", statuses)
				mu.Unlock()
				s.logger.Warn("rejecting OCP artifact with unsafe path", "name", artifact.Name, "error", err)
				return
			}

			mu.Lock()
			statuses[idx].Status = "downloading"
			sendEvent("progress", statuses)
			mu.Unlock()

			_, err = client.Download(ctx, download.DownloadOptions{
				URL:              artifact.URL,
				DestPath:         destPath,
				ExpectedChecksum: artifact.Checksum,
				RetryCount:       3,
			})

			mu.Lock()
			if err != nil {
				statuses[idx].Status = "error"
				statuses[idx].Error = err.Error()
				s.logger.Error("OCP artifact download failed",
					"name", artifact.Name, "version", req.Version, "error", err)
			} else {
				statuses[idx].Status = "done"
				statuses[idx].Path = destPath
				s.logger.Info("OCP artifact downloaded",
					"name", artifact.Name, "version", req.Version, "path", destPath)
			}
			sendEvent("progress", statuses)
			mu.Unlock()
		}(i, a)
	}

	wg.Wait()

	sendEvent("done", statuses)
}
