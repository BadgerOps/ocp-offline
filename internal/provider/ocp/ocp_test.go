package ocp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BadgerOps/airgap/internal/provider"
)

// helper function to compute SHA256 of data
func computeSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// helper function to create a no-op logger for tests
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// =============================================================================
// BinariesProvider Tests
// =============================================================================

// TestBinariesProviderName verifies Name() returns expected string
func TestBinariesProviderName(t *testing.T) {
	dataDir := t.TempDir()
	provider := NewBinariesProvider(dataDir, testLogger())

	got := provider.Name()
	want := "ocp_binaries"

	if got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

// TestBinariesProviderConfigure tests configuration with valid config
func TestBinariesProviderConfigure(t *testing.T) {
	dataDir := t.TempDir()
	p := NewBinariesProvider(dataDir, testLogger())

	// Create provider config from raw map
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         "https://mirror.openshift.com",
		"versions":         []interface{}{"4.17", "4.18"},
		"output_dir":       "ocp-binaries",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{"windows"},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	if p.cfg == nil {
		t.Fatal("Configure() did not set cfg field")
	}

	if p.cfg.BaseURL != "https://mirror.openshift.com" {
		t.Errorf("BaseURL = %q, want %q", p.cfg.BaseURL, "https://mirror.openshift.com")
	}

	if len(p.cfg.Versions) != 2 {
		t.Errorf("Versions length = %d, want 2", len(p.cfg.Versions))
	}

	if p.cfg.OutputDir != "ocp-binaries" {
		t.Errorf("OutputDir = %q, want %q", p.cfg.OutputDir, "ocp-binaries")
	}

	if p.cfg.RetryAttempts != 3 {
		t.Errorf("RetryAttempts = %d, want 3", p.cfg.RetryAttempts)
	}
}

// TestBinariesProviderPlan tests Plan() with mocked checksum file
func TestBinariesProviderPlan(t *testing.T) {
	dataDir := t.TempDir()
	p := NewBinariesProvider(dataDir, testLogger())

	// Test content and checksums
	clientContent := []byte("openshift-client-linux")
	clientHash := computeSHA256(clientContent)

	installContent := []byte("openshift-install-linux")
	installHash := computeSHA256(installContent)

	windowsContent := []byte("openshift-client-windows")
	windowsHash := computeSHA256(windowsContent)

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest-4.18/sha256sum.txt" {
			checksumContent := fmt.Sprintf("%s  openshift-client-linux-4.18.0.tar.gz\n%s  openshift-install-linux-4.18.0.tar.gz\n%s  openshift-client-windows-4.18.0.zip\n",
				clientHash, installHash, windowsHash)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(checksumContent))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Configure provider
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         server.URL,
		"versions":         []interface{}{"latest-4.18"},
		"output_dir":       "ocp-binaries",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Call Plan
	ctx := context.Background()
	plan, err := p.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	if plan == nil {
		t.Fatal("Plan() returned nil")
	}

	if plan.Provider != "ocp_binaries" {
		t.Errorf("Plan.Provider = %q, want %q", plan.Provider, "ocp_binaries")
	}

	if len(plan.Actions) != 3 {
		t.Errorf("Plan.Actions length = %d, want 3", len(plan.Actions))
	}

	// Verify actions
	expectedActions := map[string]string{
		"openshift-client-linux-4.18.0.tar.gz":  clientHash,
		"openshift-install-linux-4.18.0.tar.gz": installHash,
		"openshift-client-windows-4.18.0.zip":   windowsHash,
	}

	for _, action := range plan.Actions {
		expectedHash, ok := expectedActions[filepath.Base(action.Path)]
		if !ok {
			t.Errorf("unexpected file in plan: %s", action.Path)
			continue
		}

		if action.Checksum != expectedHash {
			t.Errorf("file %s: checksum mismatch, got %q want %q",
				action.Path, action.Checksum, expectedHash)
		}

		if action.Action != provider.ActionDownload {
			t.Errorf("file %s: action = %q, want %q",
				action.Path, action.Action, provider.ActionDownload)
		}
	}
}

