package ocp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BadgerOps/airgap/internal/safety"
)

const (
	graphDataURL = "https://api.openshift.com/api/upgrades_info/graph-data"
	graphAPIURL  = "https://api.openshift.com/api/upgrades_info/v1/graph"
	// ClientMirrorBase is the base URL for OCP client binary downloads.
	ClientMirrorBase = "https://mirror.openshift.com/pub/openshift-v4/clients/ocp"

	tracksCacheTTL         = 12 * time.Hour
	graphCacheTTL          = 1 * time.Hour
	maxManifestBytes int64 = 8 * 1024 * 1024
)

// TracksResult contains all discovered OCP channels grouped by track type.
type TracksResult struct {
	Groups []TrackGroup `json:"groups"`
}

// TrackGroup is a set of channels sharing the same type prefix.
type TrackGroup struct {
	Type     string   `json:"type"`     // "stable", "fast", "eus", "candidate"
	Channels []string `json:"channels"` // sorted by minor version descending
}

// ReleasesResult contains the available patch versions for a channel.
type ReleasesResult struct {
	Channel  string   `json:"channel"`
	Releases []string `json:"releases"` // semver sorted ascending
	Latest   string   `json:"latest"`
	Previous string   `json:"previous,omitempty"`
}

// ClientArtifact represents a downloadable OCP client binary.
type ClientArtifact struct {
	Name     string `json:"name"` // e.g. "openshift-client-linux-4.17.0.tar.gz"
	URL      string `json:"url"`
	OS       string `json:"os"`       // "linux", "mac", "windows"
	Arch     string `json:"arch"`     // "amd64", "arm64", "ppc64le", "s390x"
	Type     string `json:"type"`     // "client", "installer", "ccoctl", "opm", "oc-mirror", "other"
	Checksum string `json:"checksum"` // SHA256 hex from sha256sum.txt
}

// ManifestResult holds the parsed sha256sum.txt for a version.
type ManifestResult struct {
	Version   string            `json:"version"`
	Checksums map[string]string `json:"checksums"` // filename → sha256
	Artifacts []ClientArtifact  `json:"artifacts"`
}

// graphResponse is the JSON shape returned by the upgrades_info graph API.
type graphResponse struct {
	Nodes []graphNode `json:"nodes"`
}

type graphNode struct {
	Version  string            `json:"version"`
	Metadata map[string]string `json:"metadata"`
}

type graphCacheEntry struct {
	result *ReleasesResult
	expiry time.Time
}

// ClientService discovers OCP tracks, releases, and constructs download URLs.
type ClientService struct {
	httpClient *http.Client
	logger     *slog.Logger

	tracksMu    sync.RWMutex
	tracksCache *TracksResult
	tracksTTL   time.Time

	graphMu    sync.RWMutex
	graphCache map[string]*graphCacheEntry
}

// NewClientService creates a new OCP client discovery service.
func NewClientService(logger *slog.Logger) *ClientService {
	return &ClientService{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		logger:     logger,
		graphCache: make(map[string]*graphCacheEntry),
	}
}

// FetchTracks downloads the graph-data tarball and extracts channel names.
// Results are cached for 12 hours.
func (s *ClientService) FetchTracks(ctx context.Context) (*TracksResult, error) {
	s.tracksMu.RLock()
	if s.tracksCache != nil && time.Now().Before(s.tracksTTL) {
		cached := s.tracksCache
		s.tracksMu.RUnlock()
		return cached, nil
	}
	s.tracksMu.RUnlock()

	s.logger.Info("fetching OCP graph-data tarball", "url", graphDataURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, graphDataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating graph-data request: %w", err)
	}
	req.Header.Set("User-Agent", "airgap/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching graph-data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph-data returned status %d", resp.StatusCode)
	}

	channels, err := extractChannelsFromTarball(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing graph-data tarball: %w", err)
	}

	result := groupChannels(channels)

	s.logger.Info("fetched OCP tracks",
		slog.Int("total_channels", len(channels)),
		slog.Int("groups", len(result.Groups)))

	s.tracksMu.Lock()
	s.tracksCache = result
	s.tracksTTL = time.Now().Add(tracksCacheTTL)
	s.tracksMu.Unlock()

	return result, nil
}

