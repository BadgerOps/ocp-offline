package mirror

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryEPELMirrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if _, err := w.Write([]byte(sampleMetalinkXML)); err != nil {
			t.Fatalf("failed to write test response: %v", err)
		}
	}))
	defer srv.Close()

	logger := slog.Default()
	d := NewDiscovery(logger)
	// Override metalink base URL to point at our test server; use %d and %s placeholders
	d.metalinkBaseURL = srv.URL + "?repo=epel-%d&arch=%s"

	mirrors, err := d.EPELMirrors(context.Background(), 9, "x86_64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mirrors) != 3 {
		t.Fatalf("expected 3 mirrors, got %d", len(mirrors))
	}

	// Verify sorted by preference descending
	if mirrors[0].Preference != 100 {
		t.Errorf("expected first mirror preference 100, got %d", mirrors[0].Preference)
	}
}

func TestDiscoveryCaching(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		if _, err := w.Write([]byte(sampleMetalinkXML)); err != nil {
			t.Fatalf("failed to write test response: %v", err)
		}
	}))
	defer srv.Close()

	logger := slog.Default()
	d := NewDiscovery(logger)
	d.metalinkBaseURL = srv.URL + "?repo=epel-%d&arch=%s"

	ctx := context.Background()

	_, err := d.EPELMirrors(ctx, 9, "x86_64")
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	_, err = d.EPELMirrors(ctx, 9, "x86_64")
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 upstream call (cached), got %d", callCount.Load())
	}
}

func TestDiscoveryCacheExpiry(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		if _, err := w.Write([]byte(sampleMetalinkXML)); err != nil {
			t.Fatalf("failed to write test response: %v", err)
		}
	}))
	defer srv.Close()

	logger := slog.Default()
	d := NewDiscovery(logger)
	d.metalinkBaseURL = srv.URL + "?repo=epel-%d&arch=%s"
	d.cacheTTL = 1 * time.Millisecond

	ctx := context.Background()

	_, err := d.EPELMirrors(ctx, 9, "x86_64")
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	_, err = d.EPELMirrors(ctx, 9, "x86_64")
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 upstream calls (cache expired), got %d", callCount.Load())
	}
}

func TestDiscoveryOCPVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(ocpTestHTML)); err != nil {
			t.Fatalf("failed to write test response: %v", err)
		}
	}))
	defer srv.Close()

	logger := slog.Default()
	d := NewDiscovery(logger)
	d.ocpBaseURL = srv.URL

	versions, err := d.OCPVersions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(versions) == 0 {
		t.Fatal("expected non-empty versions")
	}

	// Verify we got channels and releases
	var hasChannel, hasRelease bool
	for _, v := range versions {
		if v.Channel == "release" {
			hasRelease = true
		} else {
			hasChannel = true
		}
	}
	if !hasChannel {
		t.Error("expected at least one channel version")
	}
	if !hasRelease {
		t.Error("expected at least one release version")
	}
}
