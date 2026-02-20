# Web UI Design

#airgap #ui #htmx #alpine

Back to [[00 - Project Index]] | Related: [[02 - Architecture]], [[06 - CLI Reference]]

## Tech Stack

- **htmx** — server-driven interactions, no client-side routing
- **Alpine.js** — lightweight reactive state for dropdowns, toggles, modals
- **`html/template`** (Go stdlib) — server-rendered templates with auto-escaping
- **Minimal CSS** — classless base (e.g., Simple.css or Pico.css) + a few custom utility classes
- **No build step** — static assets embedded in the Go binary via `embed.FS`

All templates live in `internal/ui/templates/`. Static JS/CSS in `internal/ui/static/`.

## Layout

Common layout with sidebar navigation:

```
┌──────────────────────────────────────────────┐
│  airgap                              [status]│
├────────────┬─────────────────────────────────┤
│            │                                 │
│ Dashboard  │   [Page Content]                │
│ Providers  │                                 │
│ Jobs       │                                 │
│ Transfer   │                                 │
│ Settings   │                                 │
│            │                                 │
└────────────┴─────────────────────────────────┘
```

## Pages

### Dashboard (`/`)

At-a-glance health for the entire sync ecosystem.

**Content:**
- Provider status cards — one per enabled provider showing: name, last sync time, file count, total size, status (healthy/warning/error)
- Quick action buttons: "Sync All", "Validate All", "Export"
- Recent activity feed — last 10 sync runs / transfers with timestamps and outcomes

**htmx patterns:**
- Cards auto-refresh every 30s via `hx-trigger="every 30s"` polling
- "Sync All" button uses `hx-post="/api/sync"` with `hx-swap="none"` (triggers via SSE instead)
- Activity feed uses `hx-get="/partials/activity"` with `hx-trigger="every 10s"`

### Providers (`/providers`)

Card grid of all configured providers.

**Content:**
- Card per provider: name, enabled/disabled toggle, repo count or version list, last sync summary
- Click card → drill into [[#Provider Detail]]
- "Add Provider" button (for custom-files type)

**htmx patterns:**
- Enable/disable toggle: `hx-patch="/api/providers/{name}" hx-vals='{"enabled": true}'`
- Cards link via standard `<a href="/providers/{name}">`

### Provider Detail (`/providers/{name}`)

Deep view into a single provider.

**Content:**
- Config summary (base URL, versions, ignore patterns)
- "Edit Config" button → inline form (Alpine.js toggle)
- "Sync Now" button → triggers sync with live log streaming
- File browser — sortable table of downloaded files (name, size, checksum, last modified)
- Sync history — table of past sync runs with diffs (added/removed/unchanged counts)

**htmx patterns:**
- Sync trigger: `hx-post="/api/providers/{name}/sync"` → redirects to SSE log stream
- File browser: paginated with `hx-get="/partials/providers/{name}/files?page=2"` and `hx-swap="innerHTML"`
- Config edit form: `hx-put="/api/providers/{name}/config"` with `hx-target="#config-summary"`

### Sync Jobs (`/jobs`)

**Content:**
- Active jobs — currently running syncs with progress indicators
- Scheduled jobs — next scheduled run per provider
- Job history — past runs with status, duration, file counts

**htmx patterns:**
- Active job progress: SSE connection via `hx-ext="sse" sse-connect="/api/jobs/{id}/stream"`
- Progress bar updates via SSE events with `sse-swap="progress"`

### Transfer Page (`/transfer`) {#Transfer Page}

Export and import wizards.

**Export wizard (tabs or steps):**
1. Select providers (checkboxes)
2. Choose destination path (text input, defaults to config `export.output_dir`)
3. Set split size (input with sensible default from config)
4. Review summary → "Start Export" button
5. Progress tracking — per-archive creation status, overall percentage

**Import wizard:**
1. Set source path (text input)
2. "Scan" button → reads manifest, shows summary (provider list, file counts, archive count)
3. "Validate" → per-archive checksum status (pass/fail with details)
4. "Import" → extraction progress with per-archive status
5. Final report — files imported, repos rebuilt, any errors

**htmx patterns:**
- Wizard steps use `hx-get="/partials/transfer/export/step2"` swapping into a target div
- Export/import progress via SSE
- Alpine.js manages wizard step state client-side

### Settings (`/settings`)

**Content:**
- Server config (listen address, data dir, db path) — read-only display
- Export defaults (split size, compression, output dir) — editable form
- Schedule config (enabled, cron expression) — editable form
- External tool paths (oc-mirror binary, mirror-registry binary) — editable form
- Log viewer — tail of recent log output, filterable by level

**htmx patterns:**
- Each config section is a form with `hx-put="/api/settings/{section}"` and `hx-target="this"` (replaces form with success message, then swaps back)
- Log viewer: `hx-get="/partials/logs?level=error&lines=100"` with polling

## SSE (Server-Sent Events) for Live Streaming

Long-running operations (sync, export, import) stream progress via SSE:

```
GET /api/jobs/{id}/stream
Content-Type: text/event-stream

event: progress
data: {"percent": 45, "current_file": "epel/9/Packages/ansible-core-2.15.rpm", "speed": "12.5 MB/s"}

event: progress
data: {"percent": 46, "current_file": "epel/9/Packages/ansible-lint-6.0.rpm", "speed": "11.8 MB/s"}

event: log
data: {"level": "info", "message": "Downloaded 2145 of 4521 files"}

event: complete
data: {"status": "success", "duration": "12m34s", "files_downloaded": 4521}
```

htmx SSE integration:

```html
<div hx-ext="sse" sse-connect="/api/jobs/{{.JobID}}/stream">
  <div sse-swap="progress" hx-swap="innerHTML">
    <!-- progress bar updates here -->
  </div>
  <div sse-swap="log" hx-swap="beforeend">
    <!-- log lines append here -->
  </div>
</div>
```

## API Endpoints Summary

All UI pages are backed by JSON API endpoints that the CLI can also use:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status` | Overall system status |
| GET | `/api/providers` | List all providers with status |
| GET | `/api/providers/{name}` | Provider detail |
| PATCH | `/api/providers/{name}` | Update provider config |
| POST | `/api/providers/{name}/sync` | Trigger sync |
| GET | `/api/providers/{name}/files` | List provider files |
| GET | `/api/jobs` | List all jobs |
| GET | `/api/jobs/{id}` | Job detail |
| GET | `/api/jobs/{id}/stream` | SSE stream for running job |
| POST | `/api/sync` | Trigger sync for all providers |
| POST | `/api/validate` | Trigger validation |
| POST | `/api/export` | Start export |
| POST | `/api/import` | Start import |
| GET | `/api/transfers` | Transfer history |
| GET | `/api/settings` | Current settings |
| PUT | `/api/settings/{section}` | Update settings |
| GET | `/api/logs` | Recent log entries |

## Embedded Static Assets

Using Go's `embed.FS` to bundle everything into the single binary:

```go
//go:embed templates/* static/*
var uiFS embed.FS
```

This means `airgap serve` works with zero external file dependencies — the web UI is compiled into the binary. For development, a `--dev` flag can serve from the filesystem instead for hot-reload.
