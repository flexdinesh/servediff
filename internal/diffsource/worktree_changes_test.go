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

func TestWorktreeChanges(t *testing.T) {
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
	assertChanges := func(want time.Time, count int) {
		t.Helper()
		got, err := WorktreeChanges(t.Context(), root, gitDir)
		if err != nil || got.LastChangedAt != want.UnixMilli() || got.ChangedFileCount != count {
			t.Fatalf("changes: %#v, %v; want time %d, count %d", got, err, want.UnixMilli(), count)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ignored"), []byte("ignored change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertChanges(committed, 0) // Checkout and ignored-file times do not count.
	if err := os.WriteFile(file, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := committed.Add(24 * time.Hour)
	setChangeTime(t, file, edited)
	assertChanges(edited, 1)
	testGit(t, root, "add", ".")
	staged := edited.Add(24 * time.Hour)
	setChangeTime(t, filepath.Join(gitDir, "index"), staged)
	assertChanges(staged, 1)
	assertChanges(staged, 1) // Catalog reads must not refresh the index timestamp.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	deleted := staged.Add(24 * time.Hour)
	setChangeTime(t, root, deleted)
	assertChanges(deleted, 1)
}

func TestWorktreeChangesUnbornAndCancelled(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	_, gitDir, err := RepositoryIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := WorktreeChanges(t.Context(), root, gitDir); err != nil || got.LastChangedAt != 0 || got.ChangedFileCount != 0 {
		t.Fatalf("empty repository: %#v, %v", got, err)
	}
	file := filepath.Join(root, "untracked.txt")
	if err := os.WriteFile(file, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	setChangeTime(t, file, edited)
	if got, err := WorktreeChanges(t.Context(), root, gitDir); err != nil || got.LastChangedAt != edited.UnixMilli() || got.ChangedFileCount != 1 {
		t.Fatalf("untracked change: %#v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := WorktreeChanges(ctx, root, gitDir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled catalog: %v", err)
	}
}

func TestWorktreeChangesCountsUniqueFilesAcrossScopesAndRenames(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q")
	for name, contents := range map[string]string{
		"both.txt":           "original\n",
		"rename\nsource.txt": "renamed contents\n",
		"deleted.txt":        "deleted\n",
		"binary":             "\x00original",
		".gitignore":         "ignored\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, root, "add", ".")
	testGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	testGit(t, root, "mv", "rename\nsource.txt", "renamed file.txt")
	if err := os.WriteFile(filepath.Join(root, "both.txt"), []byte("staged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "add", "both.txt")
	// The unstaged edit cancels the staged edit in the All diff; it is still reviewable.
	for name, contents := range map[string]string{
		"both.txt":            "original\n",
		"binary":              "\x00changed",
		"untracked\nfile.txt": "new\n",
		"ignored":             "not counted\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	_, gitDir, err := RepositoryIdentity(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := WorktreeChanges(t.Context(), root, gitDir)
	if err != nil || changes.ChangedFileCount != 5 {
		t.Fatalf("unique changed files: %#v, %v; want 5", changes, err)
	}
}

func TestChangedFileCountDeduplicatesRecreatedPathsAndRenameRecords(t *testing.T) {
	status := "D  recreated.txt\x00?? recreated.txt\x00R  new\nname.txt\x00old\nname.txt\x00UU conflict.txt\x00"
	if got := changedFileCount(status); got != 3 {
		t.Fatalf("changed files: %d; want 3", got)
	}
}
