package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteSyncFragmentEscapesMessage(t *testing.T) {
	w := httptest.NewRecorder()
	writeSyncFragment(w, false, `<script>alert('x')</script>`)

	body := w.Body.String()
	if strings.Contains(body, "<script>") {
		t.Fatalf("expected script tag to be escaped, got: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert") {
		t.Fatalf("expected escaped script in body, got: %s", body)
	}
}

func TestWriteTransferFragmentEscapesMessage(t *testing.T) {
	w := httptest.NewRecorder()
	writeTransferFragment(w, false, `<img src=x onerror=alert(1)>`)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status %d, got %d", http.StatusUnprocessableEntity, w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "<img") {
		t.Fatalf("expected img tag to be escaped, got: %s", body)
	}
	if !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatalf("expected escaped img payload in body, got: %s", body)
	}
}
