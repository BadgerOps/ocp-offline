package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration
type Config struct {
	Server    ServerConfig              `yaml:"server"`
	Export    ExportConfig              `yaml:"export"`
	Schedule  ScheduleConfig            `yaml:"schedule"`
	Providers map[string]ProviderConfig `yaml:"providers"`
}

// ServerConfig holds server settings
type ServerConfig struct {
	Listen  string `yaml:"listen"`
	DataDir string `yaml:"data_dir"`
	DBPath  string `yaml:"db_path"`
}

// ExportConfig holds export/transfer settings
type ExportConfig struct {
	SplitSize    string `yaml:"split_size"`
	Compression  string `yaml:"compression"`
	OutputDir    string `yaml:"output_dir"`
	ManifestName string `yaml:"manifest_name"`
}

// ScheduleConfig holds scheduler settings
type ScheduleConfig struct {
	Enabled     bool   `yaml:"enabled"`
	DefaultCron string `yaml:"default_cron"`
}

// ProviderConfig is the raw YAML config for a provider
type ProviderConfig map[string]interface{}

// EPELRepoConfig represents a single EPEL repo definition
type EPELRepoConfig struct {
	Name      string `yaml:"name"`
	BaseURL   string `yaml:"base_url"`
	OutputDir string `yaml:"output_dir"`
}

// EPELProviderConfig is the typed config for the EPEL provider
type EPELProviderConfig struct {
	Enabled                bool             `yaml:"enabled"`
	Repos                  []EPELRepoConfig `yaml:"repos"`
	MaxConcurrentDownloads int              `yaml:"max_concurrent_downloads"`
	RetryAttempts          int              `yaml:"retry_attempts"`
	CleanupRemovedPackages bool             `yaml:"cleanup_removed_packages"`
}

// OCPBinariesProviderConfig is the typed config for OCP binaries
type OCPBinariesProviderConfig struct {
	Enabled         bool     `yaml:"enabled"`
	BaseURL         string   `yaml:"base_url"`
	Versions        []string `yaml:"versions"`
	IgnoredPatterns []string `yaml:"ignored_patterns"`
	OutputDir       string   `yaml:"output_dir"`
	RetryAttempts   int      `yaml:"retry_attempts"`
}

// RHCOSProviderConfig is the typed config for RHCOS images
type RHCOSProviderConfig struct {
	Enabled         bool     `yaml:"enabled"`
	BaseURL         string   `yaml:"base_url"`
	Versions        []string `yaml:"versions"`
	IgnoredPatterns []string `yaml:"ignored_patterns"`
	OutputDir       string   `yaml:"output_dir"`
	RetryAttempts   int      `yaml:"retry_attempts"`
}

// ContainerImagesProviderConfig is the typed config for container images
type ContainerImagesProviderConfig struct {
	Enabled        bool   `yaml:"enabled"`
	OCMirrorBinary string `yaml:"oc_mirror_binary"`
	ImagesetConfig string `yaml:"imageset_config"`
	OutputDir      string `yaml:"output_dir"`
}

// RegistryProviderConfig is the typed config for mirror-registry
type RegistryProviderConfig struct {
	Enabled              bool   `yaml:"enabled"`
	MirrorRegistryBinary string `yaml:"mirror_registry_binary"`
	QuayRoot             string `yaml:"quay_root"`
}

// CustomFilesProviderConfig is the typed config for custom file sources
type CustomFilesProviderConfig struct {
	Enabled bool               `yaml:"enabled"`
	Sources []CustomFileSource `yaml:"sources"`
}

// CustomFileSource is a single custom file source
type CustomFileSource struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	ChecksumURL string `yaml:"checksum_url"`
	OutputDir   string `yaml:"output_dir"`
}

// DefaultConfig returns a config with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:  "0.0.0.0:8080",
			DataDir: "/var/lib/airgap",
			DBPath:  "",
		},
		Export: ExportConfig{
			SplitSize:    "25GB",
			Compression:  "zstd",
			OutputDir:    "/mnt/transfer-disk",
			ManifestName: "airgap-manifest.json",
		},
		Schedule: ScheduleConfig{
			Enabled:     true,
			DefaultCron: "0 2 * * 0",
		},
		Providers: make(map[string]ProviderConfig),
	}
}

// Load reads a config file from the given path
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return cfg, nil
}

// FindConfigFile searches for a config file in standard locations
func FindConfigFile() (string, error) {
	searchPaths := []string{
		"airgap.yaml",
		"/etc/airgap/airgap.yaml",
	}

	// Add user config path
	if home, err := os.UserHomeDir(); err == nil {
		searchPaths = append(searchPaths,
			filepath.Join(home, ".config", "airgap", "airgap.yaml"),
		)
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no config file found (searched: %v)", searchPaths)
}

// ProviderEnabled checks if a provider is enabled in the config
func (c *Config) ProviderEnabled(name string) bool {
	pc, ok := c.Providers[name]
	if !ok {
		return false
	}
	enabled, ok := pc["enabled"]
	if !ok {
		return false
	}
	b, ok := enabled.(bool)
	return ok && b
}

// ProviderDataDir returns the absolute path for a provider's data directory
func (c *Config) ProviderDataDir(relativeDir string) string {
	return filepath.Join(c.Server.DataDir, relativeDir)
}

// ParseProviderConfig unmarshals a provider's raw config into a typed struct
func ParseProviderConfig[T any](raw ProviderConfig) (*T, error) {
	// Re-marshal to YAML then unmarshal to typed struct
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshaling provider config: %w", err)
	}
	var typed T
	if err := yaml.Unmarshal(data, &typed); err != nil {
		return nil, fmt.Errorf("parsing provider config: %w", err)
	}
	return &typed, nil
}
