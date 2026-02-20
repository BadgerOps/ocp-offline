# Migration Path

#airgap #migration

Back to [[00 - Project Index]] | Related: [[01 - Existing Repos Audit]], [[02 - Architecture]]

## Overview

How each existing BadgerOps repo maps into the unified `airgap` codebase. Two repos get ported (code migrated), two get wrapped (external tool invocation), one is conceptually absorbed, and one is deferred.

## ocpsync → `internal/provider/ocp/`

**Migration type:** Direct port (Go → Go, restructured)

**What changes:**
- `main.go` logic splits into `binaries.go` (OCP clients) and `rhcos.go` (RHCOS images)
- Both implement the `Provider` interface: `Plan()`, `Sync()`, `Validate()`
- Config moves from standalone `config.yaml` to the `providers.ocp_binaries` and `providers.rhcos` sections of `airgap.yaml` (see [[03 - Configuration#From ocpsync config.yaml]])
- `downloadFile()` → replaced by shared `internal/download/client.go`
- `validateFile()` → replaced by shared checksum logic in download client
- `generateFileList()` → becomes part of `Plan()`
- `downloadHandler()` → becomes `Sync()`
- Logging migrates from `logrus` to `slog`

**What stays the same:**
- SHA256 checksum manifest parsing logic
- Ignore-list filtering patterns
- Exponential backoff retry (now in shared download client)

**Effort:** Low — the Go code already exists, it's a structural refactor into the provider interface.

**Phase:** [[07 - Implementation Phases#Phase 1 Foundation Weeks 1–2|Phase 1]]

---

## epel-offline-sync → `internal/provider/epel/`

**Migration type:** Language port (Python → Go)

**What changes:**
- `config.ini` parsing → YAML config via `providers.epel` section
- `xml.etree.ElementTree` → Go's `encoding/xml`
- `gzip` decompression of `primary.xml.gz` → Go's `compress/gzip`
- Python's `hashlib.sha256` → Go's `crypto/sha256`
- Python's `ThreadPoolExecutor` → Go goroutine worker pool (`internal/download/pool.go`)
- `os.path` operations → Go's `filepath` package

**What stays the same:**
- Algorithm: fetch repomd.xml → parse for primary.xml location → download primary.xml.gz → decompress → parse package list → diff against local → download new/changed → validate
- Checksum-based dedup logic (compare local hash vs manifest hash)

**What's new:**
- `cleanup_removed_packages` — detect packages in local dir that are no longer in the upstream primary.xml and remove them. This fixes the known limitation noted in the original README.
- Package count is tracked in SQLite for history/reporting

**Key parsing detail to preserve:**

The repomd.xml structure (simplified):
```xml
<repomd>
  <data type="primary">
    <location href="repodata/...-primary.xml.gz"/>
    <checksum type="sha256">abc123</checksum>
  </data>
</repomd>
```

The primary.xml structure (simplified):
```xml
<metadata>
  <package type="rpm">
    <name>ansible-core</name>
    <version ver="2.15.0" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">def456</checksum>
    <location href="Packages/a/ansible-core-2.15.0-1.el9.x86_64.rpm"/>
    <size package="3145728"/>
  </package>
</metadata>
```

Go structs for XML parsing:
```go
type RepoMD struct {
    XMLName xml.Name     `xml:"repomd"`
    Data    []RepoMDData `xml:"data"`
}

type RepoMDData struct {
    Type     string           `xml:"type,attr"`
    Location RepoMDLocation   `xml:"location"`
    Checksum RepoMDChecksum   `xml:"checksum"`
}

type PrimaryMetadata struct {
    XMLName  xml.Name  `xml:"metadata"`
    Packages []Package `xml:"package"`
}

type Package struct {
    Name     string          `xml:"name"`
    Checksum PackageChecksum `xml:"checksum"`
    Location PackageLocation `xml:"location"`
    Size     PackageSize     `xml:"size"`
}
```

**Effort:** Medium — algorithmic port is straightforward, but XML parsing details need care.

**Phase:** [[07 - Implementation Phases#Phase 2 EPEL Provider Validation Weeks 3–4|Phase 2]]

---

## mirror-registry → `internal/provider/containers/registry.go`

**Migration type:** External tool wrapper

**What happens:** The `mirror-registry` binary is invoked via `os/exec`. We don't port any of its Go or Ansible code. The binary path is configured in `airgap.yaml`.

**Wrapper responsibilities:**
- `install` — invoke `mirror-registry install` with appropriate flags, capture output
- `upgrade` — invoke `mirror-registry upgrade`
- `status` — check if Quay, Redis, Postgres are running (via Podman inspection or HTTP health check)
- `uninstall` — invoke `mirror-registry uninstall`
- Map stdout/stderr into structured log entries
- Detect errors in exit codes and output parsing

**Why wrap instead of reimplement:** See [[09 - Technical Decisions#Wrap vs Reimplement External Tools]]. Summary: mirror-registry's Ansible playbooks handle complex Quay deployment logic that would be expensive to rewrite and keep in sync with upstream.

**Effort:** Low — thin exec wrapper with output parsing.

**Phase:** [[07 - Implementation Phases#Phase 4 External Tool Wrappers Weeks 7–8|Phase 4]]

---

## oc-mirror → `internal/provider/containers/ocmirror.go`

**Migration type:** External tool wrapper

**What happens:** Same pattern as mirror-registry. The `oc-mirror` binary is invoked via `os/exec`.

**Wrapper responsibilities:**
- Generate or manage `ImageSetConfiguration` YAML based on airgap config
- Invoke `oc-mirror --config imageset-config.yaml file:///output-dir`
- Parse oc-mirror output for progress reporting
- Map oc-mirror's differential update model into our Plan/Sync interface
- Handle oc-mirror's own archive format and convert/integrate with our export engine

**Complexity note:** oc-mirror has its own concept of mirroring to disk (`file://` backend) which overlaps with our export engine. The wrapper needs to manage this carefully — we use oc-mirror's disk output as the provider's content, then our export engine packages it alongside RPMs and binaries.

**Effort:** Medium — oc-mirror's output format and configuration model add complexity.

**Phase:** [[07 - Implementation Phases#Phase 4 External Tool Wrappers Weeks 7–8|Phase 4]]

---

## dlserver → absorbed into web UI + API

**Migration type:** Conceptual replacement

**What carries forward:**
- The idea of a REST API for job management
- SQLite-backed persistence
- CORS-enabled endpoints for web UI consumption

**What's different:**
- Full web UI instead of bare API
- Rich job model (sync, export, import) instead of generic "download jobs"
- SSE for real-time progress instead of polling

**Effort:** None — dlserver code is not used, but its design intent is realized in the unified app.

---

## rpm-builder → deferred

**What happens:** Out of scope for v1. Could become a provider in a future version if there's a need to build custom RPMs in the disconnected environment.

Potential v2 use case: build custom RPMs on Machine A, include them in the export alongside EPEL packages, and publish them as a separate repo on Machine B.
