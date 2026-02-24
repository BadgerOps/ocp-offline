package containerimages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BadgerOps/airgap/internal/provider"
)

func TestProviderTypeAndName(t *testing.T) {
	p := NewProvider(t.TempDir(), slog.Default())
	if p.Type() != "container_images" {
		t.Fatalf("expected type container_images, got %q", p.Type())
	}
	if p.Name() != "container_images" {
		t.Fatalf("expected default name container_images, got %q", p.Name())
	}
	p.SetName("my-images")
	if p.Name() != "my-images" {
		t.Fatalf("expected overridden name my-images, got %q", p.Name())
	}
}

func TestConfigureRejectsInvalidImageReference(t *testing.T) {
	p := NewProvider(t.TempDir(), slog.Default())
	err := p.Configure(provider.ProviderConfig{
		"images": []interface{}{
			"docker://",
		},
	})
	if err == nil {
		t.Fatal("expected invalid image reference error")
	}
}

func TestParseImageReferenceDockerHubNormalization(t *testing.T) {
	ref, err := parseImageReference("docker://docker.io/alpine:latest")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ref.Repository != "library/alpine" {
		t.Fatalf("expected repository library/alpine, got %q", ref.Repository)
	}
	if ref.EndpointHost != "registry-1.docker.io" {
		t.Fatalf("expected endpoint registry-1.docker.io, got %q", ref.EndpointHost)
	}
}

func TestPlanResolvesManifestsAndBlobs(t *testing.T) {
	configBlob := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":[]}}`)
	layerBlob := []byte("layer")

	configDigest := digestOf(configBlob)
	layerDigest := digestOf(layerBlob)

	childManifest := map[string]interface{}{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]interface{}{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    configDigest,
			"size":      len(configBlob),
		},
		"layers": []map[string]interface{}{
			{
				"mediaType": "application/vnd.oci.image.layer.v1.tar",
				"digest":    layerDigest,
				"size":      len(layerBlob),
			},
		},
	}
	childBytes, _ := json.Marshal(childManifest)
	childDigest := digestOf(childBytes)

	rootIndex := map[string]interface{}{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]interface{}{
			{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    childDigest,
				"size":      len(childBytes),
			},
		},
	}
	rootBytes, _ := json.Marshal(rootIndex)
	rootDigest := digestOf(rootBytes)

	var serverURL string
	tokenValue := "test-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = io.WriteString(w, `{"token":"`+tokenValue+`"}`)
			return
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			if got := r.Header.Get("Authorization"); got != "Bearer "+tokenValue {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+serverURL+`/token",service="registry.test",scope="repository:library/alpine:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}

		switch r.URL.Path {
		case "/v2/library/alpine/manifests/latest":
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", rootDigest)
			_, _ = w.Write(rootBytes)
		case "/v2/library/alpine/manifests/" + childDigest:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", childDigest)
			_, _ = w.Write(childBytes)
		case "/v2/library/alpine/blobs/" + configDigest:
			_, _ = w.Write(configBlob)
		case "/v2/library/alpine/blobs/" + layerDigest:
			_, _ = w.Write(layerBlob)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	u, _ := url.Parse(server.URL)
	imageRef := "docker://" + u.Host + "/library/alpine:latest"

	p := NewProvider(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.http = server.Client()
	if err := p.Configure(provider.ProviderConfig{
		"output_dir": "mirror",
		"images": []interface{}{
			imageRef,
		},
	}); err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	plan, err := p.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Actions) != 4 {
		t.Fatalf("expected 4 actions (2 manifests + 2 blobs), got %d", len(plan.Actions))
	}

	ref, _ := parseImageReference(imageRef)
	imageID := imagePathID(ref)
	manifestPath := filepath.ToSlash(filepath.Join("mirror", imageID, "manifests", "sha256", strings.TrimPrefix(rootDigest, "sha256:")+".json"))
	foundManifest := false
	for _, action := range plan.Actions {
		if action.Action != provider.ActionDownload {
			t.Fatalf("expected action download for new files, got %s", action.Action)
		}
		if action.Headers["Authorization"] != "Bearer "+tokenValue {
			t.Fatalf("expected authorization header, got %q", action.Headers["Authorization"])
		}
		if strings.Contains(action.Path, "/manifests/") && action.Headers["Accept"] == "" {
			t.Fatal("expected Accept header for manifest downloads")
		}
		if action.Path == manifestPath {
			foundManifest = true
		}
	}
	if !foundManifest {
		t.Fatalf("expected root manifest path %q in actions", manifestPath)
	}
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
