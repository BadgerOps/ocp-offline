# CLI Reference

#airgap #cli #cobra

Back to [[00 - Project Index]] | Related: [[02 - Architecture]], [[05 - Web UI Design]]

## Overview

Built with [Cobra](https://github.com/spf13/cobra). Every operation available in the [[05 - Web UI Design|web UI]] is also available via CLI. The CLI is the primary interface for scripting and automation (cron jobs, CI/CD pipelines, etc).

## Global Flags

```
--config string    Path to config file (default: search order)
--data-dir string  Override data directory
--log-level string Log level: debug, info, warn, error (default: info)
--log-format string Log format: text, json (default: text)
--quiet            Suppress non-error output
```

## Commands

### `airgap serve`

Start the web UI and API server.

```bash
airgap serve                          # start on configured listen address
airgap serve --listen 0.0.0.0:9090    # override listen address
airgap serve --dev                    # serve templates from filesystem (hot-reload)
```

Starts the HTTP server, the background scheduler, and the SSE hub. Runs until interrupted (SIGINT/SIGTERM).

### `airgap sync`

Trigger synchronization.

```bash
airgap sync --all                     # sync all enabled providers
airgap sync --provider epel           # sync single provider
airgap sync --provider epel,rhcos     # sync multiple providers
airgap sync --provider epel --dry-run # show what would change (runs Plan() only)
```

Flags:
- `--all` — sync all enabled providers
- `--provider string` — comma-separated list of provider names
- `--dry-run` — run `Plan()` only, print what would change, don't download
- `--force` — re-download all files regardless of checksum match

Output (normal):
```
Syncing epel...
  Planning: 4521 files, 2145 new, 0 removed, 2376 unchanged
  Downloading: [████████████████████░░░░░] 2145/2145 (12.5 MB/s)
  Validating: 4521/4521 OK
  Duration: 12m34s

Syncing ocp_binaries...
  Planning: 12 files, 3 new, 0 removed, 9 unchanged
  Downloading: [█████████████████████████] 3/3 (45.2 MB/s)
  Validating: 12/12 OK
  Duration: 1m12s

All syncs complete.
```

Output (dry-run):
```
[DRY RUN] epel:
  Download: 2145 files (17.4 GB)
  Delete:   0 files
  Skip:     2376 files (unchanged)

[DRY RUN] ocp_binaries:
  Download: 3 files (1.2 GB)
  Delete:   0 files
  Skip:     9 files (unchanged)
```

### `airgap validate`

Check integrity of all local content.

```bash
airgap validate --all                 # validate all providers
airgap validate --provider epel       # validate specific provider
```

Computes SHA256 of every file in the provider's output directory and compares against the stored manifest. Reports mismatches.

Output:
```
Validating epel...
  4521/4521 files OK
Validating ocp_binaries...
  12/12 files OK
Validating rhcos...
  [FAIL] rhcos/4.18/latest/rhcos-live.x86_64.iso — expected abc123, got def456
  5/6 files OK, 1 FAILED

Validation complete: 1 file failed. Run 'airgap sync --provider rhcos' to re-download.
```

### `airgap export`

Package content for physical media transfer. See [[04 - Transfer Workflow]] for full detail.

```bash
airgap export --to /mnt/usb                        # export all providers
airgap export --to /mnt/usb --provider epel,rhcos   # export subset
airgap export --to /mnt/usb --split-size 10GB       # override split size
airgap export --to /mnt/usb --compression gzip      # use gzip instead of zstd
```

Flags:
- `--to string` — destination directory (required)
- `--provider string` — comma-separated provider list (default: all enabled)
- `--split-size string` — override archive split size
- `--compression string` — override compression (zstd, gzip)

### `airgap import`

Import content from transfer media. See [[04 - Transfer Workflow]] for full detail.

```bash
airgap import --from /mnt/usb                # full import
airgap import --from /mnt/usb --verify-only  # just check archive integrity
airgap import --from /mnt/usb --force         # skip verification (not recommended)
```

Flags:
- `--from string` — source directory containing archives + manifest (required)
- `--verify-only` — validate archive checksums without extracting
- `--force` — skip checksum validation (use when you've already verified)

### `airgap status`

Show current sync state summary.

```bash
airgap status                         # overview of all providers
airgap status --provider epel         # detailed status for one provider
```

Output:
```
Provider        Status    Last Sync            Files    Size
─────────       ──────    ─────────            ─────    ────
epel            OK        2026-02-18 02:00     4521     17.4 GB
ocp_binaries    OK        2026-02-18 02:15     12       1.2 GB
rhcos           WARNING   2026-02-18 02:20     6        4.8 GB
containers      OK        2026-02-16 02:00     —        22.1 GB
registry        RUNNING   —                    —        —

Total: 45.5 GB across 4 providers
Next scheduled sync: 2026-02-23 02:00
```

### `airgap config`

View and modify configuration.

```bash
airgap config show                                 # dump effective config as YAML
airgap config show --provider epel                  # show provider-specific config
airgap config set providers.epel.enabled true       # set a value
airgap config set export.split_size "50GB"          # change export defaults
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Config error (file not found, parse failure) |
| 3 | Sync failure (some files failed after all retries) |
| 4 | Validation failure (checksum mismatches found) |
| 5 | Export/import failure (archive creation or extraction failed) |

## Shell Completion

```bash
airgap completion bash > /etc/bash_completion.d/airgap
airgap completion zsh > "${fpath[1]}/_airgap"
airgap completion fish > ~/.config/fish/completions/airgap.fish
```
