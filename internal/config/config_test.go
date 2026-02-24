package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultConfig verifies that DefaultConfig returns sensible defaults
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name     string
		getValue func(*Config) string
		want     string
	}{
		{"listen address", func(c *Config) string { return c.Server.Listen }, "0.0.0.0:8080"},
		{"data directory", func(c *Config) string { return c.Server.DataDir }, "/var/lib/airgap"},
		{"db path", func(c *Config) string { return c.Server.DBPath }, ""},
		{"split size", func(c *Config) string { return c.Export.SplitSize }, "25GB"},
		{"compression", func(c *Config) string { return c.Export.Compression }, "zstd"},
		{"output dir", func(c *Config) string { return c.Export.OutputDir }, "/mnt/transfer-disk"},
		{"manifest name", func(c *Config) string { return c.Export.ManifestName }, "airgap-manifest.json"},
		{"default cron", func(c *Config) string { return c.Schedule.DefaultCron }, "0 2 * * 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.getValue(cfg)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	// Verify schedule is enabled by default
	if !cfg.Schedule.Enabled {
		t.Errorf("Schedule.Enabled = false, want true")
	}

	// Verify providers map is initialized
	if cfg.Providers == nil {
		t.Errorf("Providers = nil, want non-nil map")
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("Providers length = %d, want 0", len(cfg.Providers))
	}
}

// TestLoad tests loading a valid config file
func TestLoad(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "airgap.yaml")

	configContent := `
server:
  listen: "0.0.0.0:9000"
  data_dir: "/custom/data"
  db_path: "/custom/data/app.db"
export:
  split_size: "50GB"
  compression: "gzip"
  output_dir: "/export"
  manifest_name: "custom-manifest.json"
schedule:
  enabled: false
  default_cron: "0 3 * * 1"
providers:
  ocp-binaries:
    enabled: true
    base_url: "https://example.com/ocp"
    versions:
      - "4.13"
      - "4.14"
    retry_attempts: 5
  epel:
    enabled: false
    max_concurrent_downloads: 10
`

	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	// Test server config
	if cfg.Server.Listen != "0.0.0.0:9000" {
		t.Errorf("Server.Listen = %q, want %q", cfg.Server.Listen, "0.0.0.0:9000")
	}
	if cfg.Server.DataDir != "/custom/data" {
		t.Errorf("Server.DataDir = %q, want %q", cfg.Server.DataDir, "/custom/data")
	}
	if cfg.Server.DBPath != "/custom/data/app.db" {
		t.Errorf("Server.DBPath = %q, want %q", cfg.Server.DBPath, "/custom/data/app.db")
	}

	// Test export config
	if cfg.Export.SplitSize != "50GB" {
		t.Errorf("Export.SplitSize = %q, want %q", cfg.Export.SplitSize, "50GB")
	}
	if cfg.Export.Compression != "gzip" {
		t.Errorf("Export.Compression = %q, want %q", cfg.Export.Compression, "gzip")
	}
	if cfg.Export.OutputDir != "/export" {
		t.Errorf("Export.OutputDir = %q, want %q", cfg.Export.OutputDir, "/export")
	}
	if cfg.Export.ManifestName != "custom-manifest.json" {
		t.Errorf("Export.ManifestName = %q, want %q", cfg.Export.ManifestName, "custom-manifest.json")
	}

	// Test schedule config
	if cfg.Schedule.Enabled {
		t.Errorf("Schedule.Enabled = true, want false")
	}
	if cfg.Schedule.DefaultCron != "0 3 * * 1" {
		t.Errorf("Schedule.DefaultCron = %q, want %q", cfg.Schedule.DefaultCron, "0 3 * * 1")
	}

	// Test providers
	if len(cfg.Providers) != 2 {
		t.Errorf("Providers length = %d, want 2", len(cfg.Providers))
	}

	ocpProvider, ok := cfg.Providers["ocp-binaries"]
	if !ok {
		t.Fatal("ocp-binaries provider not found")
	}
	if enabled, ok := ocpProvider["enabled"].(bool); !ok || !enabled {
		t.Errorf("ocp-binaries enabled = %v, want true", ocpProvider["enabled"])
	}

	epelProvider, ok := cfg.Providers["epel"]
	if !ok {
		t.Fatal("epel provider not found")
	}
	if enabled, ok := epelProvider["enabled"].(bool); !ok || enabled {
		t.Errorf("epel enabled = %v, want false", epelProvider["enabled"])
	}
}

