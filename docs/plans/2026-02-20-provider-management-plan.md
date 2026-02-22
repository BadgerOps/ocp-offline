# Provider Management Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Full CRUD for provider configurations via the web UI, with SQLite storage, hot-reload, and YAML seeding.

**Architecture:** Provider configs move from YAML to a `provider_configs` SQLite table. On first startup, existing YAML provider entries are seeded into the DB. The engine gains a mutex-protected `ReconfigureProviders()` method for hot-reload. A REST API and enhanced `/providers` page expose CRUD operations.

**Tech Stack:** Go, SQLite (modernc.org/sqlite), HTMX, Alpine.js, html/template

---

### Task 1: Add ProviderConfig model to store

**Files:**
- Modify: `internal/store/models.go:59` (after TransferArchive)

**Step 1: Add the model**

Add after the `TransferArchive` struct at the end of `models.go`:

```go
// ProviderConfig stores a provider's configuration in the database.
type ProviderConfig struct {
	ID         int64
	Name       string
	Type       string // "epel", "ocp_binaries", "rhcos", "container_images", "registry", "custom_files"
	Enabled    bool
	ConfigJSON string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
```

**Step 2: Verify build**

Run: `go build ./internal/store/...`
Expected: compiles with no errors

**Step 3: Commit**

```bash
git add internal/store/models.go
git commit -m "feat(store): add ProviderConfig model"
```

---

### Task 2: Add migration v3 for provider_configs table

**Files:**
- Modify: `internal/store/migrations.go:121` (add after migration v2)

**Step 1: Write the failing test**

Add to `internal/store/sqlite_test.go`:

```go
func TestProviderConfigTableExists(t *testing.T) {
	s := newTestStore(t)

	// Verify the table exists by inserting a row
	_, err := s.db.Exec(`INSERT INTO provider_configs (name, type, enabled, config_json) VALUES (?, ?, ?, ?)`,
		"test", "epel", 1, "{}")
	if err != nil {
		t.Fatalf("provider_configs table should exist after migration: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestProviderConfigTableExists -v`
Expected: FAIL — "no such table: provider_configs"

**Step 3: Add migration v3**

In `internal/store/migrations.go`, add a new entry to the `migrations` slice after the `version: 2` entry (before the closing `}` of the slice):

```go
{
	version: 3,
	sql: `
		CREATE TABLE provider_configs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			type        TEXT NOT NULL,
			enabled     INTEGER NOT NULL DEFAULT 0,
			config_json TEXT NOT NULL DEFAULT '{}',
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`,
},
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestProviderConfigTableExists -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/migrations.go internal/store/sqlite_test.go
git commit -m "feat(store): add migration v3 for provider_configs table"
```

---

### Task 3: Add ProviderConfig CRUD methods to store

**Files:**
- Modify: `internal/store/sqlite.go` (add after TransferArchive operations, before the end)
- Modify: `internal/store/sqlite_test.go`

**Step 1: Write the failing tests**

Add to `internal/store/sqlite_test.go`:

