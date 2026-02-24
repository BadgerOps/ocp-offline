package epel

import (
	"io"
	"log/slog"
	"testing"

	"github.com/BadgerOps/airgap/internal/config"
)

func TestBuildPackageActionRejectsTraversalPath(t *testing.T) {
	p := NewEPELProvider(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := p.buildPackageAction(
		config.EPELRepoConfig{BaseURL: "https://example.com/epel"},
		t.TempDir(),
		PackageInfo{Location: "../../evil.rpm", Checksum: "abc", Size: 1},
		false,
	)
	if err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}
