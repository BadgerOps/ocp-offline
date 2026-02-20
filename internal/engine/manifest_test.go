package engine

import (
	"encoding/json"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	m := &TransferManifest{
		Version:    "1.0",
		Created:    time.Date(2026, 2, 19, 14, 30, 0, 0, time.UTC),
		SourceHost: "sync-server.example.com",
		Providers: map[string]ManifestProvider{
			"epel": {FileCount: 100, TotalSize: 1024000},
		},
		Archives: []ManifestArchive{
			{
				Name:   "airgap-transfer-001.tar.zst",
				Size:   512000,
				SHA256: "abc123",
				Files:  []string{"epel/9/foo.rpm"},
			},
		},
		TotalArchives: 1,
		TotalSize:     512000,
		FileInventory: []ManifestFile{
			{Provider: "epel", Path: "9/foo.rpm", Size: 1024, SHA256: "def456"},
		},
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded TransferManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Version != "1.0" {
		t.Errorf("version = %q, want %q", decoded.Version, "1.0")
	}
	if decoded.SourceHost != "sync-server.example.com" {
		t.Errorf("source_host = %q, want %q", decoded.SourceHost, "sync-server.example.com")
	}
	if len(decoded.Providers) != 1 {
		t.Fatalf("providers count = %d, want 1", len(decoded.Providers))
	}
	if decoded.Providers["epel"].FileCount != 100 {
		t.Errorf("epel file_count = %d, want 100", decoded.Providers["epel"].FileCount)
	}
	if len(decoded.Archives) != 1 {
		t.Fatalf("archives count = %d, want 1", len(decoded.Archives))
	}
	if decoded.Archives[0].SHA256 != "abc123" {
		t.Errorf("archive sha256 = %q, want %q", decoded.Archives[0].SHA256, "abc123")
	}
	if len(decoded.FileInventory) != 1 {
		t.Fatalf("file_inventory count = %d, want 1", len(decoded.FileInventory))
	}
	if decoded.FileInventory[0].Provider != "epel" {
		t.Errorf("file provider = %q, want %q", decoded.FileInventory[0].Provider, "epel")
	}
}
