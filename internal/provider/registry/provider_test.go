package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BadgerOps/airgap/internal/provider"
)

func TestConfigure_MissingEndpoint(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	err := p.Configure(provider.ProviderConfig{
		"repositories": []interface{}{"library/alpine"},
	})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("expected endpoint error, got %v", err)
	}
}

func TestConfigure_MissingRepositories(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	err := p.Configure(provider.ProviderConfig{
		"endpoint": "quay.io",
	})
	if err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("expected repository error, got %v", err)
	}
}

func TestConfigure_Success(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	err := p.Configure(provider.ProviderConfig{
		"endpoint":     "quay.io",
		"repositories": []interface{}{"org/repo", "org/repo2"},
		"tags":         []interface{}{"v1.*", "latest"},
		"output_dir":   "my-images",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.cfg.Endpoint != "quay.io" {
		t.Errorf("expected endpoint quay.io, got %s", p.cfg.Endpoint)
	}
	if len(p.cfg.Repositories) != 2 {
		t.Errorf("expected 2 repositories, got %d", len(p.cfg.Repositories))
	}
	if p.cfg.OutputDir != "my-images" {
		t.Errorf("expected output_dir my-images, got %s", p.cfg.OutputDir)
	}
}

func TestConfigure_DefaultOutputDir(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	err := p.Configure(provider.ProviderConfig{
		"endpoint":     "quay.io",
		"repositories": []interface{}{"org/repo"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.cfg.OutputDir != "registry-images" {
		t.Errorf("expected default output_dir registry-images, got %s", p.cfg.OutputDir)
	}
}

func TestConfigure_DeduplicatesRepositories(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	err := p.Configure(provider.ProviderConfig{
		"endpoint":     "quay.io",
		"repositories": []interface{}{"org/repo", "org/repo", "org/other"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p.cfg.Repositories) != 2 {
		t.Errorf("expected 2 unique repositories, got %d: %v", len(p.cfg.Repositories), p.cfg.Repositories)
	}
}

func TestFilterTags_NoPatterns(t *testing.T) {
	tags := []string{"v1.0", "v1.1", "latest", "nightly"}
	result := filterTags(tags, nil)
	if len(result) != len(tags) {
		t.Errorf("expected all %d tags, got %d", len(tags), len(result))
	}
}

func TestFilterTags_WithPatterns(t *testing.T) {
	tags := []string{"v1.0", "v1.1", "v2.0", "latest", "nightly"}
	result := filterTags(tags, []string{"v1.*", "latest"})
	expected := map[string]bool{"v1.0": true, "v1.1": true, "latest": true}
	if len(result) != len(expected) {
		t.Errorf("expected %d tags, got %d: %v", len(expected), len(result), result)
	}
	for _, tag := range result {
		if !expected[tag] {
			t.Errorf("unexpected tag %q in result", tag)
		}
	}
}

func TestFilterTags_ExactMatch(t *testing.T) {
	tags := []string{"4.16.35", "4.17.0"}
	result := filterTags(tags, []string{"4.16.35"})
	if len(result) != 1 || result[0] != "4.16.35" {
		t.Errorf("expected [4.16.35], got %v", result)
	}
}

func TestNormalizeEndpointHost(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"quay.io", "quay.io"},
		{"https://quay.io", "quay.io"},
		{"http://quay.io/", "quay.io"},
		{"docker.io", "registry-1.docker.io"},
		{"index.docker.io", "registry-1.docker.io"},
		{"myregistry.example.com:5000", "myregistry.example.com:5000"},
	}
	for _, tt := range tests {
		got := normalizeEndpointHost(tt.in)
		if got != tt.want {
			t.Errorf("normalizeEndpointHost(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestImagePathID(t *testing.T) {
	ref := imageReference{
		Registry:   "quay.io",
		Repository: "org/repo",
		Reference:  "v1.0",
	}
	id := imagePathID(ref)
	if id == "" {
		t.Error("expected non-empty image path ID")
	}
	if strings.Contains(id, "/") {
		t.Errorf("image path ID should not contain slashes: %q", id)
	}
	if !strings.Contains(id, "quay.io") || !strings.Contains(id, "org") {
		t.Errorf("expected image path ID to contain registry/org info: %q", id)
	}
}

func TestParseDigest(t *testing.T) {
	algo, hash, err := parseDigest("sha256:abcdef0123456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if algo != "sha256" || hash != "abcdef0123456789" {
		t.Errorf("got algo=%q hash=%q", algo, hash)
	}

	_, _, err = parseDigest("invalid")
	if err == nil {
		t.Error("expected error for invalid digest")
	}

	_, _, err = parseDigest("sha256:")
	if err == nil {
		t.Error("expected error for empty hex")
	}
}

func TestNameAndType(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	if p.Name() != "registry" {
		t.Errorf("expected name 'registry', got %q", p.Name())
	}
	if p.Type() != "registry" {
		t.Errorf("expected type 'registry', got %q", p.Type())
	}
	p.SetName("my-registry")
	if p.Name() != "my-registry" {
		t.Errorf("expected name 'my-registry' after SetName, got %q", p.Name())
	}
}

func TestPlan_WithMockRegistry(t *testing.T) {
	// Build a minimal manifest
	configBlob := []byte(`{"architecture":"amd64"}`)
	configDigest := "sha256:" + sha256Hex(configBlob)

	layerBlob := []byte("fake layer data")
	layerDigest := "sha256:" + sha256Hex(layerBlob)

	manifest := map[string]interface{}{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]interface{}{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    configDigest,
			"size":      len(configBlob),
		},
		"layers": []map[string]interface{}{
			{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"digest":    layerDigest,
				"size":      len(layerBlob),
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	manifestDigest := "sha256:" + sha256Hex(manifestBytes)

	mux := http.NewServeMux()

	// Tags list endpoint
	mux.HandleFunc("/v2/org/repo/tags/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "org/repo",
			"tags": []string{"v1.0", "v2.0"},
		})
	})

	// Manifest endpoint (both by tag and by digest)
	mux.HandleFunc("/v2/org/repo/manifests/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Docker-Content-Digest", manifestDigest)
		_, _ = w.Write(manifestBytes)
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	// Extract host from server URL
	host := strings.TrimPrefix(server.URL, "https://")

	dataDir := t.TempDir()
	p := NewProvider(dataDir, nil)
	p.name = "test-registry"
	p.http = server.Client()

	err := p.Configure(provider.ProviderConfig{
		"endpoint":     host,
		"repositories": []interface{}{"org/repo"},
		"tags":         []interface{}{"v1.*"},
		"output_dir":   "images",
	})
	if err != nil {
		t.Fatalf("configure error: %v", err)
	}

	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan error: %v", err)
	}

	if plan.Provider != "test-registry" {
		t.Errorf("expected provider 'test-registry', got %q", plan.Provider)
	}

	// Should have: 1 manifest + 1 config blob + 1 layer blob = 3 actions
	// (only v1.0 matches the filter, v2.0 is excluded)
	if len(plan.Actions) != 3 {
		t.Errorf("expected 3 actions (manifest + config + layer), got %d", len(plan.Actions))
		for _, a := range plan.Actions {
			t.Logf("  action: %s %s (reason: %s)", a.Action, a.Path, a.Reason)
		}
	}

	// Verify all actions are downloads (nothing local yet)
	for _, a := range plan.Actions {
		if a.Action != provider.ActionDownload {
			t.Errorf("expected download action, got %s for %s", a.Action, a.Path)
		}
	}
}

func TestValidate_EmptyDir(t *testing.T) {
	dataDir := t.TempDir()
	p := NewProvider(dataDir, nil)
	p.SetName("test-reg")
	err := p.Configure(provider.ProviderConfig{
		"endpoint":     "quay.io",
		"repositories": []interface{}{"org/repo"},
	})
	if err != nil {
		t.Fatalf("configure error: %v", err)
	}

	report, err := p.Validate(context.Background())
	if err != nil {
		t.Fatalf("validate error: %v", err)
	}
	if report.TotalFiles != 0 {
		t.Errorf("expected 0 files in empty dir, got %d", report.TotalFiles)
	}
}

func TestValidate_ValidFile(t *testing.T) {
	dataDir := t.TempDir()
	p := NewProvider(dataDir, nil)
	p.SetName("test-reg")
	err := p.Configure(provider.ProviderConfig{
		"endpoint":     "quay.io",
		"repositories": []interface{}{"org/repo"},
		"output_dir":   "images",
	})
	if err != nil {
		t.Fatalf("configure error: %v", err)
	}

	// Create a file with valid checksum embedded in path
	content := []byte("test manifest content")
	hash := sha256Hex(content)
	manifestDir := filepath.Join(dataDir, "test-reg", "images", "test-image", "manifests", "sha256")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, hash+".json"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := p.Validate(context.Background())
	if err != nil {
		t.Fatalf("validate error: %v", err)
	}
	if report.TotalFiles != 1 {
		t.Errorf("expected 1 file, got %d", report.TotalFiles)
	}
	if report.ValidFiles != 1 {
		t.Errorf("expected 1 valid file, got %d", report.ValidFiles)
	}
	if len(report.InvalidFiles) != 0 {
		t.Errorf("expected 0 invalid files, got %d", len(report.InvalidFiles))
	}
}

func TestValidate_InvalidFile(t *testing.T) {
	dataDir := t.TempDir()
	p := NewProvider(dataDir, nil)
	p.SetName("test-reg")
	err := p.Configure(provider.ProviderConfig{
		"endpoint":     "quay.io",
		"repositories": []interface{}{"org/repo"},
		"output_dir":   "images",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a file with wrong checksum
	content := []byte("corrupted data")
	wrongHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	manifestDir := filepath.Join(dataDir, "test-reg", "images", "test-image", "manifests", "sha256")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, wrongHash+".json"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := p.Validate(context.Background())
	if err != nil {
		t.Fatalf("validate error: %v", err)
	}
	if report.TotalFiles != 1 {
		t.Errorf("expected 1 file, got %d", report.TotalFiles)
	}
	if len(report.InvalidFiles) != 1 {
		t.Errorf("expected 1 invalid file, got %d", len(report.InvalidFiles))
	}
}

func TestSync_DryRun(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	plan := &provider.SyncPlan{
		Provider: "test",
		Actions: []provider.SyncAction{
			{Path: "a", Action: provider.ActionDownload, Size: 100},
			{Path: "b", Action: provider.ActionSkip, Size: 50},
		},
	}
	report, err := p.Sync(context.Background(), plan, provider.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Downloaded != 0 {
		t.Errorf("dry run should have 0 downloaded, got %d", report.Downloaded)
	}
}

func TestSync_CountsActions(t *testing.T) {
	p := NewProvider(t.TempDir(), nil)
	plan := &provider.SyncPlan{
		Provider: "test",
		Actions: []provider.SyncAction{
			{Path: "a", Action: provider.ActionDownload, Size: 100},
			{Path: "b", Action: provider.ActionSkip, Size: 50},
			{Path: "c", Action: provider.ActionUpdate, Size: 200},
			{Path: "d", Action: provider.ActionDelete},
		},
	}
	report, err := p.Sync(context.Background(), plan, provider.SyncOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Downloaded != 2 {
		t.Errorf("expected 2 downloaded, got %d", report.Downloaded)
	}
	if report.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", report.Skipped)
	}
	if report.Deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", report.Deleted)
	}
	if report.BytesTransferred != 300 {
		t.Errorf("expected 300 bytes, got %d", report.BytesTransferred)
	}
}

func TestExpectedDigestFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"images/test/manifests/sha256/abc123.json", "sha256:abc123", true},
		{"images/test/blobs/sha256/def456", "sha256:def456", true},
		{"images/test/other/sha256/xyz", "", false},
		{"random/file.txt", "", false},
	}
	for _, tt := range tests {
		got, ok := expectedDigestFromPath(tt.path)
		if ok != tt.ok || got != tt.want {
			t.Errorf("expectedDigestFromPath(%q) = (%q, %v), want (%q, %v)", tt.path, got, ok, tt.want, tt.ok)
		}
	}
}

func TestListTags_WithMockServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/org/repo/tags/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"name":"org/repo","tags":["v1.0","v2.0","latest"]}`)
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "https://")
	p := NewProvider(t.TempDir(), nil)
	p.http = server.Client()

	tags, err := p.listTags(context.Background(), host, "org/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(tags), tags)
	}
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