```go
func TestCreateProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{
		Name:       "epel",
		Type:       "epel",
		Enabled:    true,
		ConfigJSON: `{"repos":[{"name":"epel-9","base_url":"https://example.com"}]}`,
	}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatalf("CreateProviderConfig error: %v", err)
	}
	if pc.ID == 0 {
		t.Error("expected non-zero ID after create")
	}
}

func TestCreateProviderConfigDuplicateName(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	dup := &ProviderConfig{Name: "epel", Type: "epel", Enabled: false, ConfigJSON: "{}"}
	err := s.CreateProviderConfig(dup)
	if err == nil {
		t.Fatal("expected error on duplicate name")
	}
}

func TestGetProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: `{"key":"val"}`}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProviderConfig("epel")
	if err != nil {
		t.Fatalf("GetProviderConfig error: %v", err)
	}
	if got.Name != "epel" {
		t.Errorf("name = %q, want %q", got.Name, "epel")
	}
	if got.Type != "epel" {
		t.Errorf("type = %q, want %q", got.Type, "epel")
	}
	if !got.Enabled {
		t.Error("expected enabled = true")
	}
	if got.ConfigJSON != `{"key":"val"}` {
		t.Errorf("config_json = %q, want %q", got.ConfigJSON, `{"key":"val"}`)
	}
}

func TestGetProviderConfigNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetProviderConfig("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestListProviderConfigs(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{"aaa", "zzz", "mmm"} {
		pc := &ProviderConfig{Name: name, Type: "epel", Enabled: true, ConfigJSON: "{}"}
		if err := s.CreateProviderConfig(pc); err != nil {
			t.Fatal(err)
		}
	}

	configs, err := s.ListProviderConfigs()
	if err != nil {
		t.Fatalf("ListProviderConfigs error: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}
	// Should be ordered by name
	if configs[0].Name != "aaa" || configs[1].Name != "mmm" || configs[2].Name != "zzz" {
		t.Errorf("unexpected ordering: %v, %v, %v", configs[0].Name, configs[1].Name, configs[2].Name)
	}
}

func TestUpdateProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: `{"old":true}`}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	pc.Enabled = false
	pc.ConfigJSON = `{"new":true}`
	if err := s.UpdateProviderConfig(pc); err != nil {
		t.Fatalf("UpdateProviderConfig error: %v", err)
	}

	got, err := s.GetProviderConfig("epel")
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Error("expected enabled = false after update")
	}
	if got.ConfigJSON != `{"new":true}` {
		t.Errorf("config_json = %q, want %q", got.ConfigJSON, `{"new":true}`)
	}
}

func TestDeleteProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProviderConfig("epel"); err != nil {
		t.Fatalf("DeleteProviderConfig error: %v", err)
	}

	_, err := s.GetProviderConfig("epel")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteProviderConfigNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeleteProviderConfig("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent delete")
	}
}

func TestToggleProviderConfig(t *testing.T) {
	s := newTestStore(t)

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	// Toggle off
	if err := s.ToggleProviderConfig("epel"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProviderConfig("epel")
	if got.Enabled {
		t.Error("expected enabled = false after toggle")
	}

	// Toggle back on
	if err := s.ToggleProviderConfig("epel"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProviderConfig("epel")
	if !got.Enabled {
		t.Error("expected enabled = true after second toggle")
	}
}

func TestCountProviderConfigs(t *testing.T) {
	s := newTestStore(t)

	count, err := s.CountProviderConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	pc := &ProviderConfig{Name: "epel", Type: "epel", Enabled: true, ConfigJSON: "{}"}
	if err := s.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	count, err = s.CountProviderConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run "TestCreateProviderConfig|TestGetProviderConfig|TestListProviderConfigs|TestUpdateProviderConfig|TestDeleteProviderConfig|TestToggleProviderConfig|TestCountProviderConfigs" -v`
Expected: FAIL — methods don't exist

**Step 3: Implement the CRUD methods**

Add to `internal/store/sqlite.go` after the ListTransfers function:

```go
// ============================================================================
// ProviderConfig Operations
// ============================================================================

// CreateProviderConfig inserts a new ProviderConfig and sets its ID.
func (s *Store) CreateProviderConfig(pc *ProviderConfig) error {
	const query = `
		INSERT INTO provider_configs (name, type, enabled, config_json)
		VALUES (?, ?, ?, ?)
	`
	result, err := s.db.Exec(query, pc.Name, pc.Type, pc.Enabled, pc.ConfigJSON)
	if err != nil {
		return fmt.Errorf("failed to insert provider config: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}
	pc.ID = id
	return nil
}

// GetProviderConfig retrieves a ProviderConfig by name.
func (s *Store) GetProviderConfig(name string) (*ProviderConfig, error) {
	const query = `
		SELECT id, name, type, enabled, config_json, created_at, updated_at
		FROM provider_configs WHERE name = ?
	`
	pc := &ProviderConfig{}
	err := s.db.QueryRow(query, name).Scan(
		&pc.ID, &pc.Name, &pc.Type, &pc.Enabled,
		&pc.ConfigJSON, &pc.CreatedAt, &pc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("provider config not found: %s: %w", name, err)
	}
	return pc, nil
}

// ListProviderConfigs retrieves all ProviderConfigs ordered by name.
func (s *Store) ListProviderConfigs() ([]ProviderConfig, error) {
	const query = `
		SELECT id, name, type, enabled, config_json, created_at, updated_at
		FROM provider_configs ORDER BY name
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query provider configs: %w", err)
	}
	defer rows.Close()

	var configs []ProviderConfig
	for rows.Next() {
		pc := ProviderConfig{}
		if err := rows.Scan(&pc.ID, &pc.Name, &pc.Type, &pc.Enabled,
			&pc.ConfigJSON, &pc.CreatedAt, &pc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan provider config: %w", err)
		}
		configs = append(configs, pc)
	}
	return configs, rows.Err()
}

