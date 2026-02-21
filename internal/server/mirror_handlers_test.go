package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BadgerOps/airgap/internal/mirror"
)

func TestHandleEPELVersions(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/mirrors/epel/versions", nil)
	w := httptest.NewRecorder()
	srv.handleEPELVersions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var versions []mirror.EPELVersionInfo
	if err := json.NewDecoder(w.Body).Decode(&versions); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(versions) == 0 {
		t.Error("expected non-empty EPEL versions list")
	}
}

func TestHandleEPELMirrorsMissingParams(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/mirrors/epel", nil)
	w := httptest.NewRecorder()
	srv.handleEPELMirrors(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleEPELMirrorsInvalidVersion(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/mirrors/epel?version=abc&arch=x86_64", nil)
	w := httptest.NewRecorder()
	srv.handleEPELMirrors(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSpeedTestMissingURLs(t *testing.T) {
	srv := setupTestServer(t)

	body := `{"urls":[]}`
	req := httptest.NewRequest("POST", "/api/mirrors/speedtest", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleSpeedTest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSpeedTestValidRequest(t *testing.T) {
	// Create a test HTTP server that returns some data
	testSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "test data")
	}))
	t.Cleanup(testSrv.Close)

	srv := setupTestServer(t)

	body, _ := json.Marshal(map[string]interface{}{
		"urls":  []string{testSrv.URL},
		"top_n": 1,
	})
	req := httptest.NewRequest("POST", "/api/mirrors/speedtest", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleSpeedTest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var results []mirror.SpeedResult
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}
