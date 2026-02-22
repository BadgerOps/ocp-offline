# Mirror Auto-Discovery Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Auto-discover upstream EPEL mirrors and OCP/RHCOS versions, let users pick from dropdowns, and rank mirrors by speed test.

**Architecture:** New `internal/mirror` package with `MirrorDiscovery` service that fetches Fedora metalink XML (EPEL mirrors) and scrapes HTML directory listings (OCP/RHCOS versions). In-memory cache with 1-hour TTL. Four new API endpoints. Enhanced providers.html UI with Alpine.js.

**Tech Stack:** Go stdlib (`encoding/xml`, `net/http`, `sync`, `html`, `strings`), Alpine.js for UI.

---

### Task 1: MirrorInfo types and EPEL metalink parser

**Files:**
- Create: `internal/mirror/types.go`
- Create: `internal/mirror/epel.go`
- Create: `internal/mirror/epel_test.go`

**Step 1: Create types**

Create `internal/mirror/types.go`:

```go
package mirror

// MirrorInfo represents a single upstream mirror.
type MirrorInfo struct {
	URL        string `json:"url"`
	Country    string `json:"country"`
	Protocol   string `json:"protocol"`
	Preference int    `json:"preference"`
}

// SpeedResult represents a speed test result for a single mirror.
type SpeedResult struct {
	URL           string  `json:"url"`
	LatencyMs     int     `json:"latency_ms"`
	ThroughputKBps float64 `json:"throughput_kbps"`
	Error         string  `json:"error,omitempty"`
}

// OCPVersion represents an available OCP release version.
type OCPVersion struct {
	Version string `json:"version"`
	Channel string `json:"channel"` // "stable", "fast", "candidate", "release"
}

// RHCOSVersion represents an available RHCOS minor version with its builds.
type RHCOSVersion struct {
	Minor  string   `json:"minor"`
	Builds []string `json:"builds"`
}

// EPELVersionInfo describes a known EPEL version.
type EPELVersionInfo struct {
	Version       int      `json:"version"`
	Architectures []string `json:"architectures"`
}
```

**Step 2: Write the metalink parser test**

Create `internal/mirror/epel_test.go`:

```go
package mirror

import (
	"testing"
)

const testMetalinkXML = `<?xml version="1.0" encoding="utf-8"?>
<metalink version="3.0" xmlns="http://www.metalinker.org/" xmlns:mm0="http://fedorahosted.org/mirrormanager">
  <files>
    <file name="repomd.xml">
      <resources maxconnections="1">
        <url protocol="https" type="https" location="US" preference="100">https://mirror1.example.com/epel/9/Everything/x86_64/repodata/repomd.xml</url>
        <url protocol="https" type="https" location="DE" preference="80">https://mirror2.example.de/epel/9/Everything/x86_64/repodata/repomd.xml</url>
        <url protocol="http" type="http" location="JP" preference="60">http://mirror3.example.jp/epel/9/Everything/x86_64/repodata/repomd.xml</url>
      </resources>
    </file>
  </files>
</metalink>`

func TestParseMetalink(t *testing.T) {
	mirrors, err := parseMetalink([]byte(testMetalinkXML))
	if err != nil {
		t.Fatalf("parseMetalink failed: %v", err)
	}

	if len(mirrors) != 3 {
		t.Fatalf("expected 3 mirrors, got %d", len(mirrors))
	}

	// Should be sorted by preference descending
	if mirrors[0].Preference != 100 {
		t.Errorf("expected first mirror preference 100, got %d", mirrors[0].Preference)
	}
	if mirrors[0].Country != "US" {
		t.Errorf("expected first mirror country US, got %s", mirrors[0].Country)
	}
	if mirrors[0].Protocol != "https" {
		t.Errorf("expected first mirror protocol https, got %s", mirrors[0].Protocol)
	}

	// URL should be trimmed to base URL (strip /repodata/repomd.xml suffix)
	expected := "https://mirror1.example.com/epel/9/Everything/x86_64"
	if mirrors[0].URL != expected {
		t.Errorf("expected URL %q, got %q", expected, mirrors[0].URL)
	}

	if mirrors[2].Protocol != "http" {
		t.Errorf("expected third mirror protocol http, got %s", mirrors[2].Protocol)
	}
}

func TestParseMetalinkEmpty(t *testing.T) {
	mirrors, err := parseMetalink([]byte(`<?xml version="1.0"?><metalink xmlns="http://www.metalinker.org/"><files></files></metalink>`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mirrors) != 0 {
		t.Errorf("expected 0 mirrors, got %d", len(mirrors))
	}
}

func TestParseMetalinkInvalid(t *testing.T) {
	_, err := parseMetalink([]byte("not xml"))
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}
```

**Step 3: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run TestParseMetalink -v`
Expected: FAIL (package doesn't exist yet)

**Step 4: Implement metalink parser**

Create `internal/mirror/epel.go`:

```go
package mirror

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// Metalink XML structures
type metalinkXML struct {
	XMLName xml.Name       `xml:"metalink"`
	Files   metalinkFiles  `xml:"files"`
}

type metalinkFiles struct {
	File []metalinkFile `xml:"file"`
}

type metalinkFile struct {
	Name      string            `xml:"name,attr"`
	Resources metalinkResources `xml:"resources"`
}

type metalinkResources struct {
	URLs []metalinkURL `xml:"url"`
}

type metalinkURL struct {
	Protocol   string `xml:"protocol,attr"`
	Type       string `xml:"type,attr"`
	Location   string `xml:"location,attr"`
	Preference int    `xml:"preference,attr"`
	URL        string `xml:",chardata"`
}

// Known EPEL versions and architectures
var (
	EPELVersions      = []int{7, 8, 9, 10}
	EPELArchitectures = []string{"x86_64", "aarch64", "ppc64le", "s390x"}
)

