package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTemplateIsolationEndToEnd(t *testing.T) {
	srv := setupTestServer(t)

	if err := srv.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates failed: %v", err)
	}

	mux := srv.setupRoutes()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	tests := []struct {
		path           string
		mustContain    string
		mustNotContain string
	}{
		{
			path:           "/dashboard",
			mustContain:    "System Overview",
			mustNotContain: "Start Export",
		},
		{
			path:           "/transfer",
			mustContain:    "Start Export",
			mustNotContain: "System Overview",
		},
		{
			path:           "/providers",
			mustContain:    "providerManager",
			mustNotContain: "Start Export",
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s failed: %v", tt.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s returned %d", tt.path, resp.StatusCode)
			}

			raw, _ := io.ReadAll(resp.Body)
			body := string(raw)

			t.Logf("GET %s response length: %d bytes", tt.path, len(body))

			if !strings.Contains(body, tt.mustContain) {
				// Show what we got instead
				t.Errorf("GET %s: expected body to contain %q\nFirst 800 chars of body:\n%s",
					tt.path, tt.mustContain, body[:min(800, len(body))])
			}
			if strings.Contains(body, tt.mustNotContain) {
				t.Errorf("GET %s: body should NOT contain %q but it does", tt.path, tt.mustNotContain)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