// TestLoadInvalidYAML tests that Load returns an error for invalid YAML
func TestLoadInvalidYAML(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "invalid.yaml")

	invalidContent := `
server:
  listen: "0.0.0.0:8080"
  invalid: [unclosed bracket
`

	if err := os.WriteFile(configFile, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := Load(configFile)
	if err == nil {
		t.Error("Load() succeeded, want error for invalid YAML")
	}
	if err.Error() == "" {
		t.Error("error message is empty")
	}
}

// TestLoadNonexistentFile tests that Load returns an error for missing files
func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path/to/config.yaml")
	if err == nil {
		t.Error("Load() succeeded, want error for nonexistent file")
	}
	if err.Error() == "" {
		t.Error("error message is empty")
	}
}

// TestFindConfigFileNotFound tests that FindConfigFile returns error when no config exists
func TestFindConfigFileNotFound(t *testing.T) {
	// Save current working directory
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Create a temporary directory and change to it
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Fatalf("failed to restore working directory: %v", err)
		}
	})

	// Temporarily override home directory by ensuring we search non-existent paths
	// Since we can't easily mock UserHomeDir, we'll test that the error is returned
	// when no config file exists in standard locations
	_, err = FindConfigFile()
	if err == nil {
		t.Error("FindConfigFile() succeeded, want error when no config exists")
	}
	if err.Error() == "" {
		t.Error("error message is empty")
	}
}

// TestFindConfigFileFound tests that FindConfigFile returns the found config
func TestFindConfigFileFound(t *testing.T) {
	// Save current working directory
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Fatalf("failed to restore working directory: %v", err)
		}
	})

	// Create airgap.yaml in current directory
	configFile := filepath.Join(tempDir, "airgap.yaml")
	if err := os.WriteFile(configFile, []byte("server:\n  listen: \"0.0.0.0:8080\""), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	found, err := FindConfigFile()
	if err != nil {
		t.Fatalf("FindConfigFile() failed: %v", err)
	}

	// The found path should be "airgap.yaml" (relative to current directory)
	if found != "airgap.yaml" {
		t.Errorf("FindConfigFile() = %q, want airgap.yaml", found)
	}
}

// TestProviderEnabled tests the ProviderEnabled method
func TestProviderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		provider string
		want     bool
	}{
		{
			name: "provider enabled true",
			config: &Config{
				Providers: map[string]ProviderConfig{
					"test": {"enabled": true},
				},
			},
			provider: "test",
			want:     true,
		},
		{
			name: "provider enabled false",
			config: &Config{
				Providers: map[string]ProviderConfig{
					"test": {"enabled": false},
				},
			},
			provider: "test",
			want:     false,
		},
		{
			name: "provider missing enabled key",
			config: &Config{
				Providers: map[string]ProviderConfig{
					"test": {"other_key": "value"},
				},
			},
			provider: "test",
			want:     false,
		},
		{
			name: "provider not in config",
			config: &Config{
				Providers: map[string]ProviderConfig{},
			},
			provider: "nonexistent",
			want:     false,
		},
		{
			name: "enabled not a bool",
			config: &Config{
				Providers: map[string]ProviderConfig{
					"test": {"enabled": "true"},
				},
			},
			provider: "test",
			want:     false,
		},
		{
			name: "empty providers map",
			config: &Config{
				Providers: map[string]ProviderConfig{},
			},
			provider: "test",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.ProviderEnabled(tt.provider)
			if got != tt.want {
				t.Errorf("ProviderEnabled(%q) = %v, want %v", tt.provider, got, tt.want)
			}
		})
	}
}

