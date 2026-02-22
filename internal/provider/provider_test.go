package provider

import (
	"context"
	"slices"
	"testing"
)

// mockProvider implements the Provider interface for testing
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Type() string {
	return "generic"
}

func (m *mockProvider) Configure(cfg ProviderConfig) error {
	return nil
}

func (m *mockProvider) Plan(ctx context.Context) (*SyncPlan, error) {
	return nil, nil
}

func (m *mockProvider) Sync(ctx context.Context, plan *SyncPlan, opts SyncOptions) (*SyncReport, error) {
	return nil, nil
}

func (m *mockProvider) Validate(ctx context.Context) (*ValidationReport, error) {
	return nil, nil
}

// TestNewRegistry tests that NewRegistry creates an empty registry
func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()

	if registry == nil {
		t.Fatal("NewRegistry() returned nil")
	}

	if registry.providers == nil {
		t.Fatal("NewRegistry() created registry with nil providers map")
	}

	allProviders := registry.All()
	if len(allProviders) != 0 {
		t.Fatalf("NewRegistry() should create empty registry, got %d providers", len(allProviders))
	}

	names := registry.Names()
	if len(names) != 0 {
		t.Fatalf("NewRegistry() should have no names, got %d", len(names))
	}
}

// TestRegisterAndGet tests registering a provider and retrieving it
func TestRegisterAndGet(t *testing.T) {
	registry := NewRegistry()
	provider := &mockProvider{name: "test-provider"}

	registry.Register(provider)

	retrieved, ok := registry.Get("test-provider")
	if !ok {
		t.Fatal("Get() returned false for registered provider")
	}

	if retrieved != provider {
		t.Fatalf("Get() returned different provider instance")
	}

	if retrieved.Name() != "test-provider" {
		t.Fatalf("retrieved provider has wrong name: got %s, want test-provider", retrieved.Name())
	}
}

// TestGetMissingProvider tests that Get returns false for non-existent provider
func TestGetMissingProvider(t *testing.T) {
	registry := NewRegistry()

	provider, ok := registry.Get("nonexistent")
	if ok {
		t.Fatal("Get() returned true for nonexistent provider")
	}

	if provider != nil {
		t.Fatal("Get() returned non-nil provider for nonexistent name")
	}
}

// TestAll returns all registered providers
func TestAll(t *testing.T) {
	registry := NewRegistry()

	provider1 := &mockProvider{name: "provider1"}
	provider2 := &mockProvider{name: "provider2"}
	provider3 := &mockProvider{name: "provider3"}

	registry.Register(provider1)
	registry.Register(provider2)
	registry.Register(provider3)

	allProviders := registry.All()

	if len(allProviders) != 3 {
		t.Fatalf("All() returned %d providers, want 3", len(allProviders))
	}

	if allProviders["provider1"] != provider1 {
		t.Fatal("All() missing provider1")
	}

	if allProviders["provider2"] != provider2 {
		t.Fatal("All() missing provider2")
	}

	if allProviders["provider3"] != provider3 {
		t.Fatal("All() missing provider3")
	}
}

// TestNames returns all registered provider names
func TestNames(t *testing.T) {
	registry := NewRegistry()

	provider1 := &mockProvider{name: "alpha"}
	provider2 := &mockProvider{name: "beta"}
	provider3 := &mockProvider{name: "gamma"}

	registry.Register(provider1)
	registry.Register(provider2)
	registry.Register(provider3)

	names := registry.Names()

	if len(names) != 3 {
		t.Fatalf("Names() returned %d names, want 3", len(names))
	}

	// Names order is not guaranteed due to map iteration, so sort for comparison
	slices.Sort(names)
	expectedNames := []string{"alpha", "beta", "gamma"}
	slices.Sort(expectedNames)

	for i, name := range names {
		if name != expectedNames[i] {
			t.Fatalf("Names() returned unexpected name at index %d: got %s, want %s", i, name, expectedNames[i])
		}
	}
}

