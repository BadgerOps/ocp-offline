# Provider Management Design

## Summary

Add full CRUD for provider configurations through the web UI. Providers are stored in SQLite (not YAML) for atomic updates. The YAML file remains the source for server-level settings (listen address, data dir). On first startup, existing YAML provider configs are seeded into the database.

## Data Model

### New table: `provider_configs` (migration v3)

```sql
CREATE TABLE provider_configs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 0,
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Go model

```go
type ProviderConfig struct {
    ID         int64
    Name       string
    Type       string     // "epel", "ocp_binaries", "rhcos", "container_images", "registry", "custom_files"
    Enabled    bool
    ConfigJSON string     // provider-specific settings as JSON
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

### Seeding

On startup, if `provider_configs` table has 0 rows, iterate `cfg.Providers` from YAML and insert each as a row (marshal the raw `map[string]interface{}` to JSON for `config_json`). This is a one-time migration.

### Provider registration

`initializeComponents()` in `root.go` reads from `store.ListProviderConfigs()` instead of `cfg.Providers`. For each enabled config, it instantiates the correct provider type, calls `Configure()`, and registers in the registry.

## REST API

| Method | Path | Description |
|--------|------|-------------|
| `GET /api/providers/config` | List all provider configs |
| `POST /api/providers/config` | Create a new provider config |
| `PUT /api/providers/config/{name}` | Update an existing provider config |
| `DELETE /api/providers/config/{name}` | Delete a provider config |
| `POST /api/providers/config/{name}/toggle` | Quick enable/disable toggle |

### Request/response shapes

**Create/Update body:**
```json
{
  "name": "epel",
  "type": "epel",
  "enabled": true,
  "config": { /* provider-specific fields */ }
}
```

**Toggle:** No body. Flips current enabled state. Returns updated config.

**List response:** JSON array of all configs with id, name, type, enabled, config, created_at, updated_at.

**Delete:** Returns 204 No Content.

### Validation

- `type` must be one of: epel, ocp_binaries, rhcos, container_images, registry, custom_files
- `name` must be unique (409 Conflict on duplicate)
- For implemented types, config JSON is parsed into the typed struct to validate
- Unimplemented types accept any config but get a "coming soon" badge in UI

## Hot-Reload

After any config CRUD operation, the server immediately reconfigures the running engine via `SyncManager.ReconfigureProviders()`.

### Implementation

- Add `sync.RWMutex` to `SyncManager`
- Sync operations acquire a read lock
- `ReconfigureProviders()` acquires a write lock
- Method reads all enabled configs from DB, instantiates providers, swaps the registry
- On error, the old registry state is preserved

```go
func (m *SyncManager) ReconfigureProviders(configs []store.ProviderConfig) error
```

## UI Design

Enhanced `/providers` page with:

### Provider list
Table/card grid showing: name, type, enabled/disabled toggle switch (HTMX), file count, total size, Edit and Delete buttons.

### Add Provider
Button reveals an Alpine.js form:
- Type dropdown (all 6 types)
- Selecting a type shows type-specific fields via `x-show`
- Unimplemented types show "coming soon" note
- Submit POSTs to `/api/providers/config`

### Edit Provider
Clicking Edit swaps the row to an inline form (HTMX) pre-filled with current config. Submit PUTs to `/api/providers/config/{name}`.

### Type-specific fields

**epel:** repos list (name, base_url, output_dir each), max_concurrent_downloads, retry_attempts, cleanup_removed_packages

**ocp_binaries / rhcos:** base_url, versions list, ignored_patterns list, output_dir, retry_attempts

**container_images:** oc_mirror_binary, imageset_config, output_dir

**registry:** mirror_registry_binary, quay_root

**custom_files:** sources list (name, url, checksum_url, output_dir each)

List fields (versions, repos, sources) use Alpine.js for dynamic add/remove.

## Error Handling

- Duplicate name: 409 Conflict
- Non-existent provider: 404 Not Found
- Invalid config: 422 Unprocessable Entity with field-level errors
- ReconfigureProviders failure: 500, old config preserved
- Delete non-existent: 404

## Testing

- **Store:** TestProviderConfigCRUD, TestSeedProviderConfigs, TestProviderConfigToggle
- **API:** Test each endpoint for correct status codes and JSON responses
- **ReconfigureProviders:** Test registry changes after add/remove/toggle. Test mutex blocks during sync.
- **Seed logic:** Test seeding from YAML, test no-op when table has rows

## Key Files

| File | Change |
|------|--------|
| `internal/store/models.go` | Add ProviderConfig model |
| `internal/store/sqlite.go` | Migration v3, CRUD methods, seed method |
| `internal/store/sqlite_test.go` | Provider config tests |
| `internal/engine/sync.go` | Add RWMutex, ReconfigureProviders() |
| `internal/server/server.go` | New provider config routes |
| `internal/server/provider_config_handlers.go` | New: all config CRUD handlers |
| `internal/server/templates/providers.html` | Enhanced: toggles, edit/add forms |
| `cmd/airgap/root.go` | Read provider configs from DB, seed from YAML |
