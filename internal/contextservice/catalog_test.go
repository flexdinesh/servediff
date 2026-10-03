package contextservice

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
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
