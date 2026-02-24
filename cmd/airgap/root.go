package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/provider/containerimages"
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

	// Ensure data directory exists
	if err := os.MkdirAll(globalCfg.Server.DataDir, 0o755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Initialize store
	dbPath := globalCfg.Server.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(globalCfg.Server.DataDir, "airgap.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
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

	// Seed provider configs from YAML into DB on first run
	// Convert map[string]config.ProviderConfig to map[string]map[string]interface{}
	yamlProviders := make(map[string]map[string]interface{}, len(globalCfg.Providers))
	for k, v := range globalCfg.Providers {
		yamlProviders[k] = v
	}
	if err := st.SeedProviderConfigs(yamlProviders); err != nil {
		logger.Warn("failed to seed provider configs", "error", err)
	}

	// Load provider configs from DB and register enabled providers
	providerConfigs, err := st.ListProviderConfigs()
	if err != nil {
		return fmt.Errorf("failed to list provider configs: %w", err)
	}

	for _, pc := range providerConfigs {
		if !pc.Enabled {
			continue
		}

		p, err := createProvider(pc.Type, globalCfg.Server.DataDir, logger)
		if err != nil {
			logger.Warn("skipping provider: unknown type", "name", pc.Name, "type", pc.Type)
			continue
		}

		var rawCfg map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); jsonErr != nil {
			logger.Warn("failed to parse provider config", "name", pc.Name, "error", jsonErr)
			continue
		}

		if cfgErr := p.Configure(rawCfg); cfgErr != nil {
			logger.Warn("failed to configure provider", "name", pc.Name, "error", cfgErr)
		}
		globalRegistry.RegisterAs(pc.Name, p)
	}

	// Populate config.Providers from DB so ProviderEnabled() works.
	// Inject the "enabled" flag from store.ProviderConfig into the raw map,
	// because ProviderEnabled() looks for an "enabled" key in the map.
	globalCfg.Providers = make(map[string]config.ProviderConfig)
	for _, pc := range providerConfigs {
		var rawCfg map[string]interface{}
		if err := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); err == nil {
			rawCfg["enabled"] = pc.Enabled
			globalCfg.Providers[pc.Name] = rawCfg
		}
	}

	// Initialize sync manager
	globalEngine = engine.NewSyncManager(globalRegistry, globalStore, client, globalCfg, logger)
	globalEngine.SetProviderFactory(func(typeName, dataDir string, log *slog.Logger) (provider.Provider, error) {
		return createProvider(typeName, dataDir, log)
	})

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

// createProvider instantiates a provider by type name.
func createProvider(typeName, dataDir string, log *slog.Logger) (provider.Provider, error) {
	switch typeName {
	case "epel":
		return epel.NewEPELProvider(dataDir, log), nil
	case "ocp_binaries":
		return ocp.NewBinariesProvider(dataDir, log), nil
	case "ocp_clients":
		return ocp.NewClientsProvider(dataDir, log), nil
	case "rhcos":
		return ocp.NewRHCOSProvider(dataDir, log), nil
	case "container_images":
		return containerimages.NewProvider(dataDir, log), nil
	case "registry", "custom_files":
		return nil, fmt.Errorf("provider type %q is not yet implemented", typeName)
	default:
		return nil, fmt.Errorf("unknown provider type: %q", typeName)
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
		newRegistryCmd(),
		newProvidersCmd(),
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
