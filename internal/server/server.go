package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/provider"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server represents the HTTP server for the airgap web UI.
type Server struct {
	engine    *engine.SyncManager
	registry  *provider.Registry
	config    *config.Config
	logger    *slog.Logger
	httpServer *http.Server
	templates *template.Template
}

// NewServer creates a new Server instance.
func NewServer(
	eng *engine.SyncManager,
	reg *provider.Registry,
	cfg *config.Config,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		engine:   eng,
		registry: reg,
		config:   cfg,
		logger:   logger,
	}
}

// Start starts the HTTP server on the given listen address.
func (s *Server) Start(listenAddr string) error {
	// Parse and load templates with custom functions
	var err error
	s.templates, err = template.New("").Funcs(initializeTemplateFuncs()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}

	// Setup routes
	mux := s.setupRoutes()

	// Create and start HTTP server
	s.httpServer = &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
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

	// Root redirect
	mux.HandleFunc("GET /{$}", s.handleRedirectDashboard)

	return mux
}
