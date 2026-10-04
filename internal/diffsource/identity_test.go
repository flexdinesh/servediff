package diffsource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeIdentityFailsClosedForDirectoriesAndCancelsReads(t *testing.T) {
	root := t.TempDir()
	content, err := worktreeContent(t.Context(), root)
	if err != nil || content != nil {
		t.Fatalf("directory identity: %v, %v", content, err)
	}
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := worktreeContent(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled content read: %v", err)
	}
}