// FetchReleases queries the graph API for a specific channel and returns
// the available patch versions sorted by semver.
func (s *ClientService) FetchReleases(ctx context.Context, channel string) (*ReleasesResult, error) {
	if channel == "" {
		return nil, fmt.Errorf("channel is required")
	}

	s.graphMu.RLock()
	if entry, ok := s.graphCache[channel]; ok && time.Now().Before(entry.expiry) {
		cached := entry.result
		s.graphMu.RUnlock()
		return cached, nil
	}
	s.graphMu.RUnlock()

	s.logger.Info("fetching OCP releases", "channel", channel)

	url := fmt.Sprintf("%s?channel=%s", graphAPIURL, channel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating graph request: %w", err)
	}
	req.Header.Set("User-Agent", "airgap/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching graph for %s: %w", channel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph API returned status %d for channel %s", resp.StatusCode, channel)
	}

	var graph graphResponse
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		return nil, fmt.Errorf("decoding graph response: %w", err)
	}

	result := filterAndSortReleases(channel, graph.Nodes)

	s.logger.Info("fetched OCP releases",
		"channel", channel,
		"count", len(result.Releases),
		"latest", result.Latest)

	s.graphMu.Lock()
	s.graphCache[channel] = &graphCacheEntry{
		result: result,
		expiry: time.Now().Add(graphCacheTTL),
	}
	s.graphMu.Unlock()

	return result, nil
}

// FetchManifest downloads and parses the sha256sum.txt for a given version,
// returning all artifacts with their checksums. This is the source of truth
// for what files are available and their expected hashes.
func (s *ClientService) FetchManifest(ctx context.Context, version string) (*ManifestResult, error) {
	url := fmt.Sprintf("%s/%s/sha256sum.txt", ClientMirrorBase, version)

	s.logger.Info("fetching OCP client manifest", "version", version, "url", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating manifest request: %w", err)
	}
	req.Header.Set("User-Agent", "airgap/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching manifest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest returned status %d for version %s", resp.StatusCode, version)
	}

	body, err := safety.ReadAllWithLimit(resp.Body, maxManifestBytes)
	if err != nil {
		if errors.Is(err, safety.ErrBodyTooLarge) {
			return nil, fmt.Errorf("manifest exceeded %d bytes for version %s: %w", maxManifestBytes, version, err)
		}
		return nil, fmt.Errorf("reading manifest body: %w", err)
	}

	checksums := ParseChecksumFile(body)
	if len(checksums) == 0 {
		return nil, fmt.Errorf("no entries found in sha256sum.txt for version %s", version)
	}

	base := fmt.Sprintf("%s/%s", ClientMirrorBase, version)
	result := &ManifestResult{
		Version:   version,
		Checksums: checksums,
	}

	for filename, hash := range checksums {
		artifact := classifyArtifact(filename, base, hash)
		result.Artifacts = append(result.Artifacts, artifact)
	}

	// Sort artifacts by name for deterministic output
	sort.Slice(result.Artifacts, func(i, j int) bool {
		return result.Artifacts[i].Name < result.Artifacts[j].Name
	})

	s.logger.Info("fetched OCP client manifest",
		slog.String("version", version),
		slog.Int("artifacts", len(result.Artifacts)))

	return result, nil
}

// classifyArtifact determines the OS, arch, and type of an artifact from its filename.
func classifyArtifact(filename, baseURL, checksum string) ClientArtifact {
	a := ClientArtifact{
		Name:     filename,
		URL:      baseURL + "/" + filename,
		Checksum: checksum,
	}

	lower := strings.ToLower(filename)

	// Determine OS
	switch {
	case strings.Contains(lower, "-mac") || strings.Contains(lower, "-darwin"):
		a.OS = "mac"
	case strings.Contains(lower, "-windows"):
		a.OS = "windows"
	default:
		a.OS = "linux"
	}

	// Determine arch
	switch {
	case strings.Contains(lower, "-arm64"):
		a.Arch = "arm64"
	case strings.Contains(lower, "-ppc64le"):
		a.Arch = "ppc64le"
	case strings.Contains(lower, "-s390x"):
		a.Arch = "s390x"
	default:
		a.Arch = "amd64"
	}

	// Determine type
	switch {
	case strings.HasPrefix(lower, "openshift-client"):
		a.Type = "client"
	case strings.HasPrefix(lower, "openshift-install"):
		a.Type = "installer"
	case strings.HasPrefix(lower, "ccoctl"):
		a.Type = "ccoctl"
	case strings.HasPrefix(lower, "opm"):
		a.Type = "opm"
	case strings.HasPrefix(lower, "oc-mirror"):
		a.Type = "oc-mirror"
	default:
		a.Type = "other"
	}

	return a
}

// ParseChecksumFile parses a sha256sum.txt into a map of filename → hash.
// Format: each line is "{hash}  {filename}" (two spaces between hash and name).
func ParseChecksumFile(data []byte) map[string]string {
	files := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			hash := parts[0]
			// Handle the "*filename" prefix some sha256sum implementations use
			filename := strings.TrimPrefix(parts[1], "*")
			files[filename] = hash
		}
	}
	return files
}

