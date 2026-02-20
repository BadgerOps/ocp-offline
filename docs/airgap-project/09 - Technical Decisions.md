# Technical Decisions

#airgap #adr #decisions

Back to [[00 - Project Index]] | Related: [[02 - Architecture]]

## Decision Log

Architecture Decision Records (ADR) style — each decision records the context, options considered, choice made, and rationale.

---

### ADR-001: Language — Go

**Context:** Existing repos are split between Python (epel-offline-sync) and Go (ocpsync, dlserver, rpm-builder). Need a single language for the unified app.

**Options:**
1. Go
2. Python
3. Rust

**Decision:** Go

**Rationale:**
- 4 of 6 existing repos are Go — majority of code already exists in Go
- Single binary distribution — critical for air-gapped environments where package managers may not be available
- Excellent concurrency primitives (goroutines) for parallel downloads
- Strong stdlib: `net/http`, `html/template`, `encoding/xml`, `crypto/sha256`, `archive/tar`
- `embed.FS` lets us bundle the web UI into the binary
- The EPEL Python code is algorithmically simple enough that porting to Go is low-risk

---

### ADR-002: Web Framework — `net/http` + chi router

**Context:** Need HTTP server for web UI and REST API.

**Options:**
1. `net/http` + `chi` router
2. Gin
3. Echo
4. Fiber

**Decision:** `net/http` + `chi`

**Rationale:**
- chi is stdlib-compatible (`http.Handler` interface) — no framework lock-in
- Lightweight: adds routing and middleware, nothing else
- Middleware composability (logging, recovery, CORS) without magic
- No reflection-based binding or code generation
- Widely used in the Go community, well-maintained

---

### ADR-003: Frontend — htmx + Alpine.js

**Context:** Need a web UI for admin operations. The user specifically wants htmx + Alpine.js.

**Options:**
1. htmx + Alpine.js (server-rendered)
2. React SPA
3. Vue SPA
4. Svelte SPA

**Decision:** htmx + Alpine.js

**Rationale:**
- No build step, no node_modules, no JavaScript toolchain
- Server-rendered HTML via Go's `html/template` — keeps all logic in Go
- htmx handles dynamic interactions (AJAX, SSE, polling) with HTML attributes
- Alpine.js handles client-side state for dropdowns, modals, toggles
- Templates embedded in the binary via `embed.FS` — zero external dependencies
- Perfect for a tool that runs in disconnected environments where you can't `npm install`

---

### ADR-004: Database — SQLite (pure Go)

**Context:** Need persistent storage for job state, sync history, file inventory.

**Options:**
1. `modernc.org/sqlite` (pure Go, no CGO)
2. `mattn/go-sqlite3` (CGO-based)
3. PostgreSQL
4. BoltDB / bbolt

**Decision:** `modernc.org/sqlite` (pure Go)

**Rationale:**
- Zero external dependencies — no C compiler or shared libraries needed
- Single file database — trivial to back up, move, or reset
- Perfect for single-node tools (this isn't a distributed system)
- Full SQL support for complex queries (join sync_runs with file_inventory, etc.)
- Pure Go means cross-compilation works without CGO headaches
- Tradeoff: slightly slower than CGO sqlite3, but this is a management tool, not a high-throughput database

---

### ADR-005: CLI Framework — Cobra

**Context:** Need a CLI framework for structured commands and flags.

**Decision:** Cobra

**Rationale:** Industry standard for Go CLIs. Used by kubectl, docker, gh, hugo, and nearly every other major Go CLI tool. Built-in shell completion, help generation, and flag parsing. Your existing familiarity from other Go projects.

---

### ADR-006: Compression — zstd (default), gzip (option)

**Context:** Export archives need compression for physical media transfer. Multi-GB datasets need fast compression.

**Options:**
1. zstd
2. gzip
3. lz4
4. xz

**Decision:** zstd default, gzip as fallback

**Rationale:**
- zstd is 3-5x faster than gzip at similar compression ratios
- For 100GB+ datasets, this is the difference between hours and minutes
- `github.com/klauspost/compress/zstd` is a mature, pure-Go implementation
- gzip option for environments that don't have zstd tooling (though our binary handles decompression)
- xz has better ratios but is 10x slower — not worth it for this use case
- lz4 is faster but worse ratios — the transfer media has finite space

---

### ADR-007: Archive Strategy — Independent Split Archives

**Context:** Need to split large exports across multiple archive files for media transfer. See [[04 - Transfer Workflow]].

**Options:**
1. Custom split tar writer (each archive is independently decompressible)
2. Single tar piped through `split` (all parts needed to reconstruct)
3. One archive per provider

**Decision:** Custom split tar writer

**Rationale:**
- Each archive is independently extractable — a corrupt part only affects files in that part
- With `split`, a corrupt part makes the entire archive unrecoverable
- One-per-provider doesn't help with the split size problem (EPEL can be 100GB+)
- The custom writer tracks byte counts and rolls to a new file at the boundary
- The manifest records which files are in which archive, enabling targeted re-transfer

---

### ADR-008: Wrap vs Reimplement External Tools {#Wrap vs Reimplement External Tools}

**Context:** oc-mirror and mirror-registry are complex upstream tools. Should we wrap them or reimplement their functionality?

**Options:**
1. Wrap as external tool invocations
2. Reimplement core functionality in Go
3. Fork and embed as Go libraries

**Decision:** Wrap as external tools

**Rationale:**
- oc-mirror handles OCI image mirroring, operator catalog parsing, Helm chart handling — reimplementing this is months of work
- mirror-registry uses Ansible playbooks for Quay deployment — reimplementing Quay deployment logic is complex and brittle
- Wrapping means we get upstream updates for free (just update the binary)
- Clean separation of concerns — we handle sync orchestration, they handle domain-specific operations
- Binary path is configurable — easy to test with different versions
- Tradeoff: external dependency on these binaries existing on the system
- Mitigation: our Containerfile bundles them, and `airgap status` checks for their presence

---

### ADR-009: Config Format — YAML

**Context:** Need a config file format for the unified app.

**Options:**
1. YAML
2. TOML
3. JSON
4. INI

**Decision:** YAML

**Rationale:**
- ocpsync already uses YAML — familiar
- Standard in the Kubernetes/OpenShift ecosystem
- Supports comments (unlike JSON)
- Deeply nested config is readable (unlike INI)
- `gopkg.in/yaml.v3` is mature and widely used
- The oc-mirror ImageSetConfiguration is also YAML — consistency

---

### ADR-010: Logging — slog (stdlib)

**Context:** Need structured logging. ocpsync uses logrus.

**Options:**
1. `log/slog` (Go stdlib, 1.21+)
2. logrus
3. zap
4. zerolog

**Decision:** `slog`

**Rationale:**
- Part of Go stdlib since 1.21 — no external dependency
- Structured logging with key-value pairs
- Pluggable handlers (text for CLI, JSON for machine parsing)
- Replaces logrus which is in maintenance mode
- Good enough for a single-binary tool — we don't need zap's extreme performance
