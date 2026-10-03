package contextservice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func catalogGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s, %v", args, output, err)
	}
}

func TestCatalogDiscoversAllWorktreesAndPreservesReviewBindings(t *testing.T) {
	service := testService(t)
	ctx := context.Background()
	root := testRepo(t)
	catalogGit(t, root, "symbolic-ref", "HEAD", "refs/heads/main")
	catalogGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	catalogGit(t, root, "remote", "add", "origin", "https://github.com/owner/servediff.git")
	linked := filepath.Join(t.TempDir(), "elsewhere")
	catalogGit(t, root, "worktree", "add", "-qb", "feature/picker", linked)
	submission, err := service.Register(ctx, "linked", linked)
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.List(ctx, 100, "")
	if err != nil || len(page.Contexts) != 2 {
		t.Fatalf("catalog: %#v, %v", page, err)
	}
	if page.Contexts[0].ID != submission.Context.ID {
		t.Fatal("discovered worktree replaced the submitted context")
	}
	ids := make(map[string]string)
	for _, item := range page.Contexts {
		if item.Root == nil || item.Name != "servediff" || item.RepositoryID == nil ||
			*item.RepositoryID != *submission.Context.RepositoryID || item.Branch == nil {
			t.Fatalf("repo metadata: %#v", item)
		}
		ids[*item.Root] = item.ID
		if *item.Root == linked && (item.WorktreeName == nil || *item.WorktreeName != "elsewhere" || *item.Branch != "feature/picker") {
			t.Fatalf("linked metadata: %#v", item)
		}
		if *item.Root == root && (item.WorktreeName != nil || *item.Branch != "main") {
			t.Fatalf("main metadata: %#v", item)
		}
	}
	newRoot := filepath.Join(t.TempDir(), "new-worktree")
	catalogGit(t, root, "worktree", "add", "-qb", "feature/new", newRoot)
	moved := filepath.Join(t.TempDir(), "moved-worktree")
	catalogGit(t, root, "worktree", "move", linked, moved)
	if err := service.refreshCatalog(ctx, true); err != nil {
		t.Fatal(err)
	}
	updated, err := service.List(ctx, 100, "")
	if err != nil || len(updated.Contexts) != 3 {
		t.Fatalf("new worktree: %#v, %v", updated, err)
	}
	for _, item := range updated.Contexts {
		if item.Root == nil {
			t.Fatalf("missing root: %#v", item)
		}
		if *item.Root == moved && item.ID != ids[linked] || *item.Root == root && item.ID != ids[root] {
			t.Fatalf("rediscovery changed identity: %#v", item)
		}
		if *item.Root == root {
			resolved, err := service.Resolve(ctx, item.ID)
			if err != nil || resolved.ContextID != item.ID {
				t.Fatalf("discovered context not selectable: %#v, %v", resolved, err)
			}
		}
	}
	if err := service.refreshCatalog(ctx, true); err != nil {
		t.Fatal(err)
	}
	again, err := service.List(ctx, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range updated.Contexts {
		if item.ID != again.Contexts[index].ID || item.LastSubmittedAt != again.Contexts[index].LastSubmittedAt {
			t.Fatal("rediscovery changed catalog order or submission time")
		}
	}
	restarted := New(service.store, service.user)
	t.Cleanup(func() { _ = restarted.Close() })
	page, err = restarted.List(ctx, 100, "")
	if err != nil || len(page.Contexts) != 3 || page.Contexts[0].Name != "servediff" {
		t.Fatalf("restart metadata: %#v, %v", page, err)
	}
}

func TestCatalogTracksWorktreeChangesWithoutChangingSubmissionOrder(t *testing.T) {
	service := testService(t)
	ctx := t.Context()
	root := testRepo(t)
	committed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	t.Setenv("GIT_AUTHOR_DATE", committed.Format(time.RFC3339))
	t.Setenv("GIT_COMMITTER_DATE", committed.Format(time.RFC3339))
	catalogGit(t, root, "add", ".")
	catalogGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	catalogGit(t, root, "worktree", "add", "-qb", "feature", linked)
	submission, err := service.Register(ctx, "main", root)
	if err != nil {
		t.Fatal(err)
	}
	if submission.Context.LastChangedAt != committed.UnixMilli() {
		t.Fatalf("registration replaced commit time: %#v", submission.Context)
	}
	file := filepath.Join(linked, "file")
	if err := os.WriteFile(file, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := committed.Add(24 * time.Hour)
	if err := os.Chtimes(file, edited, edited); err != nil {
		t.Fatal(err)
	}
	if err := service.refreshCatalog(ctx, true); err != nil {
		t.Fatal(err)
	}
	page, err := service.List(ctx, 100, "")
	if err != nil || len(page.Contexts) != 2 {
		t.Fatalf("catalog: %#v, %v", page, err)
	}
	if page.Contexts[0].ID != submission.Context.ID || page.Contexts[0].LastSubmittedAt != submission.Context.LastSubmittedAt {
		t.Fatal("worktree edit changed submission order or time")
	}
	discovered := page.Contexts[1]
	if discovered.LastSubmittedAt != 0 || discovered.LastChangedAt != edited.UnixMilli() {
		t.Fatalf("discovered worktree change time: %#v", discovered)
	}
	restarted := New(service.store, service.user)
	t.Cleanup(func() { _ = restarted.Close() })
	item, err := restarted.Get(ctx, discovered.ID)
	if err != nil || item.LastChangedAt != edited.UnixMilli() {
		t.Fatalf("restored change time: %#v, %v", item, err)
	}
	capture, err := service.Capture(ctx, "capture", testPatch, "")
	if err != nil || capture.Context.LastChangedAt != capture.Context.LastSubmittedAt {
		t.Fatalf("capture change time: %#v, %v", capture, err)
	}
}

func TestCatalogChangeCountsRefreshWithoutLoadingDiffs(t *testing.T) {
	service := testService(t)
	ctx := t.Context()
	root := testRepo(t)
	submission, err := service.Register(ctx, "repo", root)
	if err != nil {
		t.Fatal(err)
	}
	assertCount := func(item Context, want int) {
		t.Helper()
		if item.ChangedFileCount == nil || *item.ChangedFileCount != want {
			t.Fatalf("changed file count: %#v; want %d", item, want)
		}
	}
	assertCount(submission.Context, 1)
	catalogGit(t, root, "add", ".")
	catalogGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "initial")
	if err := service.refreshCatalog(ctx, true); err != nil {
		t.Fatal(err)
	}
	item, err := service.Get(ctx, submission.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCount(item, 0)
	captured, err := service.Capture(ctx, "capture", testPatch, "")
	if err != nil {
		t.Fatal(err)
	}
	assertCount(captured.Context, 1)
	restarted := New(service.store, service.user)
	t.Cleanup(func() { _ = restarted.Close() })
	page, err := restarted.List(ctx, 100, "")
	if err != nil || len(page.Contexts) != 2 {
		t.Fatalf("restarted catalog: %#v, %v", page, err)
	}
	for _, item := range page.Contexts {
		if item.Kind == "capture" {
			assertCount(item, 1)
		} else {
			assertCount(item, 0)
		}
	}
	if len(restarted.sources) != 0 {
		t.Fatal("catalog loaded diff sources")
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.refreshCatalog(ctx, true); err != nil {
		t.Fatal(err)
	}
	item, err = restarted.Get(ctx, submission.Context.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCount(item, 1)
	moved := filepath.Join(t.TempDir(), "missing-repo")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := restarted.refreshCatalog(ctx, true); err != nil {
		t.Fatal(err)
	}
	item, err = restarted.Get(ctx, submission.Context.ID)
	if err != nil || item.ChangedFileCount != nil {
		t.Fatalf("missing checkout retained a known change count: %#v, %v", item, err)
	}
}
