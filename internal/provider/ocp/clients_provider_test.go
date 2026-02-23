package ocp

import (
	"context"
	"log/slog"
	"testing"

	"github.com/BadgerOps/airgap/internal/provider"
)

func TestClientsProviderType(t *testing.T) {
	p := NewClientsProvider("/tmp/test", slog.Default())
	if p.Type() != "ocp_clients" {
		t.Errorf("expected type 'ocp_clients', got %q", p.Type())
	}
}

func TestClientsProviderName(t *testing.T) {
	p := NewClientsProvider("/tmp/test", slog.Default())
	if p.Name() != "ocp_clients" {
		t.Errorf("expected default name 'ocp_clients', got %q", p.Name())
	}

	p.SetName("my-ocp-clients")
	if p.Name() != "my-ocp-clients" {
		t.Errorf("expected name 'my-ocp-clients', got %q", p.Name())
	}
}

func TestClientsProviderConfigure(t *testing.T) {
	p := NewClientsProvider("/tmp/test", slog.Default())

	// Valid config with channels
	err := p.Configure(provider.ProviderConfig{
		"channels":   []interface{}{"stable-4.21"},
		"platforms":  []interface{}{"linux", "linux-arm64"},
		"output_dir": "ocp-clients",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.cfg == nil {
		t.Fatal("config should not be nil after Configure")
	}
	if len(p.cfg.Channels) != 1 || p.cfg.Channels[0] != "stable-4.21" {
		t.Errorf("unexpected channels: %v", p.cfg.Channels)
	}
	if len(p.cfg.Platforms) != 2 {
		t.Errorf("expected 2 platforms, got %d", len(p.cfg.Platforms))
	}
}

func TestClientsProviderConfigureDefaults(t *testing.T) {
	p := NewClientsProvider("/tmp/test", slog.Default())

	// Config with no platforms or output_dir — should get defaults
	err := p.Configure(provider.ProviderConfig{
		"channels": []interface{}{"stable-4.21"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.cfg.OutputDir != "ocp-clients" {
		t.Errorf("expected default output_dir 'ocp-clients', got %q", p.cfg.OutputDir)
	}
	if len(p.cfg.Platforms) != 2 {
		t.Errorf("expected default 2 platforms (linux, linux-arm64), got %d: %v", len(p.cfg.Platforms), p.cfg.Platforms)
	}
}

func TestClientsProviderPlanNotConfigured(t *testing.T) {
	p := NewClientsProvider("/tmp/test", slog.Default())
	_, err := p.Plan(context.Background())
	if err == nil {
		t.Error("expected error when not configured")
	}
}
