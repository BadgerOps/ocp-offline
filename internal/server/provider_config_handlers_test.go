package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/engine"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(":memory:", logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("failed to close store: %v", err)
		}
	})

	cfg := &config.Config{
		Server:    config.ServerConfig{DataDir: t.TempDir()},
		Providers: make(map[string]config.ProviderConfig),
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
	if err := json.NewDecoder(w.Body).Decode(&configs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
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
	if err := json.NewDecoder(toggleW.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
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

func TestHandleUpdateProviderConfig(t *testing.T) {
	srv := setupTestServer(t)

	// Create first
	body := `{"name":"epel","type":"epel","enabled":true,"config":{"key":"old"}}`
	req := httptest.NewRequest("POST", "/api/providers/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleCreateProviderConfig(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create failed: %d", w.Code)
	}

	// Update
	updateBody := `{"type":"epel","enabled":false,"config":{"key":"new"}}`
	updateReq := httptest.NewRequest("PUT", "/api/providers/config/epel", bytes.NewBufferString(updateBody))
	updateReq.SetPathValue("name", "epel")
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	srv.handleUpdateProviderConfig(updateW, updateReq)

	if updateW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateW.Code, updateW.Body.String())
	}

	var result providerConfigJSON
	if err := json.NewDecoder(updateW.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.Enabled {
		t.Error("expected enabled=false after update")
	}
	if result.Config["key"] != "new" {
		t.Errorf("expected config key=new, got %v", result.Config["key"])
	}
}