// FilterArtifactsByPlatform filters artifacts by the given platform list.
// Platform format: "linux", "linux-arm64", "mac", "mac-arm64", "windows".
// If platforms is empty, returns all artifacts.
func FilterArtifactsByPlatform(artifacts []ClientArtifact, platforms []string) []ClientArtifact {
	if len(platforms) == 0 {
		return artifacts
	}

	platformSet := make(map[string]bool, len(platforms))
	for _, p := range platforms {
		platformSet[p] = true
	}

	var filtered []ClientArtifact
	for _, a := range artifacts {
		platform := a.OS
		if a.Arch != "amd64" {
			platform = a.OS + "-" + a.Arch
		}
		if platformSet[platform] {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// extractChannelsFromTarball reads a gzipped tarball and returns channel names
// found in the channels/ directory.
func extractChannelsFromTarball(r io.Reader) ([]string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("opening gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var channels []string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tarball entry: %w", err)
		}

		// We only care about regular files under channels/
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		dir, name := path.Split(hdr.Name)
		// Handle both "channels/stable-4.21" and "some-prefix/channels/stable-4.21"
		if path.Base(dir) == "channels" && name != "" {
			// Strip file extensions (e.g. ".yaml") from channel names
			name = strings.TrimSuffix(name, path.Ext(name))
			channels = append(channels, name)
		}
	}

	if len(channels) == 0 {
		return nil, fmt.Errorf("no channels found in tarball")
	}

	return channels, nil
}

// groupChannels organizes channel names into TrackGroups by type prefix.
func groupChannels(channels []string) *TracksResult {
	groups := map[string][]string{}
	for _, ch := range channels {
		// Channel names: "stable-4.21", "fast-4.17", "eus-4.18", "candidate-4.22"
		idx := strings.Index(ch, "-")
		if idx < 0 {
			continue
		}
		trackType := ch[:idx]
		groups[trackType] = append(groups[trackType], ch)
	}

	// Sort channels within each group by minor version descending
	for _, chs := range groups {
		sort.Slice(chs, func(i, j int) bool {
			return channelMinor(chs[i]) > channelMinor(chs[j])
		})
	}

	// Build result in a consistent order
	typeOrder := []string{"stable", "fast", "eus", "candidate"}
	var result TracksResult

	for _, t := range typeOrder {
		if chs, ok := groups[t]; ok {
			result.Groups = append(result.Groups, TrackGroup{
				Type:     t,
				Channels: chs,
			})
			delete(groups, t)
		}
	}
	// Any remaining types not in the predefined order
	remaining := make([]string, 0, len(groups))
	for t := range groups {
		remaining = append(remaining, t)
	}
	sort.Strings(remaining)
	for _, t := range remaining {
		result.Groups = append(result.Groups, TrackGroup{
			Type:     t,
			Channels: groups[t],
		})
	}

	return &result
}

// channelMinor extracts the minor version number from a channel name.
// e.g. "stable-4.21" → 21, "eus-4.18" → 18
func channelMinor(channel string) int {
	idx := strings.LastIndex(channel, "-")
	if idx < 0 {
		return 0
	}
	versionPart := channel[idx+1:] // "4.21"
	parts := strings.SplitN(versionPart, ".", 2)
	if len(parts) < 2 {
		return 0
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return minor
}

// filterAndSortReleases extracts versions from graph nodes that match
// the channel's minor version and sorts them by semver.
func filterAndSortReleases(channel string, nodes []graphNode) *ReleasesResult {
	// Extract the minor prefix from channel name: "stable-4.21" → "4.21."
	idx := strings.Index(channel, "-")
	minorPrefix := ""
	if idx >= 0 {
		minorPrefix = channel[idx+1:] + "."
	}

	var versions []string
	seen := make(map[string]bool)
	for _, node := range nodes {
		v := node.Version
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		// Only include versions matching the channel's minor
		if minorPrefix != "" && !strings.HasPrefix(v, minorPrefix) {
			continue
		}
		versions = append(versions, v)
	}

	SortVersions(versions)

	result := &ReleasesResult{
		Channel:  channel,
		Releases: versions,
	}

	if len(versions) > 0 {
		result.Latest = versions[len(versions)-1]
	}
	if len(versions) > 1 {
		result.Previous = versions[len(versions)-2]
	}

	return result
}

// SortVersions sorts a slice of semver strings in ascending order
// using numeric comparison (4.9.1 < 4.10.0 < 4.21.3).
func SortVersions(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		return semverLess(versions[i], versions[j])
	})
}

// semverLess compares two semver strings numerically.
func semverLess(a, b string) bool {
	aMaj, aMin, aPatch := parseSemver(a)
	bMaj, bMin, bPatch := parseSemver(b)

	if aMaj != bMaj {
		return aMaj < bMaj
	}
	if aMin != bMin {
		return aMin < bMin
	}
	return aPatch < bPatch
}

// parseSemver splits a version string into numeric major, minor, patch.
// Returns (0,0,0) for unparseable input.
func parseSemver(v string) (major, minor, patch int) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 1 {
		major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		patch, _ = strconv.Atoi(parts[2])
	}
	return
}
