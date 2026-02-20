# Existing Repos Audit

#airgap #audit #existing-code

Back to [[00 - Project Index]]

## Overview

The current disconnected-environment toolchain is spread across six BadgerOps repos. Two are battle-tested production tools, two are archived forks of upstream OpenShift projects, and two are early prototypes.

## Repo Inventory

### epel-offline-sync (Python) — Production

**Repo:** [BadgerOps/epel-offline-sync](https://github.com/BadgerOps/epel-offline-sync)
**Language:** Python 3.9 (stdlib only)
**Container:** UBI9 base, runs via Podman

**What it does:**
Given an upstream EPEL repo URL in `config.ini`, it downloads `repomd.xml` and `primary.xml`, then uses `ElementTree` to identify packages to download. Deduplication is built in — it uses `hashlib` to compare local file hashes against the `common:checksum` in the XML manifest. Multi-threaded downloads.

**Key patterns to carry forward:**
- repomd.xml → primary.xml parsing pipeline
- Checksum-based dedup (don't re-download unchanged packages)
- Config-driven repo definitions

**Known limitation:** Does not remove packages that are removed upstream. The unified app will fix this — see [[03 - Configuration]] `cleanup_removed_packages`.

**Destination in unified app:** `internal/provider/epel/` — ported from Python to Go. See [[08 - Migration Path]].

---

### ocpsync (Go) — Production

**Repo:** [BadgerOps/ocpsync](https://github.com/BadgerOps/ocpsync)
**Language:** Go
**Version:** v1.0.0

**What it does:**
Downloads OCP client binaries and RHCOS images from `mirror.openshift.com`. Reads `config.yaml` to get base URLs, version lists, and ignore patterns. Downloads SHA256 checksum manifests, generates filtered file lists, downloads with retry (exponential backoff, 3 attempts), and validates each file against its checksum.

**Core structs:**
- `Config` → `OcpBinaries` section + `Rhcos` section
- Each section has `BaseURL`, `Version[]`, `IgnoredFiles[]`, `OutputDir`

**Key patterns to carry forward:**
- SHA256 manifest → filtered file list → download → validate pipeline
- Exponential backoff with retry
- Ignore-list filtering (skip windows, mac, cloud-specific images)
- Logrus structured logging (will migrate to `slog`)

**Destination in unified app:** `internal/provider/ocp/binaries.go` and `rhcos.go` — near-direct port, restructured into the Provider interface. See [[08 - Migration Path]].

---

### mirror-registry (Go + Ansible) — Archived Fork

**Repo:** [BadgerOps/mirror-registry](https://github.com/BadgerOps/mirror-registry)
**Upstream:** [quay/mirror-registry](https://github.com/quay/mirror-registry)
**Language:** Go 68.7%, Jinja 11.5%, Makefile 10.6%, Dockerfile 9.2%
**Status:** Archived Feb 2026

**What it does:**
CLI tool that orchestrates Ansible playbooks to deploy a standalone Quay registry (Quay + Redis + Postgres via Podman). Used to mirror OCP container images in disconnected environments.

**Commands:** `install`, `upgrade`, `uninstall`
**Requirements:** RHEL 8 or Fedora, Podman v3.3+, OpenSSL, FQDN resolvable

**Destination in unified app:** Wrapped as external tool — `internal/provider/containers/registry.go` invokes the `mirror-registry` binary. See [[09 - Technical Decisions]] for the wrap-vs-reimplement decision.

---

### oc-mirror (Go) — Archived Fork

**Repo:** [BadgerOps/oc-mirror](https://github.com/BadgerOps/oc-mirror)
**Upstream:** [openshift/oc-mirror](https://github.com/openshift/oc-mirror)
**Language:** Go
**Status:** Archived

**What it does:**
Lifecycle manager for disconnected OCP environments. Mirrors container images, operators, and Helm charts based on an `ImageSetConfiguration` YAML. Handles differential updates.

**Destination in unified app:** Wrapped as external tool — `internal/provider/containers/ocmirror.go`. See [[09 - Technical Decisions]].

---

### dlserver (Go) — Early Prototype

**Repo:** [BadgerOps/dlserver](https://github.com/BadgerOps/dlserver)
**Language:** Go

**What it does:**
REST API server on port 8080 with SQLite persistence for scheduling download jobs. Has `/getjobs` (GET), `/schedule` (POST), duplicate checking. Defines a `Job` struct with `Name`, `Time`, `URL`.

**Key gap:** Has job CRUD but no actual download execution or file serving.

**Destination in unified app:** Conceptually replaced by the web UI + API layer. The job scheduling model (SQLite-backed, REST endpoints) carries forward into [[05 - Web UI Design]] and the scheduler in [[02 - Architecture]].

---

### rpm-builder (Go) — Early Prototype

**Repo:** [BadgerOps/rpm-builder](https://github.com/BadgerOps/rpm-builder)
**Language:** Go (100%)
**License:** AGPL-3.0
**Status:** 2 commits

**What it does:**
Wrapper to streamline RPM spec creation and build automation.

**Destination in unified app:** Out of scope for v1. Could become a provider later if custom RPM building is needed in the disconnected environment.

## Patterns Across Repos

All the sync tools share the same fundamental loop:

```
1. Fetch upstream manifest (repomd.xml, SHA256SUMS, imageset config)
2. Diff manifest against local state
3. Download new/changed files
4. Validate downloads against checksums
5. Report results
```

This pattern becomes the [[02 - Architecture|Provider interface]]: `Plan()` does steps 1-2, `Sync()` does steps 3-4, `Validate()` does step 5 independently.
