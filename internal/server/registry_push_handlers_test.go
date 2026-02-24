package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseRegistryPushRequestJSON(t *testing.T) {
	body := `{"source_provider":"containers-a","target_provider":"registry-a","dry_run":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/registry/push", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	got, err := parseRegistryPushRequest(req)
	if err != nil {
		t.Fatalf("parseRegistryPushRequest returned error: %v", err)
	}
	if got.SourceProvider != "containers-a" || got.TargetProvider != "registry-a" || !got.DryRun {
		t.Fatalf("unexpected parse result: %+v", got)
	}
}

func TestParseRegistryPushRequestForm(t *testing.T) {
	form := url.Values{}
	form.Set("source_provider", "containers-a")
	form.Set("target_provider", "registry-a")
	form.Set("dry_run", "on")

	req := httptest.NewRequest(http.MethodPost, "/api/registry/push", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	got, err := parseRegistryPushRequest(req)
	if err != nil {
		t.Fatalf("parseRegistryPushRequest returned error: %v", err)
	}
	if got.SourceProvider != "containers-a" || got.TargetProvider != "registry-a" || !got.DryRun {
		t.Fatalf("unexpected parse result: %+v", got)
	}
}

func TestHandleAPIRegistryPushMissingProvidersJSON(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/registry/push", bytes.NewBufferString(`{"source_provider":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleAPIRegistryPush(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var payload map[string]string
	if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["error"] == "" {
		t.Fatalf("expected error message, got payload: %#v", payload)
	}
}

func TestHandleAPIRegistryPushConflictHTMX(t *testing.T) {
	srv := setupTestServer(t)
	srv.syncMu.Lock()
	srv.syncRunning = true
	srv.syncMu.Unlock()

	form := url.Values{}
	form.Set("source_provider", "containers-a")
	form.Set("target_provider", "registry-a")
	req := httptest.NewRequest(http.MethodPost, "/api/registry/push", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	srv.handleAPIRegistryPush(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 HTML fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "already running") {
		t.Fatalf("expected conflict fragment body, got %q", body)
	}
}
