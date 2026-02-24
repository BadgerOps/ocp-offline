package safety

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSafeJoinUnder(t *testing.T) {
	root := t.TempDir()

	okPath, err := SafeJoinUnder(root, "a/b/c.txt")
	if err != nil {
		t.Fatalf("SafeJoinUnder returned error: %v", err)
	}
	if !strings.HasPrefix(okPath, root) {
		t.Fatalf("path %q is not under root %q", okPath, root)
	}

	if _, err := SafeJoinUnder(root, "../escape.txt"); err == nil {
		t.Fatal("expected traversal path to fail")
	}
	if _, err := SafeJoinUnder(root, "/abs/path.txt"); err == nil {
		t.Fatal("expected absolute path to fail")
	}
}

func TestEnsureUnderRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureUnderRoot(root, root+"/child/file.txt"); err != nil {
		t.Fatalf("EnsureUnderRoot failed for child path: %v", err)
	}
	if _, err := EnsureUnderRoot(root, root+"/../escape"); err == nil {
		t.Fatal("expected escape path to fail")
	}
}

func TestReadAllWithLimit(t *testing.T) {
	_, err := ReadAllWithLimit(strings.NewReader("abc"), 2)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("expected ErrBodyTooLarge, got %v", err)
	}

	data, err := ReadAllWithLimit(io.NopCloser(strings.NewReader("abc")), 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("unexpected data: %q", string(data))
	}
}