// TestMultipleProviders verifies multiple providers can be registered and retrieved
func TestMultipleProviders(t *testing.T) {
	registry := NewRegistry()

	providers := []*mockProvider{
		{name: "epel"},
		{name: "ocp-binaries"},
		{name: "openshift-images"},
		{name: "operator-catalogs"},
		{name: "helm-charts"},
	}

	for _, p := range providers {
		registry.Register(p)
	}

	// Verify each provider can be retrieved
	for _, expected := range providers {
		retrieved, ok := registry.Get(expected.name)
		if !ok {
			t.Fatalf("Get() failed for provider %s", expected.name)
		}

		if retrieved != expected {
			t.Fatalf("Get() returned wrong instance for %s", expected.name)
		}
	}

	// Verify All() returns correct count
	all := registry.All()
	if len(all) != len(providers) {
		t.Fatalf("All() returned %d providers, want %d", len(all), len(providers))
	}

	// Verify Names() has all names
	names := registry.Names()
	if len(names) != len(providers) {
		t.Fatalf("Names() returned %d names, want %d", len(names), len(providers))
	}

	for _, p := range providers {
		if !slices.Contains(names, p.name) {
			t.Fatalf("Names() missing provider %s", p.name)
		}
	}
}

// TestRegisterDuplicateName tests that registering with duplicate name overwrites
func TestRegisterDuplicateName(t *testing.T) {
	registry := NewRegistry()

	provider1 := &mockProvider{name: "myapp"}
	provider2 := &mockProvider{name: "myapp"}

	registry.Register(provider1)
	registry.Register(provider2)

	retrieved, ok := registry.Get("myapp")
	if !ok {
		t.Fatal("Get() failed after overwriting provider")
	}

	if retrieved != provider2 {
		t.Fatal("Get() returned first provider instead of overwritten one")
	}

	// Verify we only have one provider with this name
	all := registry.All()
	if len(all) != 1 {
		t.Fatalf("Registry should have 1 provider after overwrite, got %d", len(all))
	}
}

// TestActionTypeConstants verifies ActionType constants exist and have correct values
func TestActionTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		actual   ActionType
		expected string
	}{
		{
			name:     "ActionDownload",
			actual:   ActionDownload,
			expected: "download",
		},
		{
			name:     "ActionDelete",
			actual:   ActionDelete,
			expected: "delete",
		},
		{
			name:     "ActionSkip",
			actual:   ActionSkip,
			expected: "skip",
		},
		{
			name:     "ActionUpdate",
			actual:   ActionUpdate,
			expected: "update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.actual) != tt.expected {
				t.Fatalf("ActionType constant has wrong value: got %s, want %s", tt.actual, tt.expected)
			}
		})
	}
}

// TestRegistryWithEmptyProviderName tests behavior with empty provider name
func TestRegistryWithEmptyProviderName(t *testing.T) {
	registry := NewRegistry()
	provider := &mockProvider{name: ""}

	registry.Register(provider)

	retrieved, ok := registry.Get("")
	if !ok {
		t.Fatal("Get() returned false for empty-named provider")
	}

	if retrieved != provider {
		t.Fatal("Get() returned wrong provider for empty name")
	}
}

// TestRegistryIndependence verifies multiple registries are independent
func TestRegistryIndependence(t *testing.T) {
	registry1 := NewRegistry()
	registry2 := NewRegistry()

	provider1 := &mockProvider{name: "provider"}
	provider2 := &mockProvider{name: "provider"}

	registry1.Register(provider1)
	registry2.Register(provider2)

	retrieved1, ok1 := registry1.Get("provider")
	retrieved2, ok2 := registry2.Get("provider")

	if !ok1 || !ok2 {
		t.Fatal("Get() failed on independent registries")
	}

	if retrieved1 != provider1 {
		t.Fatal("registry1 returned wrong provider")
	}

	if retrieved2 != provider2 {
		t.Fatal("registry2 returned wrong provider")
	}

	// Verify they returned different instances
	if retrieved1 == retrieved2 {
		t.Fatal("registries should be independent but returned same provider instance")
	}
}

// BenchmarkRegister benchmarks provider registration
func BenchmarkRegister(b *testing.B) {
	registry := NewRegistry()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		provider := &mockProvider{name: "test"}
		registry.Register(provider)
	}
}

// BenchmarkGet benchmarks provider retrieval
func BenchmarkGet(b *testing.B) {
	registry := NewRegistry()
	provider := &mockProvider{name: "test"}
	registry.Register(provider)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		registry.Get("test")
	}
}

// BenchmarkAll benchmarks retrieving all providers
func BenchmarkAll(b *testing.B) {
	registry := NewRegistry()
	for i := 0; i < 100; i++ {
		provider := &mockProvider{name: "provider"}
		registry.Register(provider)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		registry.All()
	}
}

// BenchmarkNames benchmarks retrieving all provider names
func BenchmarkNames(b *testing.B) {
	registry := NewRegistry()
	for i := 0; i < 100; i++ {
		provider := &mockProvider{name: "provider"}
		registry.Register(provider)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		registry.Names()
	}
}