// UpdateProviderConfig updates an existing ProviderConfig by ID.
func (s *Store) UpdateProviderConfig(pc *ProviderConfig) error {
	const query = `
		UPDATE provider_configs SET
			name = ?, type = ?, enabled = ?, config_json = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	result, err := s.db.Exec(query, pc.Name, pc.Type, pc.Enabled, pc.ConfigJSON, pc.ID)
	if err != nil {
		return fmt.Errorf("failed to update provider config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider config not found: %d", pc.ID)
	}
	return nil
}

// DeleteProviderConfig deletes a ProviderConfig by name.
func (s *Store) DeleteProviderConfig(name string) error {
	const query = `DELETE FROM provider_configs WHERE name = ?`
	result, err := s.db.Exec(query, name)
	if err != nil {
		return fmt.Errorf("failed to delete provider config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider config not found: %s", name)
	}
	return nil
}

// ToggleProviderConfig flips the enabled state of a provider.
func (s *Store) ToggleProviderConfig(name string) error {
	const query = `
		UPDATE provider_configs
		SET enabled = CASE WHEN enabled = 1 THEN 0 ELSE 1 END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE name = ?
	`
	result, err := s.db.Exec(query, name)
	if err != nil {
		return fmt.Errorf("failed to toggle provider config: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("provider config not found: %s", name)
	}
	return nil
}

// CountProviderConfigs returns the number of provider configs.
func (s *Store) CountProviderConfigs() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM provider_configs").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count provider configs: %w", err)
	}
	return count, nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run "TestCreateProviderConfig|TestGetProviderConfig|TestListProviderConfigs|TestUpdateProviderConfig|TestDeleteProviderConfig|TestToggleProviderConfig|TestCountProviderConfigs" -v`
Expected: all PASS

**Step 5: Commit**

```bash
git add internal/store/sqlite.go internal/store/sqlite_test.go
git commit -m "feat(store): add ProviderConfig CRUD methods"
```

---

### Task 4: Add SeedProviderConfigs method to store

**Files:**
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/sqlite_test.go`

**Step 1: Write the failing test**

```go
func TestSeedProviderConfigs(t *testing.T) {
	s := newTestStore(t)

	yamlProviders := map[string]map[string]interface{}{
		"epel": {
			"enabled":    true,
			"repos":      []interface{}{map[string]interface{}{"name": "epel-9"}},
		},
		"ocp_binaries": {
			"enabled":  true,
			"base_url": "https://mirror.openshift.com",
		},
	}

	if err := s.SeedProviderConfigs(yamlProviders); err != nil {
		t.Fatalf("SeedProviderConfigs error: %v", err)
	}

	configs, _ := s.ListProviderConfigs()
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	// Second call should be a no-op (table not empty)
	if err := s.SeedProviderConfigs(yamlProviders); err != nil {
		t.Fatalf("second SeedProviderConfigs error: %v", err)
	}

	configs, _ = s.ListProviderConfigs()
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs after no-op seed, got %d", len(configs))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSeedProviderConfigs -v`
Expected: FAIL — method doesn't exist

**Step 3: Implement SeedProviderConfigs**

Add to `internal/store/sqlite.go` (needs `"encoding/json"` import):

```go
// SeedProviderConfigs populates provider_configs from a YAML providers map.
// This is a no-op if the table already has rows.
func (s *Store) SeedProviderConfigs(yamlProviders map[string]map[string]interface{}) error {
	count, err := s.CountProviderConfigs()
	if err != nil {
		return err
	}
	if count > 0 {
		s.logger.Info("provider_configs table already populated, skipping seed")
		return nil
	}

	// Determine the type from the provider name.
	// The name in the YAML is the canonical type for known providers.
	knownTypes := map[string]bool{
		"epel": true, "ocp_binaries": true, "rhcos": true,
		"container_images": true, "registry": true, "custom_files": true,
	}

	for name, rawCfg := range yamlProviders {
		provType := name
		if !knownTypes[provType] {
			provType = "custom_files"
		}

		enabled := false
		if e, ok := rawCfg["enabled"].(bool); ok {
			enabled = e
		}

		configJSON, err := json.Marshal(rawCfg)
		if err != nil {
			s.logger.Warn("failed to marshal provider config for seeding", "name", name, "error", err)
			continue
		}

		pc := &ProviderConfig{
			Name:       name,
			Type:       provType,
			Enabled:    enabled,
			ConfigJSON: string(configJSON),
		}
		if err := s.CreateProviderConfig(pc); err != nil {
			s.logger.Warn("failed to seed provider config", "name", name, "error", err)
		}
	}

	s.logger.Info("seeded provider configs from YAML", "count", len(yamlProviders))
	return nil
}
```

Add `"encoding/json"` to the import block of `sqlite.go`.

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSeedProviderConfigs -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/sqlite.go internal/store/sqlite_test.go
git commit -m "feat(store): add SeedProviderConfigs for YAML-to-DB migration"
```

---

### Task 5: Add RWMutex and ReconfigureProviders to SyncManager

**Files:**
- Modify: `internal/engine/sync.go:17-24` (SyncManager struct)
- Modify: `internal/engine/sync.go:57-60` (SyncProvider — add read lock)
- Create test in: `internal/engine/sync_test.go`

**Step 1: Write the failing test**

Add to `internal/engine/sync_test.go`:

```go
func TestReconfigureProviders(t *testing.T) {
	registry := provider.NewRegistry()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(dbPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}
	client := download.NewClient(logger)
	mgr := NewSyncManager(registry, st, client, cfg, logger)

	// Initially no providers
	if len(registry.Names()) != 0 {
		t.Fatalf("expected 0 providers, got %d", len(registry.Names()))
	}

	// Add a provider config to DB
	pc := &store.ProviderConfig{
		Name:       "epel",
		Type:       "epel",
		Enabled:    true,
		ConfigJSON: `{"enabled":true,"repos":[{"name":"epel-9","base_url":"https://example.com","output_dir":"epel/9"}]}`,
	}
	if err := st.CreateProviderConfig(pc); err != nil {
		t.Fatal(err)
	}

	// Reconfigure
	configs, _ := st.ListProviderConfigs()
	if err := mgr.ReconfigureProviders(configs); err != nil {
		t.Fatalf("ReconfigureProviders error: %v", err)
	}

	// Should now have 1 provider
	names := registry.Names()
	if len(names) != 1 {
		t.Fatalf("expected 1 provider after reconfigure, got %d", len(names))
	}
	if names[0] != "epel" {
		t.Errorf("expected provider name 'epel', got %q", names[0])
	}

	// Disable it and reconfigure
	st.ToggleProviderConfig("epel")
	configs, _ = st.ListProviderConfigs()
	if err := mgr.ReconfigureProviders(configs); err != nil {
		t.Fatal(err)
	}

	// Disabled providers should still be in the registry, just not enabled
	// (the registry tracks all known providers, config.ProviderEnabled checks enabled)
	// Actually — ReconfigureProviders only registers enabled ones, so disabled ones are removed
	if len(registry.Names()) != 0 {
		t.Fatalf("expected 0 providers after disabling, got %d", len(registry.Names()))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestReconfigureProviders -v`
Expected: FAIL — ReconfigureProviders not defined

**Step 3: Implement ReconfigureProviders and add mutex**

Modify `internal/engine/sync.go`:

1. Add `"sync"` to the import block.

2. Add `mu sync.RWMutex` field to SyncManager:

```go
type SyncManager struct {
	registry *provider.Registry
	store    *store.Store
	client   *download.Client
	config   *config.Config
	logger   *slog.Logger
	mu       sync.RWMutex
}
```

3. Add read lock to `SyncProvider` (first line of the method body, after the opening `{`):

```go
func (m *SyncManager) SyncProvider(ctx context.Context, name string, opts provider.SyncOptions) (*provider.SyncReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.logger.Info("starting sync", "provider", name, "dry_run", opts.DryRun)
	// ... rest unchanged
```

4. Add the `ReconfigureProviders` method. This needs a provider factory, so add a helper function. Place after the `Status()` method:

```go
// ReconfigureProviders rebuilds the provider registry from the given configs.
// Only enabled providers with implemented types are instantiated.
// Acquires a write lock to prevent races with running syncs.
func (m *SyncManager) ReconfigureProviders(configs []store.ProviderConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	newRegistry := provider.NewRegistry()

	for _, pc := range configs {
		if !pc.Enabled {
			continue
		}

		p, err := instantiateProvider(pc, m.config.Server.DataDir, m.logger)
		if err != nil {
			m.logger.Warn("skipping provider: failed to instantiate", "name", pc.Name, "type", pc.Type, "error", err)
			continue
		}

		// Parse the config JSON back to a map for Configure()
		var rawCfg map[string]interface{}
		if err := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); err != nil {
			m.logger.Warn("skipping provider: invalid config JSON", "name", pc.Name, "error", err)
			continue
		}

		if err := p.Configure(rawCfg); err != nil {
			m.logger.Warn("skipping provider: configure failed", "name", pc.Name, "error", err)
			continue
		}

		newRegistry.Register(p)
	}

	// Swap the registry contents
	// Clear existing and copy new entries
	for _, name := range m.registry.Names() {
		m.registry.Remove(name)
	}
	for _, p := range newRegistry.All() {
		m.registry.Register(p)
	}

	// Update the config.Providers map so ProviderEnabled() works correctly
	m.config.Providers = make(map[string]map[string]interface{})
	for _, pc := range configs {
		var rawCfg map[string]interface{}
		if err := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); err == nil {
			m.config.Providers[pc.Name] = rawCfg
		}
	}

	m.logger.Info("providers reconfigured", "active", len(newRegistry.Names()))
	return nil
}
```

5. Add `instantiateProvider` helper in the same file (needs imports for epel and ocp packages — but that would create an import cycle since engine already imports provider. Instead, put the factory in `cmd/airgap/root.go` or make ReconfigureProviders accept a factory function).

**Important design note:** To avoid an import cycle (`engine` -> `epel`/`ocp` -> `provider` -> back), the provider factory should live in `cmd/airgap/` or be injected. The cleanest approach:

Add a `ProviderFactory` field to `SyncManager`:

```go
// ProviderFactory creates a provider instance given a type name and data dir.
type ProviderFactory func(typeName, dataDir string, logger *slog.Logger) (provider.Provider, error)
```

Then `ReconfigureProviders` calls `m.providerFactory(pc.Type, m.config.Server.DataDir, m.logger)`.

The factory itself lives in `cmd/airgap/root.go` where the imports for `epel` and `ocp` already exist.

Update `SyncManager`:
```go
type SyncManager struct {
	registry        *provider.Registry
	store           *store.Store
	client          *download.Client
	config          *config.Config
	logger          *slog.Logger
	mu              sync.RWMutex
	providerFactory ProviderFactory
}
```

Update `NewSyncManager` to accept the factory (or set it via a setter to avoid breaking existing callers):
```go
// SetProviderFactory sets the factory used by ReconfigureProviders.
func (m *SyncManager) SetProviderFactory(f ProviderFactory) {
	m.providerFactory = f
}
```

6. Also add `Remove(name)` to the Registry (it doesn't exist yet). Add to `internal/provider/provider.go`:

```go
// Remove deletes a provider from the registry by name.
func (r *Registry) Remove(name string) {
	delete(r.providers, name)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/engine/ -run TestReconfigureProviders -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/engine/sync.go internal/engine/sync_test.go internal/provider/provider.go
git commit -m "feat(engine): add ReconfigureProviders with RWMutex protection"
```

---

### Task 6: Update root.go to seed from YAML and load from DB

**Files:**
- Modify: `cmd/airgap/root.go:36-98` (initializeComponents function)

**Step 1: Implement the changes**

Replace the provider registration block in `initializeComponents()` (lines 64-92) with:

```go
	// Initialize provider registry
	globalRegistry = provider.NewRegistry()

	// Seed provider configs from YAML into DB on first run
	if err := st.SeedProviderConfigs(globalCfg.Providers); err != nil {
		logger.Warn("failed to seed provider configs", "error", err)
	}

	// Load provider configs from DB and register enabled providers
	providerConfigs, err := st.ListProviderConfigs()
	if err != nil {
		return fmt.Errorf("failed to list provider configs: %w", err)
	}

	for _, pc := range providerConfigs {
		if !pc.Enabled {
			continue
		}

		p, err := createProvider(pc.Type, globalCfg.Server.DataDir, logger)
		if err != nil {
			logger.Warn("skipping provider: unknown type", "name", pc.Name, "type", pc.Type)
			continue
		}

		// Parse config JSON back to raw map for Configure()
		var rawCfg map[string]interface{}
		if jsonErr := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); jsonErr != nil {
			logger.Warn("failed to parse provider config", "name", pc.Name, "error", jsonErr)
			continue
		}

		if cfgErr := p.Configure(rawCfg); cfgErr != nil {
			logger.Warn("failed to configure provider", "name", pc.Name, "error", cfgErr)
		}
		globalRegistry.Register(p)
	}

	// Also populate config.Providers from DB so ProviderEnabled() works
	globalCfg.Providers = make(map[string]map[string]interface{})
	for _, pc := range providerConfigs {
		var rawCfg map[string]interface{}
		if err := json.Unmarshal([]byte(pc.ConfigJSON), &rawCfg); err == nil {
			globalCfg.Providers[pc.Name] = rawCfg
		}
	}
```

Add a `createProvider` function in `root.go`:

```go
// createProvider instantiates a provider by type name.
func createProvider(typeName, dataDir string, log *slog.Logger) (provider.Provider, error) {
	switch typeName {
	case "epel":
		return epel.NewEPELProvider(dataDir, log), nil
	case "ocp_binaries":
		return ocp.NewBinariesProvider(dataDir, log), nil
	case "rhcos":
		return ocp.NewRHCOSProvider(dataDir, log), nil
	case "container_images", "registry", "custom_files":
		return nil, fmt.Errorf("provider type %q is not yet implemented", typeName)
	default:
		return nil, fmt.Errorf("unknown provider type: %q", typeName)
	}
}
```

Add `"encoding/json"` to the import block.

After creating the SyncManager, set the provider factory:

```go
	// Initialize sync manager
	globalEngine = engine.NewSyncManager(globalRegistry, globalStore, client, globalCfg, logger)
	globalEngine.SetProviderFactory(func(typeName, dataDir string, log *slog.Logger) (provider.Provider, error) {
		return createProvider(typeName, dataDir, log)
	})
```

**Step 2: Verify build**

Run: `go build ./cmd/airgap/`
Expected: compiles

**Step 3: Verify all tests pass**

Run: `go test ./... -timeout 120s`
Expected: all PASS

**Step 4: Commit**

```bash
git add cmd/airgap/root.go
git commit -m "feat(root): load provider configs from DB, seed from YAML on first run"
```

---

### Task 7: Add provider config REST API routes to server

**Files:**
- Modify: `internal/server/server.go:110-116` (setupRoutes)

**Step 1: Add the routes**

Add these routes before the Transfer routes block in `setupRoutes()`:

```go
	// Provider config CRUD routes
	mux.HandleFunc("GET /api/providers/config", s.handleListProviderConfigs)
	mux.HandleFunc("POST /api/providers/config", s.handleCreateProviderConfig)
	mux.HandleFunc("PUT /api/providers/config/{name}", s.handleUpdateProviderConfig)
	mux.HandleFunc("DELETE /api/providers/config/{name}", s.handleDeleteProviderConfig)
	mux.HandleFunc("POST /api/providers/config/{name}/toggle", s.handleToggleProviderConfig)
```

**Step 2: Verify it doesn't build yet (handlers not implemented)**

Run: `go build ./internal/server/`
Expected: FAIL — undefined handler methods (this is expected)

**Step 3: Commit routes only (handlers come in next task)**

Don't commit yet — we'll implement the handlers and commit together in Task 8.

---

### Task 8: Implement provider config API handlers

**Files:**
- Create: `internal/server/provider_config_handlers.go`
- Modify: `internal/server/server.go` (from Task 7)

**Step 1: Write the failing test**

Create `internal/server/provider_config_handlers_test.go`:

```go
package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(dbPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
	}
	registry := provider.NewRegistry()
	client := download.NewClient(logger)
	eng := engine.NewSyncManager(registry, st, client, cfg, logger)

	return NewServer(eng, registry, st, cfg, logger)
}

func TestHandleListProviderConfigsEmpty(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/providers/config", nil)
	w := httptest.NewRecorder()
	srv.handleListProviderConfigs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var configs []providerConfigJSON
	json.NewDecoder(w.Body).Decode(&configs)
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}
}

func TestHandleCreateProviderConfig(t *testing.T) {
	srv := setupTestServer(t)

	body := `{"name":"epel","type":"epel","enabled":true,"config":{"repos":[]}}`
	req := httptest.NewRequest("POST", "/api/providers/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleCreateProviderConfig(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateProviderConfigDuplicate(t *testing.T) {
	srv := setupTestServer(t)

	body := `{"name":"epel","type":"epel","enabled":true,"config":{}}`
	req := httptest.NewRequest("POST", "/api/providers/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleCreateProviderConfig(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create failed: %d", w.Code)
	}

	// Duplicate
	req2 := httptest.NewRequest("POST", "/api/providers/config", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.handleCreateProviderConfig(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w2.Code)
	}
}

func TestHandleToggleProviderConfig(t *testing.T) {
	srv := setupTestServer(t)

	// Create first
	body := `{"name":"epel","type":"epel","enabled":true,"config":{}}`
	req := httptest.NewRequest("POST", "/api/providers/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleCreateProviderConfig(w, req)

	// Toggle
	toggleReq := httptest.NewRequest("POST", "/api/providers/config/epel/toggle", nil)
	toggleReq.SetPathValue("name", "epel")
	toggleW := httptest.NewRecorder()
	srv.handleToggleProviderConfig(toggleW, toggleReq)

	if toggleW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", toggleW.Code, toggleW.Body.String())
	}

	var result providerConfigJSON
	json.NewDecoder(toggleW.Body).Decode(&result)
	if result.Enabled {
		t.Error("expected enabled=false after toggle")
	}
}

func TestHandleDeleteProviderConfig(t *testing.T) {
	srv := setupTestServer(t)

	body := `{"name":"epel","type":"epel","enabled":true,"config":{}}`
	req := httptest.NewRequest("POST", "/api/providers/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleCreateProviderConfig(w, req)

	delReq := httptest.NewRequest("DELETE", "/api/providers/config/epel", nil)
	delReq.SetPathValue("name", "epel")
	delW := httptest.NewRecorder()
	srv.handleDeleteProviderConfig(delW, delReq)

	if delW.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", delW.Code)
	}
}

func TestHandleDeleteProviderConfigNotFound(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("DELETE", "/api/providers/config/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	srv.handleDeleteProviderConfig(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run "TestHandle.*ProviderConfig" -v`
Expected: FAIL — handler methods not defined

**Step 3: Implement the handlers**

Create `internal/server/provider_config_handlers.go`:

```go
package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/store"
)

// Valid provider types
var validProviderTypes = map[string]bool{
	"epel": true, "ocp_binaries": true, "rhcos": true,
	"container_images": true, "registry": true, "custom_files": true,
}

type providerConfigJSON struct {
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Enabled   bool                   `json:"enabled"`
	Config    map[string]interface{} `json:"config"`
	CreatedAt time.Time              `json:"created_at,omitempty"`
	UpdatedAt time.Time              `json:"updated_at,omitempty"`
}

type providerConfigRequest struct {
	Name    string                 `json:"name"`
	Type    string                 `json:"type"`
	Enabled bool                   `json:"enabled"`
	Config  map[string]interface{} `json:"config"`
}

func (s *Server) handleListProviderConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := s.store.ListProviderConfigs()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make([]providerConfigJSON, 0, len(configs))
	for _, pc := range configs {
		result = append(result, dbToJSON(pc))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleCreateProviderConfig(w http.ResponseWriter, r *http.Request) {
	var req providerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validProviderTypes[req.Type] {
		jsonError(w, http.StatusBadRequest, "invalid type: must be one of epel, ocp_binaries, rhcos, container_images, registry, custom_files")
		return
	}

	configBytes, _ := json.Marshal(req.Config)

	pc := &store.ProviderConfig{
		Name:       req.Name,
		Type:       req.Type,
		Enabled:    req.Enabled,
		ConfigJSON: string(configBytes),
	}

	if err := s.store.CreateProviderConfig(pc); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			jsonError(w, http.StatusConflict, "provider with name '"+req.Name+"' already exists")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Hot-reload providers
	s.reloadProviders()

	got, _ := s.store.GetProviderConfig(req.Name)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dbToJSON(*got))
}

func (s *Server) handleUpdateProviderConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "provider name required")
		return
	}

	existing, err := s.store.GetProviderConfig(name)
	if err != nil {
		jsonError(w, http.StatusNotFound, "provider not found: "+name)
		return
	}

	var req providerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Type != "" && !validProviderTypes[req.Type] {
		jsonError(w, http.StatusBadRequest, "invalid type")
		return
	}

	if req.Type != "" {
		existing.Type = req.Type
	}
	existing.Enabled = req.Enabled
	if req.Config != nil {
		configBytes, _ := json.Marshal(req.Config)
		existing.ConfigJSON = string(configBytes)
	}

	if err := s.store.UpdateProviderConfig(existing); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.reloadProviders()

	got, _ := s.store.GetProviderConfig(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dbToJSON(*got))
}

func (s *Server) handleDeleteProviderConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "provider name required")
		return
	}

	if err := s.store.DeleteProviderConfig(name); err != nil {
		jsonError(w, http.StatusNotFound, "provider not found: "+name)
		return
	}

	s.reloadProviders()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleToggleProviderConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "provider name required")
		return
	}

	if err := s.store.ToggleProviderConfig(name); err != nil {
		jsonError(w, http.StatusNotFound, "provider not found: "+name)
		return
	}

	s.reloadProviders()

	got, _ := s.store.GetProviderConfig(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dbToJSON(*got))
}

// reloadProviders reads configs from DB and calls ReconfigureProviders.
func (s *Server) reloadProviders() {
	configs, err := s.store.ListProviderConfigs()
	if err != nil {
		s.logger.Error("failed to list provider configs for reload", "error", err)
		return
	}
	if err := s.engine.ReconfigureProviders(configs); err != nil {
		s.logger.Error("failed to reconfigure providers", "error", err)
	}
}

// dbToJSON converts a store.ProviderConfig to the JSON response shape.
func dbToJSON(pc store.ProviderConfig) providerConfigJSON {
	var cfg map[string]interface{}
	json.Unmarshal([]byte(pc.ConfigJSON), &cfg)
	if cfg == nil {
		cfg = make(map[string]interface{})
	}
	return providerConfigJSON{
		Name:      pc.Name,
		Type:      pc.Type,
		Enabled:   pc.Enabled,
		Config:    cfg,
		CreatedAt: pc.CreatedAt,
		UpdatedAt: pc.UpdatedAt,
	}
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -run "TestHandle.*ProviderConfig" -v`
Expected: all PASS

**Step 5: Commit**

```bash
git add internal/server/server.go internal/server/provider_config_handlers.go internal/server/provider_config_handlers_test.go
git commit -m "feat(server): add provider config CRUD API handlers"
```

---

### Task 9: Enhance providers.html template with CRUD UI

**Files:**
- Modify: `internal/server/templates/providers.html`
- Modify: `internal/server/handlers.go` (update handleProviders to pass config data)

**Step 1: Update handleProviders to pass provider configs**

In `internal/server/handlers.go`, update `handleProviders`:

```go
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	providerNames := s.registry.Names()
	statuses := s.engine.Status()

	var providerConfigs []providerConfigJSON
	if s.store != nil {
		configs, err := s.store.ListProviderConfigs()
		if err != nil {
			s.logger.Warn("failed to list provider configs", "error", err)
		} else {
			for _, pc := range configs {
				providerConfigs = append(providerConfigs, dbToJSON(pc))
			}
		}
	}

	data := map[string]interface{}{
		"Title":           "Providers",
		"Providers":       providerNames,
		"Statuses":        statuses,
		"ProviderConfigs": providerConfigs,
	}

	if err := s.templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		s.logger.Error("failed to render providers", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
```

Note: `dbToJSON` is defined in `provider_config_handlers.go` and accessible since both files are in the `server` package.

**Step 2: Rewrite the providers.html template**

Replace the full contents of `internal/server/templates/providers.html` with an enhanced version that has:
- A table of all configured providers with toggle switches, edit/delete buttons
- An "Add Provider" button that shows an Alpine.js form
- Edit forms that appear inline via Alpine.js
- Type-specific config fields using `x-show` directives

The template should use HTMX for toggle/delete (posting to the API) and Alpine.js for the add/edit form state management. Since the providers page re-renders on navigation, the template reads from `{{.ProviderConfigs}}` for the config data and `{{.Statuses}}` for runtime stats.

This is a large template — write it using the `frontend-design` skill patterns. Key sections:

1. Provider table with columns: Name, Type, Enabled (toggle), Files, Size, Actions
2. Add Provider modal/form with type selector and dynamic fields
3. Edit inline form

**Step 3: Verify build**

Run: `go build ./...`
Expected: compiles

**Step 4: Commit**

```bash
git add internal/server/handlers.go internal/server/templates/providers.html
git commit -m "feat(ui): enhance providers page with CRUD controls"
```

---

### Task 10: Final verification

**Step 1: Run all tests**

Run: `go test ./... -timeout 120s`
Expected: all PASS

**Step 2: Run race detector**

Run: `go test -race ./internal/engine/ ./internal/store/ ./internal/server/ -timeout 120s`
Expected: no races

**Step 3: Build binary**

Run: `go build -o bin/airgap ./cmd/airgap/`
Expected: builds

**Step 4: Verify import flag still works**

Run: `./bin/airgap import --help`
Expected: shows `--skip-validated`

**Step 5: Commit any remaining changes**

```bash
git add -A
git commit -m "feat: provider management — complete CRUD with hot-reload and UI"
```