// TestBinariesProviderPlanIgnoredFiles tests that ignored patterns filter correctly
func TestBinariesProviderPlanIgnoredFiles(t *testing.T) {
	dataDir := t.TempDir()
	p := NewBinariesProvider(dataDir, testLogger())

	clientContent := []byte("client-linux")
	clientHash := computeSHA256(clientContent)

	windowsContent := []byte("client-windows")
	windowsHash := computeSHA256(windowsContent)

	macContent := []byte("client-macos")
	macHash := computeSHA256(macContent)

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest-4.18/sha256sum.txt" {
			checksumContent := fmt.Sprintf("%s  openshift-client-linux-4.18.0.tar.gz\n%s  openshift-client-windows-4.18.0.zip\n%s  openshift-client-macos-4.18.0.tar.gz\n",
				clientHash, windowsHash, macHash)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(checksumContent))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Configure with ignored patterns
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         server.URL,
		"versions":         []interface{}{"latest-4.18"},
		"output_dir":       "ocp-binaries",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{"windows", "macos"},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Call Plan
	ctx := context.Background()
	plan, err := p.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	// Should only have linux client
	if len(plan.Actions) != 1 {
		t.Errorf("Plan.Actions length = %d, want 1", len(plan.Actions))
	}

	if !containsFile(plan.Actions, "openshift-client-linux-4.18.0.tar.gz") {
		t.Error("expected openshift-client-linux-4.18.0.tar.gz in plan")
	}

	if containsFile(plan.Actions, "openshift-client-windows-4.18.0.zip") {
		t.Error("windows file should have been filtered")
	}

	if containsFile(plan.Actions, "openshift-client-macos-4.18.0.tar.gz") {
		t.Error("macos file should have been filtered")
	}
}

// TestBinariesProviderSync tests full sync operation
func TestBinariesProviderSync(t *testing.T) {
	dataDir := t.TempDir()
	p := NewBinariesProvider(dataDir, testLogger())

	clientContent := []byte("openshift-client-content")
	clientHash := computeSHA256(clientContent)

	installContent := []byte("openshift-install-content")
	installHash := computeSHA256(installContent)

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest-4.18/sha256sum.txt" {
			checksumContent := fmt.Sprintf("%s  openshift-client-linux-4.18.0.tar.gz\n%s  openshift-install-linux-4.18.0.tar.gz\n",
				clientHash, installHash)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(checksumContent))
		} else if r.URL.Path == "/latest-4.18/openshift-client-linux-4.18.0.tar.gz" {
			w.WriteHeader(http.StatusOK)
			w.Write(clientContent)
		} else if r.URL.Path == "/latest-4.18/openshift-install-linux-4.18.0.tar.gz" {
			w.WriteHeader(http.StatusOK)
			w.Write(installContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Configure provider
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         server.URL,
		"versions":         []interface{}{"latest-4.18"},
		"output_dir":       "ocp-binaries",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Create plan
	ctx := context.Background()
	plan, err := p.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	// Execute sync with dry-run
	opts := provider.SyncOptions{
		DryRun: true,
	}

	report, err := p.Sync(ctx, plan, opts)
	if err != nil {
		t.Fatalf("Sync() failed: %v", err)
	}

	if report == nil {
		t.Fatal("Sync() returned nil report")
	}

	if report.Provider != "ocp_binaries" {
		t.Errorf("Report.Provider = %q, want %q", report.Provider, "ocp_binaries")
	}

	if report.EndTime.IsZero() {
		// In dry-run mode, the report should be minimal
		// In non-dry-run, we would expect actual file operations
	}
}

// TestBinariesProviderValidate tests file validation
func TestBinariesProviderValidate(t *testing.T) {
	dataDir := t.TempDir()
	p := NewBinariesProvider(dataDir, testLogger())

	// Configure provider
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         "https://example.com",
		"versions":         []interface{}{"4.18"},
		"output_dir":       "ocp-binaries",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Create test files in the output directory
	outputPath := filepath.Join(dataDir, "ocp-binaries", "4.18")
	err = os.MkdirAll(outputPath, 0755)
	if err != nil {
		t.Fatalf("failed to create output directory: %v", err)
	}

	// Create test file
	testFile := filepath.Join(outputPath, "test-binary.tar.gz")
	testContent := []byte("binary-content")
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Call Validate
	ctx := context.Background()
	report, err := p.Validate(ctx)
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}

	if report == nil {
		t.Fatal("Validate() returned nil report")
	}

	if report.Provider != "ocp_binaries" {
		t.Errorf("Report.Provider = %q, want %q", report.Provider, "ocp_binaries")
	}

	if report.TotalFiles != 1 {
		t.Errorf("Report.TotalFiles = %d, want 1", report.TotalFiles)
	}

	if report.ValidFiles != 1 {
		t.Errorf("Report.ValidFiles = %d, want 1", report.ValidFiles)
	}

	if len(report.InvalidFiles) != 0 {
		t.Errorf("Report.InvalidFiles length = %d, want 0", len(report.InvalidFiles))
	}
}