// parseMetalink parses Fedora metalink XML and returns a list of mirrors
// sorted by preference (highest first). Mirror URLs are trimmed to the
// repo base URL (the /repodata/repomd.xml suffix is removed).
func parseMetalink(data []byte) ([]MirrorInfo, error) {
	var ml metalinkXML
	if err := xml.Unmarshal(data, &ml); err != nil {
		return nil, fmt.Errorf("parsing metalink XML: %w", err)
	}

	var mirrors []MirrorInfo
	for _, f := range ml.Files.File {
		for _, u := range f.Resources.URLs {
			url := strings.TrimSpace(u.URL)
			// Strip /repodata/repomd.xml suffix to get base repo URL
			if idx := strings.Index(url, "/repodata/"); idx != -1 {
				url = url[:idx]
			}

			mirrors = append(mirrors, MirrorInfo{
				URL:        url,
				Country:    u.Location,
				Protocol:   u.Protocol,
				Preference: u.Preference,
			})
		}
	}

	sort.Slice(mirrors, func(i, j int) bool {
		return mirrors[i].Preference > mirrors[j].Preference
	})

	return mirrors, nil
}
```

**Step 5: Run tests to verify they pass**

Run: `go test ./internal/mirror/ -run TestParseMetalink -v`
Expected: PASS (all 3 tests)

**Step 6: Commit**

```bash
git add internal/mirror/
git commit -m "feat: add mirror types and EPEL metalink parser"
```

---

### Task 2: OCP/RHCOS HTML directory parser

**Files:**
- Create: `internal/mirror/ocp.go`
- Create: `internal/mirror/ocp_test.go`

**Step 1: Write the directory parser test**

Create `internal/mirror/ocp_test.go`:

```go
package mirror

import (
	"testing"
)

const testOCPDirHTML = `<html><body>
<a href="4.14.41/">4.14.41/</a>
<a href="4.17.48/">4.17.48/</a>
<a href="4.18.3/">4.18.3/</a>
<a href="stable-4.17/">stable-4.17/</a>
<a href="fast-4.17/">fast-4.17/</a>
<a href="candidate-4.18/">candidate-4.18/</a>
<a href="latest-4.17/">latest-4.17/</a>
<a href="latest/">latest/</a>
<a href="stable/">stable/</a>
<a href="4.18.0-rc.2/">4.18.0-rc.2/</a>
<a href="4.12.0-ec.1/">4.12.0-ec.1/</a>
</body></html>`

func TestParseOCPVersions(t *testing.T) {
	versions := parseOCPDirectoryListing([]byte(testOCPDirHTML))

	channels := map[string]int{}
	for _, v := range versions {
		channels[v.Channel]++
	}

	if channels["stable"] != 1 {
		t.Errorf("expected 1 stable channel entry, got %d", channels["stable"])
	}
	if channels["fast"] != 1 {
		t.Errorf("expected 1 fast channel entry, got %d", channels["fast"])
	}
	if channels["candidate"] != 1 {
		t.Errorf("expected 1 candidate channel entry, got %d", channels["candidate"])
	}
	// 4.14.41, 4.17.48, 4.18.3 = specific releases; rc/ec excluded
	if channels["release"] < 3 {
		t.Errorf("expected at least 3 release entries, got %d", channels["release"])
	}
}

const testRHCOSDirHTML = `<html><body>
<a href="4.14/">4.14/</a>
<a href="4.17/">4.17/</a>
<a href="4.18/">4.18/</a>
<a href="latest/">latest/</a>
<a href="pre-release/">pre-release/</a>
</body></html>`

const testRHCOSBuildsHTML = `<html><body>
<a href="4.17.0/">4.17.0/</a>
<a href="4.17.1/">4.17.1/</a>
<a href="4.17.42/">4.17.42/</a>
<a href="latest/">latest/</a>
</body></html>`

func TestParseRHCOSMinorVersions(t *testing.T) {
	minors := parseRHCOSMinorVersions([]byte(testRHCOSDirHTML))

	if len(minors) != 3 {
		t.Fatalf("expected 3 minor versions, got %d: %v", len(minors), minors)
	}
	if minors[0] != "4.14" {
		t.Errorf("expected first version 4.14, got %s", minors[0])
	}
}

