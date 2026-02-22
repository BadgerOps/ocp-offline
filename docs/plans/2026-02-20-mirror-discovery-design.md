# Mirror Auto-Discovery and Speed Test Design

## Goal

Make provider configuration easier by auto-discovering upstream mirrors (EPEL) and available versions (OCP/RHCOS), letting users pick from dropdowns, and ranking mirrors by speed.

## Architecture

A new `internal/mirror` package handles all upstream discovery with in-memory caching (1-hour TTL). The server exposes four new API endpoints that the providers UI calls on user action (button clicks, not page load). Speed tests run server-side since the browser may not have internet access in airgap-adjacent setups.

## API Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/mirrors/epel/versions` | GET | Returns available EPEL versions and architectures |
| `/api/mirrors/epel?version=9&arch=x86_64` | GET | Returns EPEL mirrors from Fedora metalink, sorted by preference |
| `/api/mirrors/ocp/versions` | GET | Scrapes mirror.openshift.com for OCP versions (grouped by channel) and RHCOS versions |
| `/api/mirrors/speedtest` | POST | Runs latency + download test against given mirror URLs, returns ranked results |

## `internal/mirror` Package

### MirrorDiscovery struct

Main service. Holds HTTP client, in-memory cache (map + RWMutex), and logger.

### EPEL Discovery

- Fetches `https://mirrors.fedoraproject.org/metalink?repo=epel-{version}&arch={arch}`
- Parses Metalink 3.0 XML to extract: mirror URL, country code, protocol, preference score (1-100)
- Known versions: 7, 8, 9, 10. Known architectures: x86_64, aarch64, ppc64le, s390x.
- Returns `[]MirrorInfo`

### OCP/RHCOS Version Discovery

- Fetches HTML directory listing from `mirror.openshift.com/pub/openshift-v4/clients/ocp/`
- Parses `<a href>` tags, categorizes into: specific versions (4.17.48), channels (stable-4.17, fast-4.17, candidate-4.17)
- RHCOS: same approach at `/dependencies/rhcos/` - extracts minor versions and builds
- Returns structured version lists

### Speed Test

- Phase 1: HTTP HEAD to each mirror URL, measure latency. Run concurrently (10 goroutines max).
- Phase 2: Download a small file (repomd.xml for EPEL, sha256sum.txt for OCP) from top N fastest by latency.
- Returns `[]SpeedResult` sorted by throughput.
- Context-aware, 5-second timeout per mirror.

## Data Structures

```
MirrorInfo:     URL, Country, Protocol, Preference (int)
SpeedResult:    URL, LatencyMs (int), ThroughputKBps (float64), Error (string)
OCPVersion:     Version (string), Channel (string: "stable"/"fast"/"candidate"/"release")
RHCOSVersion:   Minor (string, e.g. "4.17"), Builds ([]string)
```

## Caching

In-memory map with TTL (1 hour). Key is request params (e.g., `epel:9:x86_64`). No persistence - discovery data is ephemeral. Protected by `sync.RWMutex`.

## Error Handling

Discovery failures return partial results with error messages. Speed test failures per-mirror are recorded in `SpeedResult.Error` rather than failing the whole batch. UI shows which mirrors errored.

## UI Changes

### EPEL Provider Form

- Version dropdown: EPEL 7, 8, 9, 10
- Architecture dropdown: x86_64, aarch64, ppc64le, s390x
- "Discover Mirrors" button fetches mirror list
- Mirror table: URL, Country, Preference. Selectable rows.
- "Test Speed" button runs speed test, adds Latency/Throughput columns, re-sorts
- Selecting a mirror auto-fills `base_url`
- Manual URL entry still supported

### OCP Binaries Provider Form

- "Load Versions" button fetches available versions
- Versions grouped by channel (stable, fast, candidate) and specific releases
- Multi-select checkboxes for versions to sync
- Base URL pre-filled with `https://mirror.openshift.com/pub/openshift-v4/clients/ocp`, editable for custom mirrors

### RHCOS Provider Form

- Same pattern as OCP: "Load Versions" button, version selection
- Base URL pre-filled with `https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos`, editable

### UX Principle

All discovery calls are explicit (user clicks button), never automatic on page load.

## What Is NOT Persisted

Mirror lists and speed test results are transient. Only the user's final choice (base_url, selected versions) gets persisted in the existing `provider_configs` table.