// =============================================================================
// RHCOSProvider Tests
// =============================================================================

// TestRHCOSProviderName verifies Name() returns expected string
func TestRHCOSProviderName(t *testing.T) {
	dataDir := t.TempDir()
	provider := NewRHCOSProvider(dataDir, testLogger())

	got := provider.Name()
	want := "rhcos"

	if got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

// TestRHCOSProviderConfigure tests configuration with valid config
func TestRHCOSProviderConfigure(t *testing.T) {
	dataDir := t.TempDir()
	p := NewRHCOSProvider(dataDir, testLogger())

	// Create provider config from raw map
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         "https://rhcos.mirror.example.com",
		"versions":         []interface{}{"418.0", "418.1"},
		"output_dir":       "rhcos-images",
		"retry_attempts":   5,
		"ignored_patterns": []interface{}{"qemu"},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	if p.cfg == nil {
		t.Fatal("Configure() did not set cfg field")
	}

	if p.cfg.BaseURL != "https://rhcos.mirror.example.com" {
		t.Errorf("BaseURL = %q, want %q", p.cfg.BaseURL, "https://rhcos.mirror.example.com")
	}

	if len(p.cfg.Versions) != 2 {
		t.Errorf("Versions length = %d, want 2", len(p.cfg.Versions))
	}

	if p.cfg.OutputDir != "rhcos-images" {
		t.Errorf("OutputDir = %q, want %q", p.cfg.OutputDir, "rhcos-images")
	}

	if p.cfg.RetryAttempts != 5 {
		t.Errorf("RetryAttempts = %d, want 5", p.cfg.RetryAttempts)
	}
}

// TestRHCOSProviderPlan tests Plan() with mocked checksum file
func TestRHCOSProviderPlan(t *testing.T) {
	dataDir := t.TempDir()
	p := NewRHCOSProvider(dataDir, testLogger())

	// Test content and checksums
	qemuContent := []byte("rhcos-qemu")
	qemuHash := computeSHA256(qemuContent)

	vmwareContent := []byte("rhcos-vmware")
	vmwareHash := computeSHA256(vmwareContent)

	awsContent := []byte("rhcos-aws")
	awsHash := computeSHA256(awsContent)

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/418.1/sha256sum.txt" {
			checksumContent := fmt.Sprintf("%s  rhcos-418.1-qemu.qcow2.gz\n%s  rhcos-418.1-vmware.ova\n%s  rhcos-418.1-aws.tar.gz\n",
				qemuHash, vmwareHash, awsHash)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(checksumContent))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Configure provider
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         server.URL,
		"versions":         []interface{}{"418.1"},
		"output_dir":       "rhcos-images",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Call Plan
	ctx := context.Background()
	plan, err := p.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	if plan == nil {
		t.Fatal("Plan() returned nil")
	}

	if plan.Provider != "rhcos" {
		t.Errorf("Plan.Provider = %q, want %q", plan.Provider, "rhcos")
	}

	if len(plan.Actions) != 3 {
		t.Errorf("Plan.Actions length = %d, want 3", len(plan.Actions))
	}

	// Verify all expected files are in plan
	if !containsFile(plan.Actions, "rhcos-418.1-qemu.qcow2.gz") {
		t.Error("expected rhcos-418.1-qemu.qcow2.gz in plan")
	}

	if !containsFile(plan.Actions, "rhcos-418.1-vmware.ova") {
		t.Error("expected rhcos-418.1-vmware.ova in plan")
	}

	if !containsFile(plan.Actions, "rhcos-418.1-aws.tar.gz") {
		t.Error("expected rhcos-418.1-aws.tar.gz in plan")
	}
}

