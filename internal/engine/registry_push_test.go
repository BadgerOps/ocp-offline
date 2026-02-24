package engine

import (
	"strings"
	"testing"

	"github.com/BadgerOps/airgap/internal/provider/containerimages"
)

func TestBuildDestinationRepository(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		prefix   string
		expected string
	}{
		{name: "no prefix", source: "openshift/release", prefix: "", expected: "openshift/release"},
		{name: "prefix applied", source: "openshift/release", prefix: "mirror", expected: "mirror/openshift/release"},
		{name: "trim slashes", source: "/openshift/release/", prefix: "/mirror/", expected: "mirror/openshift/release"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDestinationRepository(tt.source, tt.prefix)
			if got != tt.expected {
				t.Fatalf("buildDestinationRepository(%q, %q) = %q, want %q", tt.source, tt.prefix, got, tt.expected)
			}
		})
	}
}

func TestNormalizeRegistryEndpoint(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "quay.example.com:8443", want: "quay.example.com:8443"},
		{in: "https://quay.example.com:8443/", want: "quay.example.com:8443"},
		{in: "http://registry.local/", want: "registry.local"},
	}
	for _, tt := range tests {
		got := normalizeRegistryEndpoint(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeRegistryEndpoint(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDigestToTagAlias(t *testing.T) {
	got := digestToTagAlias("sha256:AA/bb")
	if !strings.HasPrefix(got, "digest-") {
		t.Fatalf("expected digest alias prefix, got %q", got)
	}
	if strings.Contains(got, ":") || strings.Contains(got, "/") {
		t.Fatalf("expected sanitized digest alias, got %q", got)
	}
}

func TestChooseRootDigest(t *testing.T) {
	indexDigest := "sha256:index"
	childDigest := "sha256:child"

	manifests := map[string]*localManifest{
		indexDigest: {
			Digest:         indexDigest,
			MediaType:      "application/vnd.oci.image.index.v1+json",
			ChildManifests: []string{childDigest},
		},
		childDigest: {
			Digest:    childDigest,
			MediaType: "application/vnd.oci.image.manifest.v1+json",
		},
	}

	ref := containerimages.ImageReference{
		Reference: indexDigest,
		IsDigest:  true,
	}
	got, err := chooseRootDigest(ref, manifests)
	if err != nil {
		t.Fatalf("chooseRootDigest returned error: %v", err)
	}
	if got != indexDigest {
		t.Fatalf("chooseRootDigest = %q, want %q", got, indexDigest)
	}
}

func TestChooseRootDigestMultipleCandidatesError(t *testing.T) {
	manifests := map[string]*localManifest{
		"sha256:a": {Digest: "sha256:a", MediaType: "application/vnd.oci.image.manifest.v1+json"},
		"sha256:b": {Digest: "sha256:b", MediaType: "application/vnd.oci.image.manifest.v1+json"},
	}

	_, err := chooseRootDigest(containerimages.ImageReference{}, manifests)
	if err == nil {
		t.Fatal("expected error for ambiguous root candidates")
	}
}
