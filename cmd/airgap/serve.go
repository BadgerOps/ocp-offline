package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BadgerOps/airgap/internal/server"
	"github.com/spf13/cobra"
)

var (
	serveListen string
	serveDev    bool
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server for content distribution",
		Long: `Start the HTTP server for serving synchronized content. The server provides
a web UI dashboard and REST API endpoints for managing offline content.

By default, the server listens on the address configured in the config file
(default: 0.0.0.0:8080). Use --listen to override.`,
		Example: `  airgap serve
  airgap serve --listen 127.0.0.1:9000
  airgap serve --dev`,
		RunE: serveRun,
	}

	defaultListen := "0.0.0.0:8080"
	if globalCfg != nil {
		defaultListen = globalCfg.Server.Listen
	}

	cmd.Flags().StringVar(&serveListen, "listen", defaultListen, "address to listen on (host:port)")
	cmd.Flags().BoolVar(&serveDev, "dev", false, "enable development mode with debug logging")

	return cmd
}

func serveRun(cmd *cobra.Command, args []string) error {
	log := slog.Default()

	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	if globalEngine == nil {
		return fmt.Errorf("sync engine not initialized")
	}

	log.Info("server starting", "listen", serveListen, "dev_mode", serveDev, "data_dir", globalCfg.Server.DataDir)

	// Create the HTTP server
	srv := server.NewServer(globalEngine, globalRegistry, globalStore, globalCfg, logger)
	srv.SetVersion(version)

	// Channel to listen for errors from server
	errChan := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		fmt.Printf("Starting server on %s...\n", serveListen)
		if err := srv.Start(serveListen); err != nil {
			errChan <- err
		}
	}()

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for either an error or a shutdown signal
	select {
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	case sig := <-sigChan:
		log.Info("received shutdown signal", "signal", sig)
		fmt.Println("\nShutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}

		fmt.Println("Server stopped gracefully")
	}

	return nil
}
