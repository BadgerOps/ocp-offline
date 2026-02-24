package mirror

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/BadgerOps/airgap/internal/safety"
)

const defaultCacheTTL = 1 * time.Hour
const defaultMetalinkBaseURL = "https://mirrors.fedoraproject.org/metalink?repo=epel-%d&arch=%s"
const maxDiscoveryResponseBytes int64 = 16 * 1024 * 1024

type cacheEntry struct {
	data      interface{}
	fetchedAt time.Time
}

// Discovery provides methods to discover mirrors and versions for EPEL, OCP, and RHCOS,
// with an in-memory cache to avoid redundant upstream requests.
type Discovery struct {
	client          *http.Client
	logger          *slog.Logger
	cache           map[string]cacheEntry
	mu              sync.RWMutex
	cacheTTL        time.Duration
	metalinkBaseURL string
	ocpBaseURL      string
	rhcosBaseURL    string
}

// NewDiscovery creates a new Discovery service with sensible defaults.
func NewDiscovery(logger *slog.Logger) *Discovery {
	return &Discovery{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:          logger,
		cache:           make(map[string]cacheEntry),
		cacheTTL:        defaultCacheTTL,
		metalinkBaseURL: defaultMetalinkBaseURL,
		ocpBaseURL:      DefaultOCPBaseURL,
		rhcosBaseURL:    DefaultRHCOSBaseURL,
	}
}

// EPELVersions returns the known EPEL versions with their supported architectures.
func (d *Discovery) EPELVersions() []EPELVersionInfo {
	var result []EPELVersionInfo
	for _, v := range EPELVersions {
		result = append(result, EPELVersionInfo{
			Version:       v,
			Architectures: EPELArchitectures,
		})
	}
	return result
}

// EPELMirrors fetches and parses the metalink for the given EPEL version and architecture,
// returning discovered mirrors sorted by preference. Results are cached.
func (d *Discovery) EPELMirrors(ctx context.Context, version int, arch string) ([]MirrorInfo, error) {
	key := fmt.Sprintf("epel:%d:%s", version, arch)

	if cached, ok := d.getCache(key); ok {
		return cached.([]MirrorInfo), nil
	}

	url := fmt.Sprintf(d.metalinkBaseURL, version, arch)
	data, err := d.fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetching metalink for EPEL %d %s: %w", version, arch, err)
	}

	mirrors, err := parseMetalink(data)
	if err != nil {
		return nil, fmt.Errorf("parsing metalink for EPEL %d %s: %w", version, arch, err)
	}

	d.setCache(key, mirrors)
	return mirrors, nil
}

// OCPVersions fetches and parses the OCP directory listing, returning available versions.
// Results are cached.
func (d *Discovery) OCPVersions(ctx context.Context) ([]OCPVersion, error) {
	key := "ocp:versions"

	if cached, ok := d.getCache(key); ok {
		return cached.([]OCPVersion), nil
	}

	data, err := d.fetch(ctx, d.ocpBaseURL)
	if err != nil {
		return nil, fmt.Errorf("fetching OCP versions: %w", err)
	}

	versions := parseOCPDirectoryListing(data)
	d.setCache(key, versions)
	return versions, nil
}

// RHCOSVersions fetches the RHCOS directory listing, discovers minor versions,
// then fetches builds for each minor version. Results are cached.
// If fetching builds for a specific minor version fails, a warning is logged
// and that minor is included with an empty builds list.
func (d *Discovery) RHCOSVersions(ctx context.Context) ([]RHCOSVersion, error) {
	key := "rhcos:versions"

	if cached, ok := d.getCache(key); ok {
		return cached.([]RHCOSVersion), nil
	}

	data, err := d.fetch(ctx, d.rhcosBaseURL)
	if err != nil {
		return nil, fmt.Errorf("fetching RHCOS versions: %w", err)
	}

	minors := parseRHCOSMinorVersions(data)
	var versions []RHCOSVersion

	for _, minor := range minors {
		buildsURL := fmt.Sprintf("%s/%s", d.rhcosBaseURL, minor)
		buildsData, err := d.fetch(ctx, buildsURL)
		if err != nil {
			d.logger.Warn("failed to fetch RHCOS builds", "minor", minor, "error", err)
			versions = append(versions, RHCOSVersion{
				Minor:  minor,
				Builds: nil,
			})
			continue
		}

		builds := parseRHCOSBuilds(buildsData)
		versions = append(versions, RHCOSVersion{
			Minor:  minor,
			Builds: builds,
		})
	}

	d.setCache(key, versions)
	return versions, nil
}

// fetch performs an HTTP GET request with the given context and returns the response body.
// It sets a User-Agent header and returns an error for non-200 status codes.
func (d *Discovery) fetch(ctx context.Context, url string) ([]byte, error) {
	if _, err := safety.ValidateHTTPURL(url); err != nil {
		return nil, fmt.Errorf("invalid fetch URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "airgap/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}

	body, err := safety.ReadAllWithLimit(resp.Body, maxDiscoveryResponseBytes)
	if err != nil {
		if errors.Is(err, safety.ErrBodyTooLarge) {
			return nil, fmt.Errorf("response exceeded %d bytes for %s: %w", maxDiscoveryResponseBytes, url, err)
		}
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	return body, nil
}

// getCache retrieves a cached value if it exists and has not expired.
func (d *Discovery) getCache(key string) (interface{}, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entry, ok := d.cache[key]
	if !ok {
		return nil, false
	}

	if time.Since(entry.fetchedAt) > d.cacheTTL {
		return nil, false
	}

	return entry.data, true
}

// setCache stores a value in the cache with the current timestamp.
func (d *Discovery) setCache(key string, data interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.cache[key] = cacheEntry{
		data:      data,
		fetchedAt: time.Now(),
	}
}