// TestProviderDataDir tests the ProviderDataDir method
func TestProviderDataDir(t *testing.T) {
	tests := []struct {
		name        string
		dataDir     string
		relativeDir string
		want        string
	}{
		{
			name:        "simple relative path",
			dataDir:     "/var/lib/airgap",
			relativeDir: "ocp",
			want:        "/var/lib/airgap/ocp",
		},
		{
			name:        "nested relative path",
			dataDir:     "/var/lib/airgap",
			relativeDir: "providers/ocp/binaries",
			want:        "/var/lib/airgap/providers/ocp/binaries",
		},
		{
			name:        "trailing slash in dataDir",
			dataDir:     "/var/lib/airgap/",
			relativeDir: "ocp",
			want:        "/var/lib/airgap/ocp",
		},
		{
			name:        "empty relativeDir",
			dataDir:     "/var/lib/airgap",
			relativeDir: "",
			want:        "/var/lib/airgap",
		},
		{
			name:        "custom dataDir",
			dataDir:     "/custom/path",
			relativeDir: "provider",
			want:        "/custom/path/provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server: ServerConfig{DataDir: tt.dataDir},
			}
			got := cfg.ProviderDataDir(tt.relativeDir)
			if got != tt.want {
				t.Errorf("ProviderDataDir(%q) = %q, want %q", tt.relativeDir, got, tt.want)
			}
		})
	}
}

// TestParseProviderConfigOCPBinaries tests generic unmarshaling with OCPBinariesProviderConfig
func TestParseProviderConfigOCPBinaries(t *testing.T) {
	raw := ProviderConfig{
		"enabled":          true,
		"base_url":         "https://mirror.example.com/ocp",
		"versions":         []interface{}{"4.13", "4.14"},
		"ignored_patterns": []interface{}{"*.sha256", "*.sig"},
		"output_dir":       "/exports/ocp",
		"retry_attempts":   3,
	}

	typed, err := ParseProviderConfig[OCPBinariesProviderConfig](raw)
	if err != nil {
		t.Fatalf("ParseProviderConfig failed: %v", err)
	}

	if !typed.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if typed.BaseURL != "https://mirror.example.com/ocp" {
		t.Errorf("BaseURL = %q, want %q", typed.BaseURL, "https://mirror.example.com/ocp")
	}
	if len(typed.Versions) != 2 {
		t.Errorf("Versions length = %d, want 2", len(typed.Versions))
	}
	if typed.Versions[0] != "4.13" {
		t.Errorf("Versions[0] = %q, want %q", typed.Versions[0], "4.13")
	}
	if typed.Versions[1] != "4.14" {
		t.Errorf("Versions[1] = %q, want %q", typed.Versions[1], "4.14")
	}
	if len(typed.IgnoredPatterns) != 2 {
		t.Errorf("IgnoredPatterns length = %d, want 2", len(typed.IgnoredPatterns))
	}
	if typed.OutputDir != "/exports/ocp" {
		t.Errorf("OutputDir = %q, want %q", typed.OutputDir, "/exports/ocp")
	}
	if typed.RetryAttempts != 3 {
		t.Errorf("RetryAttempts = %d, want 3", typed.RetryAttempts)
	}
}

// TestParseProviderConfigEPEL tests generic unmarshaling with EPELProviderConfig
func TestParseProviderConfigEPEL(t *testing.T) {
	raw := ProviderConfig{
		"enabled": false,
		"repos": []interface{}{
			map[string]interface{}{
				"name":       "epel-8",
				"base_url":   "https://mirrors.example.com/epel/8",
				"output_dir": "/exports/epel8",
			},
			map[string]interface{}{
				"name":       "epel-9",
				"base_url":   "https://mirrors.example.com/epel/9",
				"output_dir": "/exports/epel9",
			},
		},
		"max_concurrent_downloads": 15,
		"retry_attempts":           4,
		"cleanup_removed_packages": true,
	}

	typed, err := ParseProviderConfig[EPELProviderConfig](raw)
	if err != nil {
		t.Fatalf("ParseProviderConfig failed: %v", err)
	}

	if typed.Enabled {
		t.Errorf("Enabled = true, want false")
	}
	if len(typed.Repos) != 2 {
		t.Errorf("Repos length = %d, want 2", len(typed.Repos))
	}
	if typed.Repos[0].Name != "epel-8" {
		t.Errorf("Repos[0].Name = %q, want %q", typed.Repos[0].Name, "epel-8")
	}
	if typed.Repos[0].BaseURL != "https://mirrors.example.com/epel/8" {
		t.Errorf("Repos[0].BaseURL = %q", typed.Repos[0].BaseURL)
	}
	if typed.Repos[1].Name != "epel-9" {
		t.Errorf("Repos[1].Name = %q, want %q", typed.Repos[1].Name, "epel-9")
	}
	if typed.MaxConcurrentDownloads != 15 {
		t.Errorf("MaxConcurrentDownloads = %d, want 15", typed.MaxConcurrentDownloads)
	}
	if typed.RetryAttempts != 4 {
		t.Errorf("RetryAttempts = %d, want 4", typed.RetryAttempts)
	}
	if !typed.CleanupRemovedPackages {
		t.Errorf("CleanupRemovedPackages = false, want true")
	}
}