// TestRHCOSProviderPlanIgnoredFiles tests that cloud-specific images are filtered
func TestRHCOSProviderPlanIgnoredFiles(t *testing.T) {
	dataDir := t.TempDir()
	p := NewRHCOSProvider(dataDir, testLogger())

	qemuContent := []byte("rhcos-qemu")
	qemuHash := computeSHA256(qemuContent)

	vmwareContent := []byte("rhcos-vmware")
	vmwareHash := computeSHA256(vmwareContent)

	azureContent := []byte("rhcos-azure")
	azureHash := computeSHA256(azureContent)

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/418.1/sha256sum.txt" {
			checksumContent := fmt.Sprintf("%s  rhcos-418.1-qemu.qcow2.gz\n%s  rhcos-418.1-vmware.ova\n%s  rhcos-418.1-azure.tar.gz\n",
				qemuHash, vmwareHash, azureHash)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(checksumContent))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Configure with ignored patterns (filter cloud images)
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         server.URL,
		"versions":         []interface{}{"418.1"},
		"output_dir":       "rhcos-images",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{"azure", "qemu"},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Call Plan
	ctx := context.Background()
	plan, err := p.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	// Should only have vmware
	if len(plan.Actions) != 1 {
		t.Errorf("Plan.Actions length = %d, want 1", len(plan.Actions))
	}

	if !containsFile(plan.Actions, "rhcos-418.1-vmware.ova") {
		t.Error("expected rhcos-418.1-vmware.ova in plan")
	}

	if containsFile(plan.Actions, "rhcos-418.1-qemu.qcow2.gz") {
		t.Error("qemu file should have been filtered")
	}

	if containsFile(plan.Actions, "rhcos-418.1-azure.tar.gz") {
		t.Error("azure file should have been filtered")
	}
}

// TestRHCOSProviderSync tests full sync operation
func TestRHCOSProviderSync(t *testing.T) {
	dataDir := t.TempDir()
	p := NewRHCOSProvider(dataDir, testLogger())

	vmwareContent := []byte("rhcos-vmware-disk")
	vmwareHash := computeSHA256(vmwareContent)

	awsContent := []byte("rhcos-aws-ami")
	awsHash := computeSHA256(awsContent)

	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/418.1/sha256sum.txt" {
			checksumContent := fmt.Sprintf("%s  rhcos-418.1-vmware.ova\n%s  rhcos-418.1-aws.tar.gz\n",
				vmwareHash, awsHash)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(checksumContent))
		} else if r.URL.Path == "/418.1/rhcos-418.1-vmware.ova" {
			w.WriteHeader(http.StatusOK)
			w.Write(vmwareContent)
		} else if r.URL.Path == "/418.1/rhcos-418.1-aws.tar.gz" {
			w.WriteHeader(http.StatusOK)
			w.Write(awsContent)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Configure provider
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         server.URL,
		"versions":         []interface{}{"418.1"},
		"output_dir":       "rhcos-images",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Create plan
	ctx := context.Background()
	plan, err := p.Plan(ctx)
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}

	// Execute sync with dry-run
	opts := provider.SyncOptions{
		DryRun: true,
	}

	report, err := p.Sync(ctx, plan, opts)
	if err != nil {
		t.Fatalf("Sync() failed: %v", err)
	}

	if report == nil {
		t.Fatal("Sync() returned nil report")
	}

	if report.Provider != "rhcos" {
		t.Errorf("Report.Provider = %q, want %q", report.Provider, "rhcos")
	}
}

