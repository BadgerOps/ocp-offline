# airgap — Project Index

#airgap #index

> Unified offline synchronization tool for disconnected/air-gapped environments.
> Single Go binary: sync, validate, export, import across OCP, EPEL, container images, and custom content.

## Design Documents

- [[01 - Existing Repos Audit]] — inventory of current BadgerOps repos and what carries forward
- [[02 - Architecture]] — system design, provider plugin model, project directory structure
- [[03 - Configuration]] — unified `airgap.yaml` config reference with full example
- [[04 - Transfer Workflow]] — the export/import engine, split tar.zst archives, physical media workflow
- [[05 - Web UI Design]] — htmx + Alpine.js pages, SSE streaming, template layout
- [[06 - CLI Reference]] — cobra commands, flags, and usage examples
- [[07 - Implementation Phases]] — 6-phase roadmap with deliverables per phase
- [[08 - Migration Path]] — how each existing repo maps into the unified codebase
- [[09 - Technical Decisions]] — ADR-style log of every major tech choice and rationale
- [[10 - Retry and Resilience]] — download reliability patterns, backoff, dead letter queue

- [[11 - Development Setup]] — Nix flake, building, testing, CI

## Subagent Implementation Logs

- [[12 - Foundation Setup]] — Phase 1 project setup, directory structure, initial code stats
- [[13 - Architecture Reference]] — subagent architecture design output
- [[14 - CLI Scaffolding]] — Cobra CLI scaffolding details, command reference, patterns
- [[15 - Store Implementation]] — SQLite store API, models, migrations
- [[16 - Component Wiring]] — initialization sequence, dependency graph, error handling strategy
- [[17 - Code Verification]] — comprehensive verification report, build errors found and fixed

## Quick Links

- GitHub repos: [BadgerOps](https://github.com/BadgerOps)
- Key upstream tools: [oc-mirror](https://github.com/openshift/oc-mirror), [mirror-registry](https://github.com/quay/mirror-registry)

## Status

| Phase | Description | Status |
|-------|-------------|--------|
| 1 | Foundation + OCP providers | **Complete** — build errors fixed, Go 1.23 |
| 2 | EPEL provider + Web UI skeleton | **Complete** — EPEL provider, server routes, htmx templates |
| 3 | Export/Import engine | Not started |
| 4 | External tool wrappers | Not started |
| 5 | Web UI polish | Not started |
| 6 | Hardening | Not started |
