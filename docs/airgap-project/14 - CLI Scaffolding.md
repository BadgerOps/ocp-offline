# CLI Scaffolding

> Subagent output from CLI implementation. Preserved for posterity.

## Source Files
- `CLI_SCAFFOLDING.md`
- `COBRA_CLI_SUMMARY.md`
- `CLI_IMPLEMENTATION_COMPLETE.txt`
- `IMPLEMENTATION_SUMMARY.txt`

---

## Overview

**Framework:** Cobra v1.8.1
**Files Created:** 9 Go command files in `cmd/airgap/`
**Total Code:** ~18 KB

## Command Files

| File | Size | Purpose |
|------|------|---------|
| `main.go` | 124B | Entry point, creates and executes root command |
| `root.go` | 3.5KB | Root command, persistent flags, PersistentPreRunE hook |
| `sync.go` | 2.6KB | Content synchronization with dry-run support |
| `validate.go` | 1.8KB | Content integrity checking |
| `serve.go` | 1.5KB | HTTP server startup |
| `status.go` | 1.8KB | Provider status display |
| `export.go` | 2.5KB | Offline content export |
| `importcmd.go` | 1.7KB | Offline content import (named to avoid Go keyword) |
| `config_cmd.go` | 2.2KB | Config show/set subcommands |

## Persistent Flags (Root Level)

```
--config string        Path to config file
--data-dir string      Override data directory
--log-level string     Set log level (debug/info/warn/error)
--log-format string    Set output format (text/json)
--quiet               Suppress non-error output
```

## Command Reference

```bash
airgap sync --all                    # Sync all providers
airgap sync --provider xxx           # Sync specific providers
airgap sync --dry-run                # Preview without changes
airgap sync --force                  # Force re-download

airgap validate --all                # Validate all
airgap validate --provider xxx       # Validate specific

airgap serve                         # Start server
airgap serve --listen 127.0.0.1:9000 # Custom listen address

airgap status                        # Show all status
airgap status --provider xxx         # Specific status
airgap status --failed               # Only failures

airgap export --to /path             # Export content
airgap export --to /path --split-size 10GB

airgap import --from /path           # Import content
airgap import --from /path --verify-only

airgap config show                   # Show current config
airgap config set KEY VALUE          # Set config value
```

## Execution Flow

```
main() → root command
  └→ PersistentPreRunE:
       1. setupLogging() → initializes slog
       2. config.FindConfigFile() → discovers config
       3. config.Load() or DefaultConfig()
       4. Flag overrides applied
  └→ Subcommand RunE:
       - Access globalCfg
       - Use slog.Default() for logging
       - Execute command logic
```

## Integration Points

- `config.FindConfigFile()` — Auto-discover config
- `config.Load(path)` — Load config from file
- `config.DefaultConfig()` — Get defaults
- `slog.Default()` — Get logger in any command
- Provider registry pattern ready for real implementations

## Design Patterns

- All commands follow `newXxxCmd()` + `xxxRun()` pattern
- All commands check `globalCfg` before use
- Comma-separated provider list support
- All errors wrapped with `fmt.Errorf` context
- Required flags via `MarkFlagRequired()`

---

*This note consolidates: CLI_SCAFFOLDING.md, COBRA_CLI_SUMMARY.md, CLI_IMPLEMENTATION_COMPLETE.txt, IMPLEMENTATION_SUMMARY.txt*
