package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BadgerOps/airgap/internal/store"
)

func TestHandleAPISyncFailureResolve(t *testing.T) {
	srv := setupTestServer(t)

	now := time.Now()
	rec := &store.FailedFileRecord{
		Provider:     "epel",
		FilePath:     "9/Packages/example.rpm",
		URL:          "https://mirror.example.com/example.rpm",
		Error:        "failed to download",
		RetryCount:   1,
		FirstFailure: now,
		LastFailure:  now,
		Resolved:     false,
	}
	if err := srv.store.AddFailedFile(rec); err != nil {
		t.Fatalf("AddFailedFile() failed: %v", err)
	}

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
