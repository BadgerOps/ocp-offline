package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BadgerOps/airgap/internal/store"
)

func addFailedRecord(t *testing.T, srv *Server, provider, filePath string) *store.FailedFileRecord {
	t.Helper()

	now := time.Now()
	rec := &store.FailedFileRecord{
		Provider:     provider,
		FilePath:     filePath,
		URL:          "https://mirror.example.com/" + filePath,
		Error:        "failed to download",
		RetryCount:   1,
		FirstFailure: now,
		LastFailure:  now,
		Resolved:     false,
	}
	if err := srv.store.AddFailedFile(rec); err != nil {
		t.Fatalf("AddFailedFile() failed: %v", err)
	}
	return rec
}

func TestHandleAPISyncFailureResolve(t *testing.T) {
	srv := setupTestServer(t)

	rec := addFailedRecord(t, srv, "epel", "9/Packages/example.rpm")

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/failures/"+fmt.Sprintf("%d", rec.ID), nil)
	req.SetPathValue("id", fmt.Sprintf("%d", rec.ID))
	w := httptest.NewRecorder()
	srv.handleAPISyncFailureResolve(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	remaining, err := srv.store.ListFailedFiles("epel")
	if err != nil {
		t.Fatalf("ListFailedFiles() failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 unresolved failed files after clear, got %d", len(remaining))
	}
}

func TestHandleAPISyncFailureResolveNotFound(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/failures/99999", nil)
	req.SetPathValue("id", "99999")
	w := httptest.NewRecorder()
	srv.handleAPISyncFailureResolve(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAPISyncFailureResolveInvalidID(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/sync/failures/not-a-number", nil)
	req.SetPathValue("id", "not-a-number")
	w := httptest.NewRecorder()
	srv.handleAPISyncFailureResolve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAPISyncFailuresResolveSelected(t *testing.T) {
	srv := setupTestServer(t)

	r1 := addFailedRecord(t, srv, "epel", "9/Packages/a.rpm")
	_ = addFailedRecord(t, srv, "epel", "9/Packages/b.rpm")
	r3 := addFailedRecord(t, srv, "epel", "9/Packages/c.rpm")
	_ = addFailedRecord(t, srv, "rhcos", "4.16/image.raw.gz")

	body, err := json.Marshal(map[string]interface{}{
		"provider": "epel",
		"ids":      []int64{r1.ID, r3.ID},
		"all":      false,
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/failures/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleAPISyncFailuresResolve(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	epelRemaining, err := srv.store.ListFailedFiles("epel")
	if err != nil {
		t.Fatalf("ListFailedFiles(epel) failed: %v", err)
	}
	if len(epelRemaining) != 1 {
		t.Fatalf("expected 1 unresolved epel file, got %d", len(epelRemaining))
	}

	rhcosRemaining, err := srv.store.ListFailedFiles("rhcos")
	if err != nil {
		t.Fatalf("ListFailedFiles(rhcos) failed: %v", err)
	}
	if len(rhcosRemaining) != 1 {
		t.Fatalf("expected rhcos failed file to be unaffected, got %d", len(rhcosRemaining))
	}
}

func TestHandleAPISyncFailuresResolveAll(t *testing.T) {
	srv := setupTestServer(t)

	_ = addFailedRecord(t, srv, "epel", "9/Packages/a.rpm")
	_ = addFailedRecord(t, srv, "epel", "9/Packages/b.rpm")

	body, err := json.Marshal(map[string]interface{}{
		"provider": "epel",
		"all":      true,
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/failures/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleAPISyncFailuresResolve(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	remaining, err := srv.store.ListFailedFiles("epel")
	if err != nil {
		t.Fatalf("ListFailedFiles() failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected all failed files to be resolved, got %d", len(remaining))
	}
}

func TestHandleAPISyncFailuresResolveInvalidRequest(t *testing.T) {
	srv := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/failures/resolve", bytes.NewBufferString(`{"provider":"epel","all":false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleAPISyncFailuresResolve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
