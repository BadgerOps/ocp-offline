# Configuration

#airgap #config

Back to [[00 - Project Index]] | Related: [[02 - Architecture]], [[01 - Existing Repos Audit]]

## Overview

Single YAML file (`airgap.yaml`) replaces the scattered `config.ini` (epel-offline-sync) and `config.yaml` (ocpsync). Loaded by `internal/config/config.go` into typed Go structs.

Config file search order:
1. `--config` CLI flag (explicit path)
2. `./airgap.yaml` (current directory)
3. `/etc/airgap/airgap.yaml` (system-wide)
4. `$HOME/.config/airgap/airgap.yaml` (user-level)

## Full Reference

```yaml
# airgap.yaml — complete configuration reference

# ─── Server Settings ─────────────────────────────────────
server:
  listen: "0.0.0.0:8080"              # web UI + API bind address
  data_dir: "/var/lib/airgap"          # root directory for all downloaded content
  db_path: "/var/lib/airgap/airgap.db" # SQLite database location

# ─── Export/Import Settings ──────────────────────────────
export:
  split_size: "25GB"                   # max size per tar archive part
  compression: "zstd"                  # "zstd" (fast) or "gzip" (compatible)
  output_dir: "/mnt/transfer-disk"     # default export destination
  manifest_name: "airgap-manifest.json"

# ─── Scheduler Settings ─────────────────────────────────
schedule:
  enabled: true
  default_cron: "0 2 * * 0"           # default: weekly Sunday 2am

# ─── Provider Settings ───────────────────────────────────
providers:

  # --- EPEL RPM Repositories ---
  epel:
    enabled: true
    repos:
      - name: "epel-9"
        base_url: "https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64/"
        output_dir: "epel/9"
      - name: "epel-8"
        base_url: "https://dl.fedoraproject.org/pub/epel/8/Everything/x86_64/"
        output_dir: "epel/8"
    max_concurrent_downloads: 8
    retry_attempts: 3
    cleanup_removed_packages: true     # NEW: removes packages deleted upstream

  # --- OCP Client Binaries ---
  ocp_binaries:
    enabled: true
    base_url: "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/"
    versions:
      - "latest-4.17"
      - "latest-4.18"
      - "latest-4.19"
    ignored_patterns:
      - "windows"
      - "mac"
      - "arm64"
      - "aarch64"
      - "ppc64le"
    output_dir: "ocp-clients"
    retry_attempts: 3

  # --- RHCOS Images ---
  rhcos:
    enabled: true
    base_url: "https://mirror.openshift.com/pub/openshift-v4/x86_64/dependencies/rhcos/"
    versions:
      - "4.17/latest"
      - "4.18/latest"
      - "4.19/latest"
    ignored_patterns:
      - "aliyun"
      - "aws"
      - "azure"
      - "gcp"
      - "ibmcloud"
      - "nutanix"
      - "openstack"
      - "vmware"
    output_dir: "rhcos"
    retry_attempts: 3

  # --- Container Images (oc-mirror wrapper) ---
  container_images:
    enabled: true
    oc_mirror_binary: "/usr/local/bin/oc-mirror"
    imageset_config: "/etc/airgap/imageset-config.yaml"
    output_dir: "container-images"

  # --- Registry Management (mirror-registry wrapper) ---
  registry:
    enabled: true
    mirror_registry_binary: "/usr/local/bin/mirror-registry"
    quay_root: "/var/lib/quay"

  # --- Custom File Sources ---
  custom_files:
    enabled: false
    sources: []
    # - name: "helm-charts"
    #   url: "https://example.com/charts/"
    #   checksum_url: "https://example.com/charts/SHA256SUMS"
    #   output_dir: "helm-charts"
```

## Config Migration from Existing Repos

### From epel-offline-sync `config.ini`

```ini
# OLD (config.ini)
[upstream]
base_url = https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64/
```

Maps to:

```yaml
# NEW (airgap.yaml)
providers:
  epel:
    repos:
      - name: "epel-9"
        base_url: "https://dl.fedoraproject.org/pub/epel/9/Everything/x86_64/"
```

### From ocpsync `config.yaml`

```yaml
# OLD (config.yaml)
ocp_binaries:
  base_url: "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/"
  version:
    - "latest-4.17"
  ignored_files:
    - "windows"
  output_dir: "/data/ocp-clients"
```

Maps to:

```yaml
# NEW (airgap.yaml)
providers:
  ocp_binaries:
    base_url: "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/"
    versions:
      - "latest-4.17"
    ignored_patterns:
      - "windows"
    output_dir: "ocp-clients"   # relative to data_dir now
```

Key changes: `version` → `versions`, `ignored_files` → `ignored_patterns`, `output_dir` is now relative to `server.data_dir` instead of absolute.

## Go Struct Mapping

```go
type Config struct {
    Server    ServerConfig              `yaml:"server"`
    Export    ExportConfig              `yaml:"export"`
    Schedule  ScheduleConfig            `yaml:"schedule"`
    Providers map[string]ProviderConfig `yaml:"providers"`
}

type ServerConfig struct {
    Listen  string `yaml:"listen"`
    DataDir string `yaml:"data_dir"`
    DBPath  string `yaml:"db_path"`
}

type ExportConfig struct {
    SplitSize    string `yaml:"split_size"`
    Compression  string `yaml:"compression"`
    OutputDir    string `yaml:"output_dir"`
    ManifestName string `yaml:"manifest_name"`
}
```

Provider configs are loaded as `map[string]any` initially, then each provider's `Configure()` method unmarshals its own section into typed structs. This keeps the config loader generic while providers own their schema.

## Runtime Config Modification

The CLI supports modifying config at runtime:

```bash
airgap config show                              # dump effective config
airgap config set providers.epel.enabled true    # toggle provider
airgap config set export.split_size "50GB"       # change split size
```

The web UI [[05 - Web UI Design#Settings|Settings page]] provides a form-based editor for the same operations.