// TestRHCOSProviderValidate tests file validation
func TestRHCOSProviderValidate(t *testing.T) {
	dataDir := t.TempDir()
	p := NewRHCOSProvider(dataDir, testLogger())

	// Configure provider
	rawCfg := provider.ProviderConfig{
		"enabled":          true,
		"base_url":         "https://example.com",
		"versions":         []interface{}{"418.1"},
		"output_dir":       "rhcos-images",
		"retry_attempts":   3,
		"ignored_patterns": []interface{}{},
	}

	err := p.Configure(rawCfg)
	if err != nil {
		t.Fatalf("Configure() failed: %v", err)
	}

	// Create test files in the output directory
	outputPath := filepath.Join(dataDir, "rhcos-images", "418.1")
	err = os.MkdirAll(outputPath, 0755)
	if err != nil {
		t.Fatalf("failed to create output directory: %v", err)
	}

	// Create test file
	testFile := filepath.Join(outputPath, "rhcos-418.1-qemu.qcow2.gz")
	testContent := []byte("rhcos-image-content")
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Call Validate
	ctx := context.Background()
	report, err := p.Validate(ctx)
	if err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}

	if report == nil {
		t.Fatal("Validate() returned nil report")
	}

	if report.Provider != "rhcos" {
		t.Errorf("Report.Provider = %q, want %q", report.Provider, "rhcos")
	}

	if report.TotalFiles != 1 {
		t.Errorf("Report.TotalFiles = %d, want 1", report.TotalFiles)
	}

	if report.ValidFiles != 1 {
		t.Errorf("Report.ValidFiles = %d, want 1", report.ValidFiles)
	}

	if len(report.InvalidFiles) != 0 {
		t.Errorf("Report.InvalidFiles length = %d, want 0", len(report.InvalidFiles))
	}
}

// =============================================================================
// Common Functions Tests
// =============================================================================

// TestParseChecksumFile tests the checksum parsing function
func TestParseChecksumFile(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected map[string]string
	}{
		{
			name: "standard format",
			data: "abc123  file1.tar.gz\ndef456  file2.tar.gz\n",
			expected: map[string]string{
				"file1.tar.gz": "abc123",
				"file2.tar.gz": "def456",
			},
		},
		{
			name: "with empty lines",
			data: "abc123  file1.tar.gz\n\ndef456  file2.tar.gz\n",
			expected: map[string]string{
				"file1.tar.gz": "abc123",
				"file2.tar.gz": "def456",
			},
		},
		{
			name:     "empty file",
			data:     "",
			expected: map[string]string{},
		},
		{
			name: "single entry",
			data: "abc123  myfile.txt\n",
			expected: map[string]string{
				"myfile.txt": "abc123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseChecksumFile([]byte(tt.data))
			if len(result) != len(tt.expected) {
				t.Errorf("parseChecksumFile() returned %d items, want %d", len(result), len(tt.expected))
			}
			for filename, expectedHash := range tt.expected {
				actualHash, ok := result[filename]
				if !ok {
					t.Errorf("missing file: %s", filename)
					continue
				}
				if actualHash != expectedHash {
					t.Errorf("file %s: got hash %q, want %q", filename, actualHash, expectedHash)
				}
			}
		})
	}
}

// TestFilterFiles tests the file filtering function
func TestFilterFiles(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		patterns []string
		expected int
		excluded []string
	}{
		{
			name: "filter windows",
			files: map[string]string{
				"client-linux.tar.gz":  "hash1",
				"client-windows.zip":   "hash2",
				"install-linux.tar.gz": "hash3",
			},
			patterns: []string{"windows"},
			expected: 2,
			excluded: []string{"client-windows.zip"},
		},
		{
			name: "filter multiple patterns",
			files: map[string]string{
				"image-qemu.qcow2":   "hash1",
				"image-vmware.ova":   "hash2",
				"image-azure.tar.gz": "hash3",
			},
			patterns: []string{"qemu", "azure"},
			expected: 1,
			excluded: []string{"image-qemu.qcow2", "image-azure.tar.gz"},
		},
		{
			name: "case insensitive matching",
			files: map[string]string{
				"file-WINDOWS.zip":  "hash1",
				"file-linux.tar.gz": "hash2",
			},
			patterns: []string{"windows"},
			expected: 1,
			excluded: []string{"file-WINDOWS.zip"},
		},
		{
			name: "no patterns",
			files: map[string]string{
				"file1.tar.gz": "hash1",
				"file2.tar.gz": "hash2",
			},
			patterns: []string{},
			expected: 2,
			excluded: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterFiles(tt.files, tt.patterns)
			if len(result) != tt.expected {
				t.Errorf("filterFiles() returned %d items, want %d", len(result), tt.expected)
			}

			// Verify excluded files are not present
			for _, excluded := range tt.excluded {
				if _, ok := result[excluded]; ok {
					t.Errorf("excluded file should not be present: %s", excluded)
				}
			}
		})
	}
}

