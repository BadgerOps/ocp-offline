package mirror

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSpeedTest(t *testing.T) {
	// Create fast server (responds immediately)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fast response body with some content for throughput measurement"))
	}))
	defer fast.Close()

	// Create slow server (200ms delay)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("slow response body with some content for throughput measurement"))
	}))
	defer slow.Close()

	d := NewDiscovery(slog.Default())
	urls := []string{slow.URL, fast.URL}

	results := d.SpeedTest(context.Background(), urls, 2)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Results should be sorted by throughput descending (fast first)
	if results[0].URL != fast.URL {
		t.Errorf("expected fast server first, got %s", results[0].URL)
	}
	if results[1].URL != slow.URL {
		t.Errorf("expected slow server second, got %s", results[1].URL)
	}

	// Both should have non-negative latency (fast local servers may have sub-millisecond latency)
	for i, r := range results {
		if r.LatencyMs < 0 {
			t.Errorf("result[%d] LatencyMs should be non-negative, got %d", i, r.LatencyMs)
		}
		if r.Error != "" {
			t.Errorf("result[%d] unexpected error: %s", i, r.Error)
		}
	}

	// Fast server should have higher throughput than slow
	if results[0].ThroughputKBps <= results[1].ThroughputKBps {
		t.Errorf("fast server throughput (%f) should be greater than slow (%f)",
			results[0].ThroughputKBps, results[1].ThroughputKBps)
	}
}

func TestSpeedTestWithErrors(t *testing.T) {
	// One good server
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("good response"))
	}))
	defer good.Close()

	// One unreachable URL (RFC 5737 TEST-NET, guaranteed unreachable)
	badURL := "http://192.0.2.1:1"

	d := NewDiscovery(slog.Default())
	urls := []string{good.URL, badURL}

	// Use a client with a short timeout so the unreachable URL fails quickly.
	d.client.Timeout = 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	results := d.SpeedTest(ctx, urls, 2)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// At least one should have an error
	hasError := false
	hasGood := false
	for _, r := range results {
		if r.Error != "" {
			hasError = true
		}
		if r.URL == good.URL && r.Error == "" {
			hasGood = true
		}
	}

	if !hasError {
		t.Error("expected at least one result with an error")
	}
	if !hasGood {
		t.Error("expected the good server to succeed")
	}

	// Good result should be sorted before errored result (throughput descending, errors last)
	if results[0].URL != good.URL {
		t.Errorf("expected good server first, got %s", results[0].URL)
	}
}
