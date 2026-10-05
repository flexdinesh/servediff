package diffsource

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryWorktreesUsesRemoteNameAndExternalPaths(t *testing.T) {
	root := t.TempDir()
	testGit(t, root, "init", "-q", "-b", "main")
	testGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial")
	testGit(t, root, "remote", "add", "origin", "git@github.com:owner/servediff.git")
	linked := filepath.Join(t.TempDir(), "work tree")
	testGit(t, root, "worktree", "add", "-qb", "feature/picker", linked)
	detached := filepath.Join(t.TempDir(), "detached")
	testGit(t, root, "worktree", "add", "-q", "--detach", detached)
	name, worktrees, err := RepositoryWorktrees(context.Background(), linked)
	if err != nil || name != "servediff" || len(worktrees) != 3 {
		t.Fatalf("worktrees: %q %#v, %v", name, worktrees, err)
	}
	for _, item := range worktrees {
		switch item.Root {
		case root:
			if item.Linked || item.Branch != "main" {
				t.Fatalf("main checkout: %#v", item)
			}
		case linked:
			if !item.Linked || item.Branch != "feature/picker" {
				t.Fatalf("linked checkout: %#v", item)
			}
		case detached:
			if !item.Linked || len(item.Branch) != len("Detached · ")+8 {
				t.Fatalf("detached checkout: %#v", item)
			}
		default:
			t.Fatalf("unexpected checkout: %#v", item)
		}
	}
	testGit(t, root, "remote", "remove", "origin")
	name, _, err = RepositoryWorktrees(context.Background(), linked)
	if err != nil || name != filepath.Base(root) {
		t.Fatalf("no-remote fallback: %q, %v", name, err)
	}
}

func TestParseWorktreesPreservesNewlinesAndSkipsBareRepository(t *testing.T) {
	items := parseWorktrees("worktree /repo.git\x00bare\x00\x00worktree /external/a\nfolder\x00HEAD abcdef123456\x00branch refs/heads/feature\x00\x00")
	if len(items) != 1 || items[0].Root != "/external/a\nfolder" || !items[0].Linked || items[0].Branch != "feature" {
		t.Fatalf("worktrees: %#v", items)
	}
}

func TestRemoteRepositoryName(t *testing.T) {
	for _, remote := range []string{"git@host:owner/repo.git", "host:owner/repo.git", "https://host/owner/repo.git", "ssh://git@host:2222/owner/repo.git", "https://host/owner/repo/", "file:///tmp/repo.git", filepath.ToSlash(filepath.Join(os.TempDir(), "repo.git"))} {
		if got := RemoteRepositoryName(remote, "fallback"); got != "repo" {
			t.Errorf("%q: %q", remote, got)
		}
	}
	for _, remote := range []string{"", "https://host/", "."} {
		if got := RemoteRepositoryName(remote, "fallback"); got != "fallback" {
			t.Errorf("%q: %q", remote, got)
		}
	}
}
