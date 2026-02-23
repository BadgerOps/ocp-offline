package ocp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input                  string
		major, minor, patch    int
	}{
		{"4.21.3", 4, 21, 3},
		{"4.9.0", 4, 9, 0},
		{"4.10.15", 4, 10, 15},
		{"3.11.2", 3, 11, 2},
		{"4.21", 4, 21, 0},
		{"invalid", 0, 0, 0},
		{"", 0, 0, 0},
	}

	for _, tt := range tests {
		maj, min, patch := parseSemver(tt.input)
		if maj != tt.major || min != tt.minor || patch != tt.patch {
			t.Errorf("parseSemver(%q) = (%d,%d,%d), want (%d,%d,%d)",
				tt.input, maj, min, patch, tt.major, tt.minor, tt.patch)
		}
	}
}

func TestSemverLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"4.9.0", "4.10.0", true},
		{"4.10.0", "4.9.0", false},
		{"4.21.0", "4.21.1", true},
		{"4.21.3", "4.21.3", false},
		{"3.11.0", "4.1.0", true},
		{"4.21.9", "4.21.10", true},
	}

	for _, tt := range tests {
		got := semverLess(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("semverLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSortVersions(t *testing.T) {
	versions := []string{"4.21.3", "4.9.1", "4.10.0", "4.21.0", "4.10.15", "4.9.0"}
	SortVersions(versions)

	want := []string{"4.9.0", "4.9.1", "4.10.0", "4.10.15", "4.21.0", "4.21.3"}
	if len(versions) != len(want) {
		t.Fatalf("SortVersions: got %d elements, want %d", len(versions), len(want))
	}
	for i := range want {
		if versions[i] != want[i] {
			t.Errorf("SortVersions[%d] = %q, want %q", i, versions[i], want[i])
		}
	}
}

func TestGroupChannels(t *testing.T) {
	channels := []string{
		"stable-4.21", "stable-4.20", "stable-4.18",
		"fast-4.21", "fast-4.20",
		"eus-4.18", "eus-4.16",
		"candidate-4.22", "candidate-4.21",
	}

	result := groupChannels(channels)

	if len(result.Groups) != 4 {
		t.Fatalf("expected 4 groups, got %d", len(result.Groups))
	}

	// Verify order: stable, fast, eus, candidate
	expectedOrder := []string{"stable", "fast", "eus", "candidate"}
	for i, g := range result.Groups {
		if g.Type != expectedOrder[i] {
			t.Errorf("group[%d].Type = %q, want %q", i, g.Type, expectedOrder[i])
		}
	}

	// Verify stable channels sorted by minor desc
	stableGroup := result.Groups[0]
	if len(stableGroup.Channels) != 3 {
		t.Fatalf("stable group: got %d channels, want 3", len(stableGroup.Channels))
	}
	if stableGroup.Channels[0] != "stable-4.21" {
		t.Errorf("stable[0] = %q, want stable-4.21", stableGroup.Channels[0])
	}
	if stableGroup.Channels[2] != "stable-4.18" {
		t.Errorf("stable[2] = %q, want stable-4.18", stableGroup.Channels[2])
	}
}

func TestChannelMinor(t *testing.T) {
	tests := []struct {
		channel string
		want    int
	}{
		{"stable-4.21", 21},
		{"fast-4.9", 9},
		{"eus-4.18", 18},
		{"candidate-4.22", 22},
		{"invalid", 0},
		{"no-version", 0},
	}

	for _, tt := range tests {
		got := channelMinor(tt.channel)
		if got != tt.want {
			t.Errorf("channelMinor(%q) = %d, want %d", tt.channel, got, tt.want)
		}
	}
}

func TestFilterAndSortReleases(t *testing.T) {
	nodes := []graphNode{
		{Version: "4.21.3"},
		{Version: "4.21.1"},
		{Version: "4.21.19"},
		{Version: "4.21.2"},
		{Version: "4.20.5"},  // different minor, should be filtered out
		{Version: "4.16.11"}, // different minor, should be filtered out
		{Version: "4.21.3"},  // duplicate, should be deduped
	}

	result := filterAndSortReleases("stable-4.21", nodes)

	if result.Channel != "stable-4.21" {
		t.Errorf("Channel = %q, want stable-4.21", result.Channel)
	}

	expectedReleases := []string{"4.21.1", "4.21.2", "4.21.3", "4.21.19"}
	if len(result.Releases) != len(expectedReleases) {
		t.Fatalf("got %d releases, want %d: %v", len(result.Releases), len(expectedReleases), result.Releases)
	}
	for i, v := range expectedReleases {
		if result.Releases[i] != v {
			t.Errorf("Releases[%d] = %q, want %q", i, result.Releases[i], v)
		}
	}

	if result.Latest != "4.21.19" {
		t.Errorf("Latest = %q, want 4.21.19", result.Latest)
	}
	if result.Previous != "4.21.3" {
		t.Errorf("Previous = %q, want 4.21.3", result.Previous)
	}
}

func TestParseChecksumFile(t *testing.T) {
	data := `c203c436f0207fa3c19c37a7d3e12fceda814b5c90def33e1022e79b3b523519  openshift-client-linux-4.17.0.tar.gz
007d8bef0fd036c45f496fd00098bd6d9042eaf525a77793e892a1d500e0d041  openshift-install-linux-4.17.0.tar.gz
b05386d325452dfd900ac8204d87744263a7e77f5ae8a44786fee5c0d0638d3b  openshift-client-windows-4.17.0.zip
`

	result := ParseChecksumFile([]byte(data))
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result["openshift-client-linux-4.17.0.tar.gz"] != "c203c436f0207fa3c19c37a7d3e12fceda814b5c90def33e1022e79b3b523519" {
		t.Errorf("unexpected hash for client-linux")
	}
	if result["openshift-client-windows-4.17.0.zip"] != "b05386d325452dfd900ac8204d87744263a7e77f5ae8a44786fee5c0d0638d3b" {
		t.Errorf("unexpected hash for client-windows")
	}
}

func TestParseChecksumFileWithStarPrefix(t *testing.T) {
	data := `abc123  *somefile.tar.gz`
	result := ParseChecksumFile([]byte(data))
	if _, ok := result["somefile.tar.gz"]; !ok {
		t.Error("expected star prefix to be stripped from filename")
	}
}

func TestClassifyArtifact(t *testing.T) {
	tests := []struct {
		filename     string
		wantOS       string
		wantArch     string
		wantType     string
	}{
		{"openshift-client-linux-4.17.0.tar.gz", "linux", "amd64", "client"},
		{"openshift-client-linux-arm64-4.17.0.tar.gz", "linux", "arm64", "client"},
		{"openshift-install-mac-4.17.0.tar.gz", "mac", "amd64", "installer"},
		{"openshift-install-mac-arm64-4.17.0.tar.gz", "mac", "arm64", "installer"},
		{"openshift-client-windows-4.17.0.zip", "windows", "amd64", "client"},
		{"openshift-client-linux-ppc64le-4.17.0.tar.gz", "linux", "ppc64le", "client"},
		{"openshift-client-linux-s390x-rhel9-4.17.0.tar.gz", "linux", "s390x", "client"},
		{"ccoctl-linux-4.17.0.tar.gz", "linux", "amd64", "ccoctl"},
		{"opm-linux-4.17.0.tar.gz", "linux", "amd64", "opm"},
		{"oc-mirror.tar.gz", "linux", "amd64", "oc-mirror"},
		{"release.txt", "linux", "amd64", "other"},
	}

	for _, tt := range tests {
		a := classifyArtifact(tt.filename, "https://example.com", "abc123")
		if a.OS != tt.wantOS {
			t.Errorf("classifyArtifact(%q).OS = %q, want %q", tt.filename, a.OS, tt.wantOS)
		}
		if a.Arch != tt.wantArch {
			t.Errorf("classifyArtifact(%q).Arch = %q, want %q", tt.filename, a.Arch, tt.wantArch)
		}
		if a.Type != tt.wantType {
			t.Errorf("classifyArtifact(%q).Type = %q, want %q", tt.filename, a.Type, tt.wantType)
		}
		if a.Checksum != "abc123" {
			t.Errorf("classifyArtifact(%q).Checksum = %q, want abc123", tt.filename, a.Checksum)
		}
	}
}

func TestFilterArtifactsByPlatform(t *testing.T) {
	artifacts := []ClientArtifact{
		{Name: "oc-linux", OS: "linux", Arch: "amd64"},
		{Name: "oc-linux-arm64", OS: "linux", Arch: "arm64"},
		{Name: "oc-mac", OS: "mac", Arch: "amd64"},
		{Name: "oc-mac-arm64", OS: "mac", Arch: "arm64"},
		{Name: "oc-windows", OS: "windows", Arch: "amd64"},
	}

	// Filter for linux + linux-arm64
	filtered := FilterArtifactsByPlatform(artifacts, []string{"linux", "linux-arm64"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(filtered))
	}

	// Empty platforms = return all
	all := FilterArtifactsByPlatform(artifacts, nil)
	if len(all) != 5 {
		t.Fatalf("expected all 5 artifacts, got %d", len(all))
	}
}

func TestParseChecksumFileEmpty(t *testing.T) {
	result := ParseChecksumFile([]byte(""))
	if len(result) != 0 {
		t.Fatalf("expected 0 entries for empty input, got %d", len(result))
	}

	// Whitespace-only lines
	result = ParseChecksumFile([]byte("  \n\n  \n"))
	if len(result) != 0 {
		t.Fatalf("expected 0 entries for whitespace-only input, got %d", len(result))
	}
}

func TestParseChecksumFileMalformedLines(t *testing.T) {
	// Lines with only a hash (no filename) should be skipped
	data := "abc123\ndef456  somefile.tar.gz\n"
	result := ParseChecksumFile([]byte(data))
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if _, ok := result["somefile.tar.gz"]; !ok {
		t.Error("expected somefile.tar.gz to be present")
	}
}

func TestClassifyArtifactURL(t *testing.T) {
	a := classifyArtifact("openshift-client-linux-4.17.0.tar.gz", "https://mirror.example.com/v4/clients/ocp/4.17.0", "abc123")
	expectedURL := "https://mirror.example.com/v4/clients/ocp/4.17.0/openshift-client-linux-4.17.0.tar.gz"
	if a.URL != expectedURL {
		t.Errorf("URL = %q, want %q", a.URL, expectedURL)
	}
}

func TestFilterArtifactsByPlatformSinglePlatform(t *testing.T) {
	artifacts := []ClientArtifact{
		{Name: "a", OS: "linux", Arch: "amd64"},
		{Name: "b", OS: "linux", Arch: "arm64"},
		{Name: "c", OS: "linux", Arch: "ppc64le"},
		{Name: "d", OS: "mac", Arch: "amd64"},
		{Name: "e", OS: "windows", Arch: "amd64"},
	}

	// Only windows
	filtered := FilterArtifactsByPlatform(artifacts, []string{"windows"})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 windows artifact, got %d", len(filtered))
	}
	if filtered[0].Name != "e" {
		t.Errorf("expected artifact 'e', got %q", filtered[0].Name)
	}

	// Non-matching platform returns empty
	filtered = FilterArtifactsByPlatform(artifacts, []string{"linux-s390x"})
	if len(filtered) != 0 {
		t.Fatalf("expected 0 artifacts for linux-s390x, got %d", len(filtered))
	}
}

func TestExtractChannelsFromTarball(t *testing.T) {
	// Build a small gzipped tarball in memory with channels/ entries
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Real tarball uses .yaml file extensions for channel entries
	tarballNames := []string{"stable-4.21.yaml", "fast-4.21.yaml", "eus-4.18.yaml", "candidate-4.22.yaml"}
	expectedChannels := []string{"stable-4.21", "fast-4.21", "eus-4.18", "candidate-4.22"}
	for _, name := range tarballNames {
		hdr := &tar.Header{
			Name:     "channels/" + name,
			Mode:     0644,
			Size:     0,
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}

	// Also add a directory entry and a non-channel file that should be ignored
	tw.WriteHeader(&tar.Header{
		Name:     "channels/",
		Mode:     0755,
		Typeflag: tar.TypeDir,
	})
	tw.WriteHeader(&tar.Header{
		Name:     "other/file.txt",
		Mode:     0644,
		Size:     5,
		Typeflag: tar.TypeReg,
	})
	tw.Write([]byte("hello"))

	tw.Close()
	gw.Close()

	channels, err := extractChannelsFromTarball(&buf)
	if err != nil {
		t.Fatalf("extractChannelsFromTarball: %v", err)
	}

	if len(channels) != len(expectedChannels) {
		t.Fatalf("got %d channels, want %d: %v", len(channels), len(expectedChannels), channels)
	}

	// Verify all expected channels are present (with .yaml stripped)
	channelSet := make(map[string]bool)
	for _, ch := range channels {
		channelSet[ch] = true
	}
	for _, expected := range expectedChannels {
		if !channelSet[expected] {
			t.Errorf("missing channel %q, got channels: %v", expected, channels)
		}
	}
}