func TestParseRHCOSBuilds(t *testing.T) {
	builds := parseRHCOSBuilds([]byte(testRHCOSBuildsHTML))

	if len(builds) != 3 {
		t.Fatalf("expected 3 builds (excluding 'latest'), got %d: %v", len(builds), builds)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run TestParseOCP -v`
Expected: FAIL

**Step 3: Implement parsers**

Create `internal/mirror/ocp.go`:

```go
package mirror

import (
	"regexp"
	"sort"
	"strings"
)

var (
	// Matches version directories like "4.17.48/"
	versionRegex = regexp.MustCompile(`^(\d+\.\d+\.\d+)/?$`)
	// Matches channel directories like "stable-4.17/"
	channelRegex = regexp.MustCompile(`^(stable|fast|candidate|latest)-(\d+\.\d+)/?$`)
	// Matches RHCOS minor version directories like "4.17/"
	rhcosMinorRegex = regexp.MustCompile(`^(\d+\.\d+)/?$`)
	// Matches href attributes in HTML anchor tags
	hrefRegex = regexp.MustCompile(`href="([^"]+)"`)
)

// Default upstream URLs
const (
	DefaultOCPBaseURL   = "https://mirror.openshift.com/pub/openshift-v4/clients/ocp"
	DefaultRHCOSBaseURL = "https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos"
)

// extractHrefs pulls href values from HTML anchor tags.
func extractHrefs(data []byte) []string {
	matches := hrefRegex.FindAllSubmatch(data, -1)
	hrefs := make([]string, 0, len(matches))
	for _, m := range matches {
		href := string(m[1])
		// Skip parent directory and non-directory links
		if href == "../" || !strings.HasSuffix(href, "/") {
			continue
		}
		hrefs = append(hrefs, strings.TrimSuffix(href, "/"))
	}
	return hrefs
}

// parseOCPDirectoryListing parses an HTML directory listing from
// mirror.openshift.com and categorizes versions into channels and releases.
func parseOCPDirectoryListing(data []byte) []OCPVersion {
	hrefs := extractHrefs(data)
	var versions []OCPVersion

	for _, href := range hrefs {
		// Check for channel pattern: stable-4.17, fast-4.17, candidate-4.18
		if m := channelRegex.FindStringSubmatch(href); m != nil {
			channel := m[1]
			if channel == "latest" {
				continue // skip "latest-X.Y", redundant with stable
			}
			versions = append(versions, OCPVersion{
				Version: href,
				Channel: channel,
			})
			continue
		}

		// Check for specific version: 4.17.48
		if m := versionRegex.FindStringSubmatch(href); m != nil {
			ver := m[1]
			// Skip RC and EC builds
			if strings.Contains(href, "-rc.") || strings.Contains(href, "-ec.") {
				continue
			}
			versions = append(versions, OCPVersion{
				Version: ver,
				Channel: "release",
			})
		}
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version > versions[j].Version
	})

	return versions
}

// parseRHCOSMinorVersions extracts minor version numbers (e.g., "4.17")
// from the top-level RHCOS directory listing.
func parseRHCOSMinorVersions(data []byte) []string {
	hrefs := extractHrefs(data)
	var minors []string

	for _, href := range hrefs {
		if m := rhcosMinorRegex.FindStringSubmatch(href); m != nil {
			minors = append(minors, m[1])
		}
	}

	sort.Strings(minors)
	return minors
}

// parseRHCOSBuilds extracts build version numbers from a RHCOS
// minor version directory listing. Skips "latest" and other non-version entries.
func parseRHCOSBuilds(data []byte) []string {
	hrefs := extractHrefs(data)
	var builds []string

	for _, href := range hrefs {
		if versionRegex.MatchString(href) {
			builds = append(builds, strings.TrimSuffix(href, "/"))
		}
	}

	sort.Strings(builds)
	return builds
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/mirror/ -v`
Expected: PASS (all tests)

**Step 5: Commit**

```bash
git add internal/mirror/ocp.go internal/mirror/ocp_test.go
git commit -m "feat: add OCP/RHCOS HTML directory listing parsers"
```

---

### Task 3: MirrorDiscovery service with caching

**Files:**
- Create: `internal/mirror/discovery.go`
- Create: `internal/mirror/discovery_test.go`

**Step 1: Write the discovery service test**

Create `internal/mirror/discovery_test.go`:

```go
package mirror

import (
	"context"
	"log/slog"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscoveryEPELMirrors(t *testing.T) {
	// Serve test metalink XML
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testMetalinkXML))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDiscovery(logger)
	d.metalinkBaseURL = srv.URL + "/?repo=%s&arch=%s"

	mirrors, err := d.EPELMirrors(context.Background(), 9, "x86_64")
	if err != nil {
		t.Fatalf("EPELMirrors failed: %v", err)
	}
	if len(mirrors) != 3 {
		t.Fatalf("expected 3 mirrors, got %d", len(mirrors))
	}
}

func TestDiscoveryCaching(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte(testMetalinkXML))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDiscovery(logger)
	d.metalinkBaseURL = srv.URL + "/?repo=%s&arch=%s"

	ctx := context.Background()
	d.EPELMirrors(ctx, 9, "x86_64")
	d.EPELMirrors(ctx, 9, "x86_64")

	if callCount != 1 {
		t.Errorf("expected 1 upstream call (cached), got %d", callCount)
	}
}

func TestDiscoveryCacheExpiry(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Write([]byte(testMetalinkXML))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDiscovery(logger)
	d.metalinkBaseURL = srv.URL + "/?repo=%s&arch=%s"
	d.cacheTTL = 1 * time.Millisecond

	ctx := context.Background()
	d.EPELMirrors(ctx, 9, "x86_64")
	time.Sleep(5 * time.Millisecond)
	d.EPELMirrors(ctx, 9, "x86_64")

	if callCount != 2 {
		t.Errorf("expected 2 upstream calls (cache expired), got %d", callCount)
	}
}

func TestDiscoveryOCPVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testOCPDirHTML))
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDiscovery(logger)
	d.ocpBaseURL = srv.URL

	versions, err := d.OCPVersions(context.Background())
	if err != nil {
		t.Fatalf("OCPVersions failed: %v", err)
	}
	if len(versions) == 0 {
		t.Fatal("expected some versions")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run TestDiscovery -v`
Expected: FAIL

**Step 3: Implement discovery service**

Create `internal/mirror/discovery.go`:

```go
package mirror

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	defaultMetalinkBaseURL = "https://mirrors.fedoraproject.org/metalink?repo=epel-%d&arch=%s"
	defaultCacheTTL        = 1 * time.Hour
)

type cacheEntry struct {
	data      interface{}
	fetchedAt time.Time
}

// Discovery provides mirror and version discovery for upstream sources.
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

