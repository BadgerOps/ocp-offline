package engine

import (
	"context"
	"fmt"
	"os/exec"
)

// runCreaterepoC runs createrepo_c --update on the given directory.
// It warns and continues if createrepo_c is not installed or fails.
func (m *SyncManager) runCreaterepoC(ctx context.Context, dir string) error {
	path, err := exec.LookPath("createrepo_c")
	if err != nil {
		m.logger.Warn("createrepo_c not found, skipping repodata rebuild", "dir", dir)
		return nil
	}

	cmd := exec.CommandContext(ctx, path, "--update", dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Warn("createrepo_c failed, skipping repodata rebuild",
			"dir", dir, "error", err, "output", string(output))
		return fmt.Errorf("createrepo_c failed on %s: %w", dir, err)
	}

	m.logger.Info("createrepo_c completed", "dir", dir)
	return nil
}
