package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/mirror"
	"github.com/BadgerOps/airgap/internal/ocp"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server represents the HTTP server for the airgap web UI.
type Server struct {
	engine     *engine.SyncManager
	registry   *provider.Registry
	store      *store.Store
	config     *config.Config
	logger     *slog.Logger
	discovery  *mirror.Discovery
	ocpClients *ocp.ClientService
	httpServer *http.Server
	templates  map[string]*template.Template

	// Active sync state
	syncMu      sync.Mutex
	syncCancel  context.CancelFunc
	syncRunning bool
}

// NewServer creates a new Server instance.
func NewServer(
	eng *engine.SyncManager,
	reg *provider.Registry,
	st *store.Store,
	cfg *config.Config,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	discovery := mirror.NewDiscovery(logger)
	return &Server{
		engine:     eng,
		registry:   reg,
		store:      st,
		config:     cfg,
		logger:     logger,
		discovery:  discovery,
		ocpClients: ocp.NewClientService(logger),
	}
}

// Start starts the HTTP server on the given listen address.
func (s *Server) Start(listenAddr string) error {
	// Parse each page template paired with layout.html so each gets its own "content" block.
	if err := s.parseTemplates(); err != nil {
		return err
	}

	// Setup routes
	mux := s.setupRoutes()

	// Create and start HTTP server
	s.httpServer = &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}

	s.logger.Info("starting HTTP server", "addr", listenAddr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	s.logger.Info("shutting down HTTP server")
	return s.httpServer.Shutdown(ctx)
}

// parseTemplates parses each page template paired with layout.html
// so that each page gets its own "content" block definition.
func (s *Server) parseTemplates() error {
	funcs := initializeTemplateFuncs()
	s.templates = make(map[string]*template.Template)

	// Each page template is parsed together with layout.html
	pages := []string{
		"templates/dashboard.html",
		"templates/providers.html",
		"templates/provider_detail.html",
		"templates/transfer.html",
		"templates/ocp_clients.html",
	}

	for _, page := range pages {
		t, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", page)
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		s.templates[page] = t
	}

	return nil
}

// renderTemplate executes the named page template with the given data.
func (s *Server) renderTemplate(w http.ResponseWriter, page string, data interface{}) {
	t, ok := s.templates[page]
	if !ok {
		s.logger.Error("template not found", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("failed to render template", "page", page, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// setupRoutes registers all HTTP routes on a new ServeMux.
// Uses Go 1.22+ enhanced routing with method prefixes and path variables.
func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Page routes
	mux.HandleFunc("GET /dashboard", s.handleDashboard)
	mux.HandleFunc("GET /providers/{name}", s.handleProviderDetail)
	mux.HandleFunc("GET /providers", s.handleProviders)
	mux.HandleFunc("GET /sync", s.handleSync)

	// API routes
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /api/providers", s.handleAPIProviders)
	mux.HandleFunc("POST /api/sync", s.handleAPISync)
	mux.HandleFunc("POST /api/sync/cancel", s.handleAPISyncCancel)
	mux.HandleFunc("GET /api/sync/progress", s.handleSyncProgress)
	mux.HandleFunc("GET /api/sync/running", s.handleAPISyncRunning)
	mux.HandleFunc("POST /api/scan", s.handleAPIScan)
	mux.HandleFunc("POST /api/validate", s.handleAPIValidate)
	mux.HandleFunc("GET /api/sync/failures", s.handleAPISyncFailures)
	mux.HandleFunc("DELETE /api/sync/failures/{id}", s.handleAPISyncFailureResolve)
	mux.HandleFunc("POST /api/sync/retry", s.handleAPISyncRetry)

	// Provider config CRUD routes
	mux.HandleFunc("GET /api/providers/config", s.handleListProviderConfigs)
	mux.HandleFunc("POST /api/providers/config", s.handleCreateProviderConfig)
	mux.HandleFunc("PUT /api/providers/config/{name}", s.handleUpdateProviderConfig)
	mux.HandleFunc("DELETE /api/providers/config/{name}", s.handleDeleteProviderConfig)
	mux.HandleFunc("POST /api/providers/config/{name}/toggle", s.handleToggleProviderConfig)

	// Transfer routes
	mux.HandleFunc("GET /transfer", s.handleTransfer)
	mux.HandleFunc("POST /api/transfer/export", s.handleAPITransferExport)
	mux.HandleFunc("POST /api/transfer/import", s.handleAPITransferImport)
	mux.HandleFunc("GET /api/transfers", s.handleAPITransfers)

	// Mirror discovery routes
	mux.HandleFunc("GET /api/mirrors/epel/versions", s.handleEPELVersions)
	mux.HandleFunc("GET /api/mirrors/epel", s.handleEPELMirrors)
	mux.HandleFunc("GET /api/mirrors/ocp/versions", s.handleOCPVersions)
	mux.HandleFunc("POST /api/mirrors/speedtest", s.handleSpeedTest)

	// OCP client downloads
	mux.HandleFunc("GET /ocp/clients", s.handleOCPClients)
	mux.HandleFunc("GET /api/ocp/tracks", s.handleAPIOCPTracks)
	mux.HandleFunc("GET /api/ocp/releases", s.handleAPIOCPReleases)
	mux.HandleFunc("GET /api/ocp/artifacts", s.handleAPIOCPArtifacts)
	mux.HandleFunc("POST /api/ocp/download", s.handleAPIOCPDownload)

	// Root redirect
	mux.HandleFunc("GET /{$}", s.handleRedirectDashboard)

	return mux
}