// TestParseProviderConfigInvalidYAML tests error handling in ParseProviderConfig
func TestParseProviderConfigInvalidYAML(t *testing.T) {
	// Create a ProviderConfig with a value that can't be properly unmarshaled
	// This is tricky since ProviderConfig is map[string]interface{}, but we can use
	// complex nested structures
	raw := ProviderConfig{
		"enabled":        true,
		"versions":       "should-be-array", // Wrong type - should be array
		"output_dir":     "/exports",
		"retry_attempts": "not-an-int", // Wrong type - should be int
	}

	_, err := ParseProviderConfig[OCPBinariesProviderConfig](raw)
	if err == nil {
		t.Fatal("ParseProviderConfig should fail with invalid types")
	}
}

// TestParseProviderConfigWithDefaults tests that missing fields get zero values
func TestParseProviderConfigWithDefaults(t *testing.T) {
	raw := ProviderConfig{
		"enabled": true,
	}

	typed, err := ParseProviderConfig[OCPBinariesProviderConfig](raw)
	if err != nil {
		t.Fatalf("ParseProviderConfig failed: %v", err)
	}

	if !typed.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if typed.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty", typed.BaseURL)
	}
	if len(typed.Versions) != 0 {
		t.Errorf("Versions length = %d, want 0", len(typed.Versions))
	}
	if typed.RetryAttempts != 0 {
		t.Errorf("RetryAttempts = %d, want 0", typed.RetryAttempts)
	}
}

// TestLoadIntegration tests a complete load and parse workflow
func TestLoadIntegration(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "full-config.yaml")

	configContent := `
server:
  listen: "0.0.0.0:8080"
  data_dir: "/data"
  db_path: "/data/app.db"
export:
  split_size: "100GB"
  compression: "zstd"
  output_dir: "/export"
  manifest_name: "manifest.json"
schedule:
  enabled: true
  default_cron: "0 2 * * 0"
providers:
  ocp:
    enabled: true
    base_url: "https://example.com/ocp"
    versions:
      - "4.13"
  epel:
    enabled: true
    repos:
      - name: "epel-8"
        base_url: "https://mirrors.example.com/epel/8"
        output_dir: "/data/epel8"
    max_concurrent_downloads: 10
    retry_attempts: 3
`

	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Load the config
	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Parse OCP provider
	ocpRaw, ok := cfg.Providers["ocp"]
	if !ok {
		t.Fatal("ocp provider not found")
	}
	ocpTyped, err := ParseProviderConfig[OCPBinariesProviderConfig](ocpRaw)
	if err != nil {
		t.Fatalf("ParseProviderConfig for ocp failed: %v", err)
	}
	if !ocpTyped.Enabled {
		t.Errorf("ocp Enabled = false, want true")
	}
	if len(ocpTyped.Versions) != 1 || ocpTyped.Versions[0] != "4.13" {
		t.Errorf("ocp Versions = %v, want [4.13]", ocpTyped.Versions)
	}

	// Parse EPEL provider
	epelRaw, ok := cfg.Providers["epel"]
	if !ok {
		t.Fatal("epel provider not found")
	}
	epelTyped, err := ParseProviderConfig[EPELProviderConfig](epelRaw)
	if err != nil {
		t.Fatalf("ParseProviderConfig for epel failed: %v", err)
	}
	if !epelTyped.Enabled {
		t.Errorf("epel Enabled = false, want true")
	}
	if len(epelTyped.Repos) != 1 {
		t.Errorf("epel Repos length = %d, want 1", len(epelTyped.Repos))
	}
	if epelTyped.MaxConcurrentDownloads != 10 {
		t.Errorf("epel MaxConcurrentDownloads = %d, want 10", epelTyped.MaxConcurrentDownloads)
	}
}
