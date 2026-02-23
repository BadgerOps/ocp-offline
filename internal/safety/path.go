package safety

import (
	"fmt"
	"path/filepath"
	"strings"
)

// CleanRelativePath validates and normalizes a relative path.
// It rejects absolute paths and parent traversal segments.
func CleanRelativePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}

	clean := filepath.Clean(filepath.FromSlash(p))
	if clean == "." {
		return "", fmt.Errorf("path resolves to current directory")
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", p)
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("parent traversal is not allowed: %q", p)
	}
	return clean, nil
}

// SafeJoinUnder joins a validated relative path under root and verifies
// the final path remains inside root.
func SafeJoinUnder(root, rel string) (string, error) {
	cleanRel, err := CleanRelativePath(rel)
	if err != nil {
		return "", err
	}
	return EnsureUnderRoot(root, filepath.Join(root, cleanRel))
}

// EnsureUnderRoot verifies candidate resolves under root and returns
// an absolute normalized path.
func EnsureUnderRoot(root, candidate string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	candAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve candidate: %w", err)
	}

	rel, err := filepath.Rel(rootAbs, candAbs)
	if err != nil {
		return "", fmt.Errorf("compare paths: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes root: %q", candidate)
	}
	return candAbs, nil
}