// NewDiscovery creates a new Discovery service.
func NewDiscovery(logger *slog.Logger) *Discovery {
	if logger == nil {
		logger = slog.Default()
	}
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

// EPELVersions returns the list of known EPEL versions and architectures.
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

// EPELMirrors fetches and returns EPEL mirrors for the given version and architecture.
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

// OCPVersions fetches and returns available OCP versions from mirror.openshift.com.
func (d *Discovery) OCPVersions(ctx context.Context) ([]OCPVersion, error) {
	key := "ocp:versions"

	if cached, ok := d.getCache(key); ok {
		return cached.([]OCPVersion), nil
	}

	data, err := d.fetch(ctx, d.ocpBaseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("fetching OCP directory listing: %w", err)
	}

	versions := parseOCPDirectoryListing(data)
	d.setCache(key, versions)
	return versions, nil
}

// RHCOSVersions fetches and returns available RHCOS versions.
func (d *Discovery) RHCOSVersions(ctx context.Context) ([]RHCOSVersion, error) {
	key := "rhcos:versions"

	if cached, ok := d.getCache(key); ok {
		return cached.([]RHCOSVersion), nil
	}

	data, err := d.fetch(ctx, d.rhcosBaseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("fetching RHCOS directory listing: %w", err)
	}

	minors := parseRHCOSMinorVersions(data)

	var versions []RHCOSVersion
	for _, minor := range minors {
		buildData, err := d.fetch(ctx, fmt.Sprintf("%s/%s/", d.rhcosBaseURL, minor))
		if err != nil {
			d.logger.Warn("failed to fetch RHCOS builds", "minor", minor, "error", err)
			versions = append(versions, RHCOSVersion{Minor: minor})
			continue
		}
		builds := parseRHCOSBuilds(buildData)
		versions = append(versions, RHCOSVersion{Minor: minor, Builds: builds})
	}

	d.setCache(key, versions)
	return versions, nil
}

// fetch performs an HTTP GET and returns the response body.
func (d *Discovery) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "airgap/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

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

func (d *Discovery) setCache(key string, data interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cache[key] = cacheEntry{data: data, fetchedAt: time.Now()}
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/mirror/ -v`
Expected: PASS (all tests)

**Step 5: Commit**

```bash
git add internal/mirror/discovery.go internal/mirror/discovery_test.go
git commit -m "feat: add MirrorDiscovery service with caching"
```

---

### Task 4: Speed test

**Files:**
- Modify: `internal/mirror/discovery.go`
- Create: `internal/mirror/speedtest.go`
- Create: `internal/mirror/speedtest_test.go`

**Step 1: Write speed test tests**

Create `internal/mirror/speedtest_test.go`:

```go
package mirror

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSpeedTest(t *testing.T) {
	// Create a fast server and a slow server
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fast response data here"))
	}))
	defer fast.Close()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("slow"))
	}))
	defer slow.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDiscovery(logger)

	urls := []string{slow.URL, fast.URL}
	results := d.SpeedTest(context.Background(), urls, 10)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Results should be sorted by throughput (fastest first)
	if results[0].URL != fast.URL {
		t.Errorf("expected fastest mirror first, got %s", results[0].URL)
	}

	// Both should have latency > 0
	for _, r := range results {
		if r.LatencyMs <= 0 {
			t.Errorf("expected positive latency for %s, got %d", r.URL, r.LatencyMs)
		}
	}
}

