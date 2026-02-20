package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/provider/epel"
	"github.com/BadgerOps/airgap/internal/provider/ocp"
	"github.com/BadgerOps/airgap/internal/store"
	"github.com/spf13/cobra"
)

var (
	// Global flags
	cfgPath   string
	dataDir   string
	logLevel  string
	logFormat string
	quiet     bool
	globalCfg *config.Config
	logger    *slog.Logger

	// Global components
	globalStore    *store.Store
	globalEngine   *engine.SyncManager
	globalRegistry *provider.Registry
)

// initializeComponents initializes the global store, client, registry, and engine
func initializeComponents() error {
	if globalCfg == nil {
		return fmt.Errorf("config not loaded")
	}

	// Initialize store
	dbPath := globalCfg.Server.DBPath
	if dbPath == "" {
		dbPath = "/var/lib/airgap/airgap.db"
	}
	st, err := store.New(dbPath, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize store: %w", err)
	}
	globalStore = st

	// Initialize download client
	client := download.NewClient(logger)

	// Initialize provider registry
	globalRegistry = provider.NewRegistry()

	// Register OCP binaries provider
	binariesProvider := ocp.NewBinariesProvider(globalCfg.Server.DataDir, logger)
	if rawCfg, ok := globalCfg.Providers["ocp_binaries"]; ok {
		if err := binariesProvider.Configure(rawCfg); err != nil {
			logger.Warn("failed to configure OCP binaries provider", "error", err)
		}
	}
	globalRegistry.Register(binariesProvider)

	// Register RHCOS provider
	rhcosProvider := ocp.NewRHCOSProvider(globalCfg.Server.DataDir, logger)
	if rawCfg, ok := globalCfg.Providers["rhcos"]; ok {
		if err := rhcosProvider.Configure(rawCfg); err != nil {
			logger.Warn("failed to configure RHCOS provider", "error", err)
		}
	}
	globalRegistry.Register(rhcosProvider)

	// Register EPEL provider
	epelProvider := epel.NewEPELProvider(globalCfg.Server.DataDir, logger)
	if rawCfg, ok := globalCfg.Providers["epel"]; ok {
		if err := epelProvider.Configure(rawCfg); err != nil {
			logger.Warn("failed to configure EPEL provider", "error", err)
		}
	}
	globalRegistry.Register(epelProvider)

	// Initialize sync manager
	globalEngine = engine.NewSyncManager(globalRegistry, globalStore, client, globalCfg, logger)

	logger.Info("components initialized successfully")
	return nil
}

// shouldSkipComponentInit checks if a command should skip component initialization
func shouldSkipComponentInit(cmdName string) bool {
	skipInitCmds := map[string]bool{
		"help":    true,
		"version": true,
		"config":  true,
	}
	return skipInitCmds[cmdName]
}

// closeStore closes the global store connection
func closeStore() {
	if globalStore != nil {
		if err := globalStore.Close(); err != nil {
			logger.Error("failed to close store", "error", err)
		}
	}
}

// NewRootCmd creates and returns the root command
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "airgap",
		Short: "Unified offline sync tool for OpenShift and enterprise repositories",
		Long: `airgap is a comprehensive tool for managing offline content synchronization
for OpenShift clusters and enterprise environments. It supports multiple content
providers including EPEL repositories, OCP binaries, RHCOS images, container
registries, and custom file sources.`,
		Example: `  airgap sync --all
  airgap sync --provider epel,ocp-binaries
  airgap validate --provider rhcos
  airgap serve --listen 0.0.0.0:8080
  airgap export --to /mnt/transfer --provider container-images
  airgap status --provider epel`,
		Version: "0.1.0",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Initialize logging
			setupLogging()

			// Skip config loading for commands that don't need it
			if shouldSkipConfig(cmd.Name()) {
				return nil
			}

			// Load config
			if cfgPath == "" {
				var err error
				cfgPath, err = config.FindConfigFile()
				if err != nil && cmd.Name() != "config" {
					logger.Warn("config file not found, using defaults", "error", err)
				}
			}

			if cfgPath != "" {
				var err error
				globalCfg, err = config.Load(cfgPath)
				if err != nil {
					return fmt.Errorf("failed to load config: %w", err)
				}
			} else {
				globalCfg = config.DefaultConfig()
			}

			// Override with command-line flags if provided
			if dataDir != "" {
				globalCfg.Server.DataDir = dataDir
			}

			if !quiet {
				logger.Debug("config loaded", "path", cfgPath, "data_dir", globalCfg.Server.DataDir)
			}

			// Initialize components after config is loaded
			if !shouldSkipComponentInit(cmd.Name()) {
				if err := initializeComponents(); err != nil {
					return fmt.Errorf("failed to initialize components: %w", err)
				}
			}

			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			closeStore()
		},
	}

	// Add persistent flags
	cmd.PersistentFlags().StringVar(&cfgPath, "config", "", "path to config file (auto-discovered if not specified)")
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "override data directory")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	cmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "log format (text or json)")
	cmd.PersistentFlags().BoolVar(&quiet, "quiet", false, "suppress non-error output")

	// Add subcommands
	cmd.AddCommand(
		newSyncCmd(),
		newValidateCmd(),
		newServeCmd(),
		newStatusCmd(),
		newExportCmd(),
		newImportCmd(),
		newConfigCmd(),
	)

	return cmd
}

// setupLogging initializes the slog logger based on flags
func setupLogging() {
	var level slog.Level
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	if strings.ToLower(logFormat) == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}

	logger = slog.New(handler)
	slog.SetDefault(logger)
}

// shouldSkipConfig checks if a command should skip config loading
func shouldSkipConfig(cmdName string) bool {
	skipConfigCmds := map[string]bool{
		"help":    true,
		"version": true,
	}
	return skipConfigCmds[cmdName]
}
