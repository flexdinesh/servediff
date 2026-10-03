package diffsource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setChangeTime(t *testing.T, path string, timestamp time.Time) {
	t.Helper()
	if err := os.Chtimes(path, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeLastChangedAt(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	committed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Setenv("GIT_AUTHOR_DATE", committed.Format(time.RFC3339))
	t.Setenv("GIT_COMMITTER_DATE", committed.Format(time.RFC3339))
	file := filepath.Join(root, "value\nwith spaces.txt")
	for path, contents := range map[string]string{file: "base\n", filepath.Join(root, ".gitignore"): "ignored\n"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	_, gitDir, err := RepositoryIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	assertLatest := func(want time.Time) {
		t.Helper()
		got, err := WorktreeLastChangedAt(t.Context(), root, gitDir)
		if err != nil || got != want.UnixMilli() {
			t.Fatalf("last change: %d, %v; want %d", got, err, want.UnixMilli())
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ignored"), []byte("ignored change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertLatest(committed) // Checkout and ignored-file times do not count.
	if err := os.WriteFile(file, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := committed.Add(24 * time.Hour)
	setChangeTime(t, file, edited)
	assertLatest(edited)
	testGit(t, root, "add", ".")
	staged := edited.Add(24 * time.Hour)
	setChangeTime(t, filepath.Join(gitDir, "index"), staged)
	assertLatest(staged)
	assertLatest(staged) // Catalog reads must not refresh the index timestamp.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	deleted := staged.Add(24 * time.Hour)
	setChangeTime(t, root, deleted)
	assertLatest(deleted)
}

func TestWorktreeLastChangedAtUnbornAndCancelled(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	_, gitDir, err := RepositoryIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := WorktreeLastChangedAt(t.Context(), root, gitDir); err != nil || got != 0 {
		t.Fatalf("empty repository: %d, %v", got, err)
	}
	file := filepath.Join(root, "untracked.txt")
	if err := os.WriteFile(file, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	setChangeTime(t, file, edited)
	if got, err := WorktreeLastChangedAt(t.Context(), root, gitDir); err != nil || got != edited.UnixMilli() {
		t.Fatalf("untracked change: %d, %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := WorktreeLastChangedAt(ctx, root, gitDir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled catalog: %v", err)
	}
}