// TestChecksumLocalFile tests computing checksums of local files
func TestChecksumLocalFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("test content")

	err := os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash, err := checksumLocalFile(testFile)
	if err != nil {
		t.Fatalf("checksumLocalFile() failed: %v", err)
	}

	expectedHash := computeSHA256(testContent)
	if hash != expectedHash {
		t.Errorf("checksum mismatch: got %q, want %q", hash, expectedHash)
	}
}

// TestChecksumLocalFileNotFound tests handling of missing files
func TestChecksumLocalFileNotFound(t *testing.T) {
	hash, err := checksumLocalFile("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("checksumLocalFile() should fail for nonexistent file")
	}

	if hash != "" {
		t.Errorf("checksumLocalFile() returned non-empty hash for error: %q", hash)
	}
}

// TestBuildSyncPlan tests the sync plan building function
func TestBuildSyncPlan(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := tmpDir
	outputDir := "test-output"
	version := "1.0"

	// Create test files
	versionDir := filepath.Join(dataDir, outputDir, version)
	err := os.MkdirAll(versionDir, 0755)
	if err != nil {
		t.Fatalf("failed to create version dir: %v", err)
	}

	// File 1: exists with matching checksum
	file1Content := []byte("file1-content")
	file1Hash := computeSHA256(file1Content)
	file1Path := filepath.Join(versionDir, "file1.tar.gz")
	err = os.WriteFile(file1Path, file1Content, 0644)
	if err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}

	// File 2: doesn't exist (should be downloaded)
	file2Hash := "abc123def456"

	// File 3: exists with mismatched checksum (should be updated)
	file3Content := []byte("old-content")
	file3Path := filepath.Join(versionDir, "file3.tar.gz")
	err = os.WriteFile(file3Path, file3Content, 0644)
	if err != nil {
		t.Fatalf("failed to write file3: %v", err)
	}
	file3ExpectedHash := computeSHA256([]byte("new-content"))

	remoteFiles := map[string]string{
		"file1.tar.gz": file1Hash,
		"file2.tar.gz": file2Hash,
		"file3.tar.gz": file3ExpectedHash,
	}

	actions, err := buildSyncPlan("test-provider", "https://example.com", version, outputDir, dataDir, remoteFiles, testLogger())
	if err != nil {
		t.Fatalf("buildSyncPlan() failed: %v", err)
	}

	if len(actions) != 3 {
		t.Errorf("buildSyncPlan() returned %d actions, want 3", len(actions))
	}

	// Verify action types
	actionMap := make(map[string]provider.ActionType)
	for _, action := range actions {
		filename := filepath.Base(action.Path)
		actionMap[filename] = action.Action
	}

	if actionMap["file1.tar.gz"] != provider.ActionSkip {
		t.Errorf("file1 action = %q, want skip (checksum matches)", actionMap["file1.tar.gz"])
	}

	if actionMap["file2.tar.gz"] != provider.ActionDownload {
		t.Errorf("file2 action = %q, want download (doesn't exist)", actionMap["file2.tar.gz"])
	}

	if actionMap["file3.tar.gz"] != provider.ActionUpdate {
		t.Errorf("file3 action = %q, want update (checksum mismatch)", actionMap["file3.tar.gz"])
	}
}

func TestBuildSyncPlanRejectsTraversalFilename(t *testing.T) {
	_, err := buildSyncPlan(
		"test-provider",
		"https://example.com",
		"1.0",
		"test-output",
		t.TempDir(),
		map[string]string{"../../evil.bin": "deadbeef"},
		testLogger(),
	)
	if err == nil {
		t.Fatal("expected traversal filename to be rejected")
	}
	if !strings.Contains(err.Error(), "unsafe remote filename") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// containsFile checks if a file with given basename is in the sync actions
func containsFile(actions []provider.SyncAction, filename string) bool {
	for _, action := range actions {
		if filepath.Base(action.Path) == filename {
			return true
		}
	}
	return false
}
