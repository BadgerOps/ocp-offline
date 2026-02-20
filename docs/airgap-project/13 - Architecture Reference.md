# Architecture Reference

> Subagent output from Phase 1 architecture design. Preserved for posterity.

## Source File
- `ARCHITECTURE.md`

---

## System Overview

The airgap tool is a unified solution for managing offline/disconnected OpenShift environments. It consolidates multiple standalone scripts into a single Go binary with a provider-based plugin architecture.

## Core Architecture

### Provider Plugin Pattern

All content sources implement a common `Provider` interface:

```go
type Provider interface {
    Name() string
    Configure(cfg ProviderConfig) error
    Plan(ctx context.Context, opts SyncOptions) (*SyncPlan, error)
    Sync(ctx context.Context, plan *SyncPlan, opts SyncOptions) (*SyncReport, error)
    Validate(ctx context.Context) (*ValidationReport, error)
}
```

### Component Layers

```
┌─────────────────────────────────────────┐
│              CLI (Cobra)                │
├─────────────────────────────────────────┤
│           HTTP Server (htmx)            │
├─────────────────────────────────────────┤
│         Sync Engine (orchestration)      │
├─────────────────────────────────────────┤
│   Provider Registry (plugin discovery)   │
├──────┬──────┬──────┬──────┬────────────┤
│ EPEL │ OCP  │RHCOS │ Imgs │  Custom    │
├──────┴──────┴──────┴──────┴────────────┤
│      Download Client (HTTP + retry)      │
├─────────────────────────────────────────┤
│        SQLite Store (persistence)        │
└─────────────────────────────────────────┘
```

### Data Flow

1. **Plan Phase**: Provider examines remote manifest → compares with local state → produces SyncPlan
2. **Sync Phase**: Engine executes plan → downloads via worker pool → records in store
3. **Validate Phase**: Provider checksums local files → compares with expected → reports discrepancies

### Key Patterns

- **Plan-Execute Separation**: Every sync generates a plan first, enabling dry-run and audit
- **Worker Pool**: Configurable concurrency per provider for parallel downloads
- **Retry with Backoff**: Exponential backoff + jitter for transient failures
- **Resume Support**: HTTP Range headers for interrupted downloads
- **Dead Letter Queue**: Failed files tracked for retry analysis

### Extension Points

- New providers implement the `Provider` interface
- Provider configs are type-safe via `ParseProviderConfig[T]()` generics
- All providers auto-register via `Registry.Register()`

---

*This note consolidates: ARCHITECTURE.md*