func TestSpeedTestWithErrors(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer good.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDiscovery(logger)

	urls := []string{good.URL, "http://192.0.2.1:1"} // 192.0.2.1 is TEST-NET, should fail
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	results := d.SpeedTest(ctx, urls, 10)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// The unreachable mirror should have an error
	var hasError bool
	for _, r := range results {
		if r.Error != "" {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected at least one result with error")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run TestSpeedTest -v`
Expected: FAIL

**Step 3: Implement speed test**

Create `internal/mirror/speedtest.go`:

```go
package mirror

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	speedTestTimeout    = 5 * time.Second
	speedTestMaxWorkers = 10
)

// SpeedTest runs latency and throughput tests against the given mirror URLs.
// It first measures HTTP HEAD latency, then downloads a small file from
// the top candidates. Results are sorted by throughput (highest first).
// Mirrors that error get sorted to the bottom with their error recorded.
func (d *Discovery) SpeedTest(ctx context.Context, urls []string, topN int) []SpeedResult {
	if topN <= 0 || topN > len(urls) {
		topN = len(urls)
	}

	// Phase 1: Measure latency concurrently
	results := d.measureLatency(ctx, urls)

	// Sort by latency (fastest first), errors last
	sort.Slice(results, func(i, j int) bool {
		if results[i].Error != "" && results[j].Error == "" {
			return false
		}
		if results[i].Error == "" && results[j].Error != "" {
			return true
		}
		return results[i].LatencyMs < results[j].LatencyMs
	})

	// Phase 2: Download test on top N by latency
	candidates := results
	if topN < len(candidates) {
		candidates = candidates[:topN]
	}

	d.measureThroughput(ctx, candidates)

	// Re-sort by throughput (highest first), errors last
	sort.Slice(results, func(i, j int) bool {
		if results[i].Error != "" && results[j].Error == "" {
			return false
		}
		if results[i].Error == "" && results[j].Error != "" {
			return true
		}
		return results[i].ThroughputKBps > results[j].ThroughputKBps
	})

	return results
}

func (d *Discovery) measureLatency(ctx context.Context, urls []string) []SpeedResult {
	results := make([]SpeedResult, len(urls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, speedTestMaxWorkers)

	for i, url := range urls {
		wg.Add(1)
		go func(idx int, mirrorURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := SpeedResult{URL: mirrorURL}

			reqCtx, cancel := context.WithTimeout(ctx, speedTestTimeout)
			defer cancel()

			req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, mirrorURL, nil)
			if err != nil {
				result.Error = err.Error()
				results[idx] = result
				return
			}
			req.Header.Set("User-Agent", "airgap/1.0")

			start := time.Now()
			resp, err := d.client.Do(req)
			latency := time.Since(start)

			if err != nil {
				result.Error = fmt.Sprintf("latency test failed: %v", err)
				results[idx] = result
				return
			}
			resp.Body.Close()

			result.LatencyMs = int(latency.Milliseconds())
			results[idx] = result
		}(i, url)
	}

	wg.Wait()
	return results
}

func (d *Discovery) measureThroughput(ctx context.Context, results []SpeedResult) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, speedTestMaxWorkers)

	for i := range results {
		if results[i].Error != "" {
			continue
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mirrorURL := results[idx].URL

			reqCtx, cancel := context.WithTimeout(ctx, speedTestTimeout)
			defer cancel()

			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, mirrorURL, nil)
			if err != nil {
				results[idx].Error = err.Error()
				return
			}
			req.Header.Set("User-Agent", "airgap/1.0")

			start := time.Now()
			resp, err := d.client.Do(req)
			if err != nil {
				results[idx].Error = fmt.Sprintf("download test failed: %v", err)
				return
			}
			defer resp.Body.Close()

			n, _ := io.Copy(io.Discard, resp.Body)
			elapsed := time.Since(start)

			if elapsed > 0 && n > 0 {
				results[idx].ThroughputKBps = float64(n) / elapsed.Seconds() / 1024.0
			}
		}(i)
	}

	wg.Wait()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/mirror/ -run TestSpeedTest -v -timeout 30s`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/mirror/speedtest.go internal/mirror/speedtest_test.go
git commit -m "feat: add mirror speed test with latency and throughput measurement"
```

---

### Task 5: Wire Discovery into Server and add API routes

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/mirror_handlers.go`
- Modify: `cmd/airgap/serve.go` or `cmd/airgap/root.go`

**Step 1: Add Discovery to Server struct**

In `internal/server/server.go`, add `discovery` field to Server struct and update NewServer:

```go
// Add import:
"github.com/BadgerOps/airgap/internal/mirror"

// Add field to Server struct (after store):
discovery  *mirror.Discovery

// In NewServer, after setting logger:
discovery := mirror.NewDiscovery(logger)

// In return statement, add:
discovery: discovery,
```

**Step 2: Add routes to setupRoutes**

In `internal/server/server.go` `setupRoutes()`, add after the transfer routes block:

```go
// Mirror discovery routes
mux.HandleFunc("GET /api/mirrors/epel/versions", s.handleEPELVersions)
mux.HandleFunc("GET /api/mirrors/epel", s.handleEPELMirrors)
mux.HandleFunc("GET /api/mirrors/ocp/versions", s.handleOCPVersions)
mux.HandleFunc("POST /api/mirrors/speedtest", s.handleSpeedTest)
```

**Step 3: Create mirror handlers**

Create `internal/server/mirror_handlers.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// handleEPELVersions returns known EPEL versions and architectures.
func (s *Server) handleEPELVersions(w http.ResponseWriter, r *http.Request) {
	versions := s.discovery.EPELVersions()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}

// handleEPELMirrors returns EPEL mirrors for the given version and architecture.
func (s *Server) handleEPELMirrors(w http.ResponseWriter, r *http.Request) {
	versionStr := r.URL.Query().Get("version")
	arch := r.URL.Query().Get("arch")

	if versionStr == "" || arch == "" {
		jsonError(w, http.StatusBadRequest, "version and arch query parameters required")
		return
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "version must be an integer")
		return
	}

	mirrors, err := s.discovery.EPELMirrors(r.Context(), version, arch)
	if err != nil {
		s.logger.Error("failed to discover EPEL mirrors", "version", version, "arch", arch, "error", err)
		jsonError(w, http.StatusBadGateway, "failed to fetch mirrors: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mirrors)
}

// handleOCPVersions returns available OCP and RHCOS versions.
func (s *Server) handleOCPVersions(w http.ResponseWriter, r *http.Request) {
	type response struct {
		OCP   interface{} `json:"ocp"`
		RHCOS interface{} `json:"rhcos"`
	}

	ocpVersions, ocpErr := s.discovery.OCPVersions(r.Context())
	rhcosVersions, rhcosErr := s.discovery.RHCOSVersions(r.Context())

	if ocpErr != nil && rhcosErr != nil {
		s.logger.Error("failed to discover versions", "ocp_error", ocpErr, "rhcos_error", rhcosErr)
		jsonError(w, http.StatusBadGateway, "failed to fetch versions")
		return
	}

	if ocpErr != nil {
		s.logger.Warn("failed to discover OCP versions", "error", ocpErr)
	}
	if rhcosErr != nil {
		s.logger.Warn("failed to discover RHCOS versions", "error", rhcosErr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response{
		OCP:   ocpVersions,
		RHCOS: rhcosVersions,
	})
}

// speedTestRequest is the request body for POST /api/mirrors/speedtest.
type speedTestRequest struct {
	URLs []string `json:"urls"`
	TopN int      `json:"top_n"`
}

// handleSpeedTest runs speed tests against the given mirror URLs.
func (s *Server) handleSpeedTest(w http.ResponseWriter, r *http.Request) {
	var req speedTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.URLs) == 0 {
		jsonError(w, http.StatusBadRequest, "urls list required")
		return
	}

	if req.TopN <= 0 {
		req.TopN = 10
	}

	results := s.discovery.SpeedTest(r.Context(), req.URLs, req.TopN)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
```

**Step 4: Run build to verify compilation**

Run: `go build ./...`
Expected: Success

**Step 5: Commit**

```bash
git add internal/server/server.go internal/server/mirror_handlers.go
git commit -m "feat: add mirror discovery API endpoints"
```

---

### Task 6: Mirror handler tests

**Files:**
- Create: `internal/server/mirror_handlers_test.go`

**Step 1: Write handler tests**

Create `internal/server/mirror_handlers_test.go`:

```go
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BadgerOps/airgap/internal/mirror"
)

func TestHandleEPELVersions(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/mirrors/epel/versions", nil)
	w := httptest.NewRecorder()
	srv.handleEPELVersions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var versions []mirror.EPELVersionInfo
	json.NewDecoder(w.Body).Decode(&versions)
	if len(versions) == 0 {
		t.Error("expected at least one EPEL version")
	}
}

func TestHandleEPELMirrorsMissingParams(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/mirrors/epel", nil)
	w := httptest.NewRecorder()
	srv.handleEPELMirrors(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleEPELMirrorsInvalidVersion(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/mirrors/epel?version=abc&arch=x86_64", nil)
	w := httptest.NewRecorder()
	srv.handleEPELMirrors(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSpeedTestMissingURLs(t *testing.T) {
	srv := setupTestServer(t)

	body := `{"urls":[]}`
	req := httptest.NewRequest("POST", "/api/mirrors/speedtest", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleSpeedTest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSpeedTestValidRequest(t *testing.T) {
	srv := setupTestServer(t)

	// Create a test server to speed-test against
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test data"))
	}))
	defer ts.Close()

	body, _ := json.Marshal(speedTestRequest{URLs: []string{ts.URL}, TopN: 5})
	req := httptest.NewRequest("POST", "/api/mirrors/speedtest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleSpeedTest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []mirror.SpeedResult
	json.NewDecoder(w.Body).Decode(&results)
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}
```

**Step 2: Run tests**

Run: `go test ./internal/server/ -run TestHandle -v`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/server/mirror_handlers_test.go
git commit -m "test: add mirror handler tests"
```

---

### Task 7: Enhance providers.html with EPEL mirror discovery UI

**Files:**
- Modify: `internal/server/templates/providers.html`

**Step 1: Update the EPEL section**

Replace the `<template x-if="newProvider.type === 'epel'">` block (lines 184-202 in providers.html) with the enhanced version that includes version/arch dropdowns, mirror discovery, and speed test:

```html
<template x-if="newProvider.type === 'epel'">
	<div>
		<h3 style="margin-bottom: 12px;">EPEL Configuration</h3>

		<!-- Version and Architecture Selection -->
		<div class="form-row">
			<div class="form-group">
				<label>EPEL Version</label>
				<select x-model="epelVersion" @change="epelMirrors = []; selectedMirror = null">
					<option value="">Select version...</option>
					<template x-for="v in epelVersions" :key="v.version">
						<option :value="v.version" x-text="'EPEL ' + v.version"></option>
					</template>
				</select>
			</div>
			<div class="form-group">
				<label>Architecture</label>
				<select x-model="epelArch" @change="epelMirrors = []; selectedMirror = null">
					<option value="x86_64">x86_64</option>
					<option value="aarch64">aarch64</option>
					<option value="ppc64le">ppc64le</option>
					<option value="s390x">s390x</option>
				</select>
			</div>
		</div>

		<div style="margin-bottom: 16px;">
			<button type="button" class="button" @click="discoverEPELMirrors()" :disabled="!epelVersion || discoveringMirrors">
				<span x-show="!discoveringMirrors">Discover Mirrors</span>
				<span x-show="discoveringMirrors">Discovering...</span>
			</button>
			<button type="button" class="button secondary" x-show="epelMirrors.length > 0" @click="testMirrorSpeed()" :disabled="testingSpeed" style="margin-left: 8px;">
				<span x-show="!testingSpeed">Test Speed</span>
				<span x-show="testingSpeed">Testing...</span>
			</button>
		</div>

		<!-- Mirror List -->
		<div x-show="epelMirrors.length > 0" style="margin-bottom: 16px;">
			<label>Select a Mirror</label>
			<div style="max-height: 300px; overflow-y: auto; border: 1px solid #ddd; border-radius: 4px;">
				<table style="margin-bottom: 0; box-shadow: none;">
					<thead>
						<tr>
							<th style="width: 30px;"></th>
							<th>URL</th>
							<th style="width: 60px;">Country</th>
							<th style="width: 50px;">Pref</th>
							<th x-show="speedResults.length > 0" style="width: 80px;">Latency</th>
							<th x-show="speedResults.length > 0" style="width: 100px;">Speed</th>
						</tr>
					</thead>
					<tbody>
						<template x-for="(m, idx) in epelMirrors" :key="m.url">
							<tr @click="selectMirror(m)" style="cursor: pointer;" :style="selectedMirror === m.url ? 'background-color: #ebf5fb' : ''">
								<td><input type="radio" :checked="selectedMirror === m.url" name="mirror_select"></td>
								<td style="font-size: 12px; word-break: break-all;" x-text="m.url"></td>
								<td x-text="m.country"></td>
								<td x-text="m.preference"></td>
								<td x-show="speedResults.length > 0" x-text="getSpeedResult(m.url, 'latency')"></td>
								<td x-show="speedResults.length > 0" x-text="getSpeedResult(m.url, 'throughput')"></td>
							</tr>
						</template>
					</tbody>
				</table>
			</div>
		</div>

		<!-- Repositories (auto-filled from selection or manual) -->
		<div class="form-group">
			<label>Repositories</label>
			<div class="list-items">
				<template x-for="(repo, idx) in newProvider.config.repos" :key="idx">
					<div class="list-item">
						<input type="text" x-model="repo.name" placeholder="Repo name (e.g., epel-9)">
						<input type="text" x-model="repo.base_url" placeholder="Base URL">
						<input type="text" x-model="repo.output_dir" placeholder="Output dir">
						<button type="button" class="button danger small" @click="newProvider.config.repos.splice(idx, 1)">X</button>
					</div>
				</template>
				<button type="button" class="button small secondary" @click="newProvider.config.repos.push({name:'',base_url:'',output_dir:''})">+ Add Repo</button>
			</div>
		</div>
	</div>
</template>
```

**Step 2: Update the OCP/RHCOS section**

Replace the `<template x-if="newProvider.type === 'ocp_binaries' || newProvider.type === 'rhcos'">` block (lines 204-220) with:

```html
<template x-if="newProvider.type === 'ocp_binaries' || newProvider.type === 'rhcos'">
	<div>
		<h3 style="margin-bottom: 12px;" x-text="newProvider.type === 'ocp_binaries' ? 'OCP Binaries Configuration' : 'RHCOS Configuration'"></h3>
		<div class="form-group">
			<label>Base URL</label>
			<input type="text" x-model="newProvider.config.base_url" :placeholder="newProvider.type === 'ocp_binaries' ? 'https://mirror.openshift.com/pub/openshift-v4/clients/ocp' : 'https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos'">
		</div>
		<div class="form-group">
			<label>Output Directory</label>
			<input type="text" x-model="newProvider.config.output_dir" :placeholder="newProvider.type === 'ocp_binaries' ? 'ocp-binaries' : 'rhcos-images'">
		</div>

		<div style="margin-bottom: 16px;">
			<button type="button" class="button" @click="loadOCPVersions()" :disabled="loadingVersions">
				<span x-show="!loadingVersions">Load Available Versions</span>
				<span x-show="loadingVersions">Loading...</span>
			</button>
		</div>

		<!-- Version Selection -->
		<div x-show="ocpVersionsLoaded">
			<template x-if="newProvider.type === 'ocp_binaries'">
				<div>
					<!-- Channels -->
					<div class="form-group" x-show="ocpChannels.length > 0">
						<label>Channels (recommended)</label>
						<div class="checkbox-group" style="flex-wrap: wrap;">
							<template x-for="ch in ocpChannels" :key="ch.version">
								<label style="min-width: 180px;">
									<input type="checkbox" :value="ch.version" x-model="selectedVersions"> <span x-text="ch.version"></span>
								</label>
							</template>
						</div>
					</div>
					<!-- Specific Releases -->
					<div class="form-group" x-show="ocpReleases.length > 0">
						<label>Specific Releases <small style="color: #999;">(showing latest 20)</small></label>
						<div class="checkbox-group" style="flex-wrap: wrap;">
							<template x-for="rel in ocpReleases.slice(0, 20)" :key="rel.version">
								<label style="min-width: 120px;">
									<input type="checkbox" :value="rel.version" x-model="selectedVersions"> <span x-text="rel.version"></span>
								</label>
							</template>
						</div>
					</div>
				</div>
			</template>

			<template x-if="newProvider.type === 'rhcos'">
				<div class="form-group" x-show="rhcosVersions.length > 0">
					<label>RHCOS Versions</label>
					<div class="checkbox-group" style="flex-wrap: wrap;">
						<template x-for="rv in rhcosVersions" :key="rv.minor">
							<label style="min-width: 120px;">
								<input type="checkbox" :value="rv.minor" x-model="selectedVersions"> <span x-text="'RHCOS ' + rv.minor"></span>
								<small x-show="rv.builds && rv.builds.length > 0" style="color: #999;" x-text="'(' + rv.builds.length + ' builds)'"></small>
							</label>
						</template>
					</div>
				</div>
			</template>
		</div>

		<!-- Fallback: manual versions input -->
		<div class="form-group" x-show="!ocpVersionsLoaded">
			<label>Versions (comma-separated)</label>
			<input type="text" x-model="newProvider.config.versions_str" placeholder="4.14.10, 4.15.2, stable-4.15">
		</div>
	</div>
</template>
```

**Step 3: Update the Alpine.js `providerManager()` function**

Replace the `return { ... }` block in the `<script>` section. Add new state variables and methods. The full updated script section:

```html
<script>
function providerManager() {
	const statuses = {};
	{{range $name, $status := .Statuses}}
	statuses["{{$name}}"] = {
		fileCount: {{$status.FileCount}},
		totalSize: {{$status.TotalSize}}
	};
	{{end}}

	return {
		configs: [],
		statuses: statuses,
		showAddForm: false,
		message: '',
		messageType: 'success',
		newProvider: {
			name: '',
			type: '',
			enabled: true,
			config: {
				repos: [{name: '', base_url: '', output_dir: ''}],
				base_url: '',
				output_dir: '',
				versions_str: ''
			}
		},

		// EPEL mirror discovery state
		epelVersions: [],
		epelVersion: '',
		epelArch: 'x86_64',
		epelMirrors: [],
		selectedMirror: null,
		discoveringMirrors: false,
		testingSpeed: false,
		speedResults: [],

		// OCP/RHCOS version discovery state
		ocpChannels: [],
		ocpReleases: [],
		rhcosVersions: [],
		ocpVersionsLoaded: false,
		loadingVersions: false,
		selectedVersions: [],

		async init() {
			await this.loadConfigs();
			// Pre-fetch EPEL versions
			try {
				const resp = await fetch('/api/mirrors/epel/versions');
				if (resp.ok) this.epelVersions = await resp.json();
			} catch (e) {
				console.error('Failed to load EPEL versions:', e);
			}
		},

		async loadConfigs() {
			try {
				const resp = await fetch('/api/providers/config');
				this.configs = await resp.json();
			} catch (e) {
				console.error('Failed to load configs:', e);
			}
		},

		getFileCount(name) {
			return this.statuses[name] ? this.statuses[name].fileCount : 0;
		},

		getFileSize(name) {
			const size = this.statuses[name] ? this.statuses[name].totalSize : 0;
			return formatBytes(size);
		},

		async discoverEPELMirrors() {
			this.discoveringMirrors = true;
			this.epelMirrors = [];
			this.speedResults = [];
			this.selectedMirror = null;
			try {
				const resp = await fetch(`/api/mirrors/epel?version=${this.epelVersion}&arch=${this.epelArch}`);
				if (resp.ok) {
					this.epelMirrors = await resp.json();
				} else {
					const err = await resp.json();
					this.message = err.error || 'Failed to discover mirrors';
					this.messageType = 'error';
				}
			} catch (e) {
				this.message = 'Network error: ' + e.message;
				this.messageType = 'error';
			}
			this.discoveringMirrors = false;
		},

		async testMirrorSpeed() {
			this.testingSpeed = true;
			this.speedResults = [];
			const urls = this.epelMirrors.map(m => m.url);
			try {
				const resp = await fetch('/api/mirrors/speedtest', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({urls: urls, top_n: 10})
				});
				if (resp.ok) {
					this.speedResults = await resp.json();
					// Re-sort mirrors by speed results
					const speedMap = {};
					this.speedResults.forEach(r => { speedMap[r.url] = r; });
					this.epelMirrors.sort((a, b) => {
						const sa = speedMap[a.url], sb = speedMap[b.url];
						if (!sa || sa.error) return 1;
						if (!sb || sb.error) return -1;
						return sb.throughput_kbps - sa.throughput_kbps;
					});
				}
			} catch (e) {
				console.error('Speed test failed:', e);
			}
			this.testingSpeed = false;
		},

		getSpeedResult(url, field) {
			const r = this.speedResults.find(s => s.url === url);
			if (!r) return '-';
			if (r.error) return 'err';
			if (field === 'latency') return r.latency_ms + 'ms';
			if (field === 'throughput') return r.throughput_kbps > 0 ? r.throughput_kbps.toFixed(1) + ' KB/s' : '-';
			return '-';
		},

		selectMirror(m) {
			this.selectedMirror = m.url;
			// Auto-fill the first repo entry
			const repoName = 'epel-' + this.epelVersion;
			if (this.newProvider.config.repos.length === 0) {
				this.newProvider.config.repos.push({name: '', base_url: '', output_dir: ''});
			}
			this.newProvider.config.repos[0].name = repoName;
			this.newProvider.config.repos[0].base_url = m.url;
			this.newProvider.config.repos[0].output_dir = repoName;
			if (!this.newProvider.name) {
				this.newProvider.name = repoName;
			}
		},

		async loadOCPVersions() {
			this.loadingVersions = true;
			this.ocpChannels = [];
			this.ocpReleases = [];
			this.rhcosVersions = [];
			this.selectedVersions = [];
			try {
				const resp = await fetch('/api/mirrors/ocp/versions');
				if (resp.ok) {
					const data = await resp.json();
					if (data.ocp) {
						this.ocpChannels = data.ocp.filter(v => v.channel !== 'release');
						this.ocpReleases = data.ocp.filter(v => v.channel === 'release');
					}
					if (data.rhcos) {
						this.rhcosVersions = data.rhcos;
					}
					this.ocpVersionsLoaded = true;

					// Pre-fill base URL if empty
					if (!this.newProvider.config.base_url) {
						if (this.newProvider.type === 'ocp_binaries') {
							this.newProvider.config.base_url = 'https://mirror.openshift.com/pub/openshift-v4/clients/ocp';
						} else if (this.newProvider.type === 'rhcos') {
							this.newProvider.config.base_url = 'https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos';
						}
					}
				} else {
					const err = await resp.json();
					this.message = err.error || 'Failed to load versions';
					this.messageType = 'error';
				}
			} catch (e) {
				this.message = 'Network error: ' + e.message;
				this.messageType = 'error';
			}
			this.loadingVersions = false;
		},

		async createProvider() {
			this.message = '';
			const cfg = Object.assign({}, this.newProvider.config);

			// Handle versions: prefer selectedVersions from discovery over manual input
			if (this.selectedVersions.length > 0) {
				cfg.versions = this.selectedVersions;
			} else if (cfg.versions_str) {
				cfg.versions = cfg.versions_str.split(',').map(v => v.trim()).filter(v => v);
			}
			delete cfg.versions_str;

			// Clean up empty repos
			if (cfg.repos) {
				cfg.repos = cfg.repos.filter(r => r.name || r.base_url);
			}

			const body = {
				name: this.newProvider.name,
				type: this.newProvider.type,
				enabled: this.newProvider.enabled,
				config: cfg
			};

			try {
				const resp = await fetch('/api/providers/config', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify(body)
				});

				if (resp.ok) {
					this.message = 'Provider created successfully';
					this.messageType = 'success';
					this.showAddForm = false;
					this.resetForm();
					await this.loadConfigs();
				} else {
					const err = await resp.json();
					this.message = err.error || 'Failed to create provider';
					this.messageType = 'error';
				}
			} catch (e) {
				this.message = 'Network error: ' + e.message;
				this.messageType = 'error';
			}
		},

		async toggleProvider(name) {
			try {
				const resp = await fetch('/api/providers/config/' + name + '/toggle', {method: 'POST'});
				if (resp.ok) {
					await this.loadConfigs();
				}
			} catch (e) {
				console.error('Toggle failed:', e);
			}
		},

		async deleteProvider(name) {
			if (!confirm('Delete provider "' + name + '"? This cannot be undone.')) return;
			try {
				const resp = await fetch('/api/providers/config/' + name, {method: 'DELETE'});
				if (resp.ok) {
					await this.loadConfigs();
				}
			} catch (e) {
				console.error('Delete failed:', e);
			}
		},

		resetForm() {
			this.newProvider = {
				name: '',
				type: '',
				enabled: true,
				config: {
					repos: [{name: '', base_url: '', output_dir: ''}],
					base_url: '',
					output_dir: '',
					versions_str: ''
				}
			};
			this.epelMirrors = [];
			this.selectedMirror = null;
			this.speedResults = [];
			this.ocpVersionsLoaded = false;
			this.selectedVersions = [];
		}
	};
}

function formatBytes(bytes) {
	if (bytes === 0) return '0 B';
	const k = 1024;
	const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
	const i = Math.floor(Math.log(bytes) / Math.log(k));
	return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}
</script>
```

**Step 4: Update x-init**

In the providers.html, change the `x-init` from `loadConfigs()` to `init()`:

```html
<div x-data="providerManager()" x-init="init()">
```

**Step 5: Run build to verify**

Run: `go build ./...`
Expected: Success

**Step 6: Commit**

```bash
git add internal/server/templates/providers.html
git commit -m "feat: enhance providers UI with mirror discovery and version selection"
```

---

### Task 8: Final verification

**Step 1: Run all tests**

Run: `go test ./... -timeout 120s`
Expected: All PASS

**Step 2: Run race detector**

Run: `go test -race ./internal/mirror/ ./internal/server/ -timeout 120s`
Expected: No races

**Step 3: Build and verify**

Run: `go build -o bin/airgap ./cmd/airgap/`
Expected: Success

**Step 4: Manual smoke test**

Run: `./bin/airgap serve --dev --data-dir ./airgap-data/`

Verify in browser:
- Navigate to /providers
- Click "Add Provider"
- Select type "EPEL Repository"
- Version and architecture dropdowns appear
- Click "Discover Mirrors" - mirror list appears
- Click "Test Speed" - latency and throughput columns appear
- Click a mirror row - base_url auto-fills
- Select type "OCP Binaries"
- Click "Load Available Versions" - channels and releases appear
- Check some versions, they populate the config

**Step 5: Commit any fixes from smoke test**

---

## Key Files Reference

| File | Role |
|------|------|
| `internal/mirror/types.go` | New: shared types (MirrorInfo, SpeedResult, OCPVersion, etc.) |
| `internal/mirror/epel.go` | New: metalink XML parser, EPEL version constants |
| `internal/mirror/ocp.go` | New: HTML directory listing parser for OCP/RHCOS |
| `internal/mirror/discovery.go` | New: Discovery service with caching and HTTP fetching |
| `internal/mirror/speedtest.go` | New: latency + throughput speed test |
| `internal/server/mirror_handlers.go` | New: 4 API endpoint handlers |
| `internal/server/server.go` | Modified: add Discovery field, routes |
| `internal/server/templates/providers.html` | Modified: enhanced EPEL/OCP/RHCOS form sections |
