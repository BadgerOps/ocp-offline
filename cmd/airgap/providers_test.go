package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

func TestProvidersListRun_Empty(t *testing.T) {
	st := newTestStore(t)

	origStore := globalStore
	origRegistry := globalRegistry
	globalStore = st
	globalRegistry = provider.NewRegistry()
	t.Cleanup(func() {
		globalStore = origStore
		globalRegistry = origRegistry
	})

	out := captureStdout(t, func() {
		if err := providersListRun(nil, nil); err != nil {
			t.Fatalf("providersListRun returned error: %v", err)
		}
	})

	if !strings.Contains(out, "No providers configured.") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestProvidersListRun_ShowsConfiguredProviders(t *testing.T) {
	st := newTestStore(t)
	mustCreateProviderConfig(t, st, "rhcos-main", "rhcos", true)
	mustCreateProviderConfig(t, st, "container-set", "container_images", false)

	reg := provider.NewRegistry()
	reg.RegisterAs("rhcos-main", &stubProvider{name: "rhcos-main"})

	origStore := globalStore
	origRegistry := globalRegistry
	globalStore = st
	globalRegistry = reg
	t.Cleanup(func() {
		globalStore = origStore
		globalRegistry = origRegistry
	})

	out := captureStdout(t, func() {
		if err := providersListRun(nil, nil); err != nil {
			t.Fatalf("providersListRun returned error: %v", err)
		}
	})

	if !strings.Contains(out, "rhcos-main") || !strings.Contains(out, "container-set") {
		t.Fatalf("expected provider names in output, got: %s", out)
	}
	if !strings.Contains(out, "rhcos") || !strings.Contains(out, "container_images") {
		t.Fatalf("expected provider types in output, got: %s", out)
	}
	if !strings.Contains(out, "yes") || !strings.Contains(out, "no") {
		t.Fatalf("expected enabled/loaded markers in output, got: %s", out)
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(":memory:", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("creating test store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustCreateProviderConfig(t *testing.T, st *store.Store, name, typ string, enabled bool) {
	t.Helper()
	pc := &store.ProviderConfig{
		Name:       name,
		Type:       typ,
		Enabled:    enabled,
		ConfigJSON: "{}",
	}
	if err := st.CreateProviderConfig(pc); err != nil {
		t.Fatalf("creating provider config %s: %v", name, err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	_ = r.Close()
	return string(data)
}

type stubProvider struct {
	name string
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) SetName(name string) { s.name = name }

func (s *stubProvider) Type() string { return "stub" }

func (s *stubProvider) Configure(cfg provider.ProviderConfig) error { return nil }

func (s *stubProvider) Plan(ctx context.Context) (*provider.SyncPlan, error) { return nil, nil }

func (s *stubProvider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
	return nil, nil
}

func (s *stubProvider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	return nil, nil
}
