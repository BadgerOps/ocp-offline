package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/download"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/store"
)

func newCreaterepoTestManager(t *testing.T) *SyncManager {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.New(dbPath, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("failed to close store: %v", err)
		}
	})

	cfg := &config.Config{
		Server: config.ServerConfig{DataDir: t.TempDir()},
	}
	registry := provider.NewRegistry()
	client := download.NewClient(logger)
	return NewSyncManager(registry, st, client, cfg, logger)
}

func TestRunCreaterepoCNotInstalled(t *testing.T) {
	mgr := newCreaterepoTestManager(t)

	// Use empty PATH so createrepo_c won't be found
	t.Setenv("PATH", "")

	// Should return nil (warn and continue)
	err := mgr.runCreaterepoC(context.Background(), t.TempDir())
	if err != nil {
		t.Errorf("expected nil error when createrepo_c not found, got: %v", err)
	}
}

func TestRunCreaterepoCWithFakeDir(t *testing.T) {
	// If createrepo_c is actually installed, test it on a fake dir
	_, err := os.Stat("/usr/bin/createrepo_c")
	if err != nil {
		t.Skip("createrepo_c not installed, skipping")
	}

	mgr := newCreaterepoTestManager(t)
	dir := t.TempDir()

	// createrepo_c on an empty dir should succeed (creates repodata)
	err = mgr.runCreaterepoC(context.Background(), dir)
	if err != nil {
		t.Errorf("createrepo_c on empty dir failed: %v", err)
	}

	// Check repodata was created
	repodata := filepath.Join(dir, "repodata")
	if _, statErr := os.Stat(repodata); os.IsNotExist(statErr) {
		t.Error("expected repodata directory to be created")
	}
}
