package collector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryFindsWorkspaceAndRemovedWorktreeBranch(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "project")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "--initial-branch=main")
	git(t, root, "config", "user.name", "Test")
	git(t, root, "config", "user.email", "test@example.com")
	write(t, root, "file.txt", "before\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	linked := filepath.Join(workspace, "temporary")
	git(t, root, "worktree", "add", "-b", "feature", linked)
	write(t, linked, "file.txt", "after\n")
	git(t, linked, "commit", "-am", "change")
	git(t, root, "worktree", "remove", linked)
	result, err := Discover(context.Background(), workspace, "machine", "auto")
	if err != nil || len(result.Sources) != 2 {
		t.Fatalf("sources %#v diagnostics %v err %v", result.Sources, result.Diagnostics, err)
	}
	if result.Sources[0].Path != root || result.Sources[0].Base != "auto" || result.Sources[1].Branch != "feature" || result.Sources[1].Identity == result.Sources[0].Identity {
		t.Fatalf("unexpected discovery: %#v", result.Sources)
	}
	identity := result.Sources[1].Identity
	git(t, root, "branch", "-m", "feature", "renamed")
	moved := filepath.Join(workspace, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	result, err = Discover(context.Background(), moved, "machine", "auto")
	if err != nil || len(result.Sources) != 2 || result.Sources[1].Identity != identity {
		t.Fatalf("renamed source lost identity: %#v %v %v", result.Sources, result.Diagnostics, err)
	}
	liveIdentity := result.Sources[0].Identity
	git(t, moved, "branch", "-m", "main", "master")
	result, err = Discover(context.Background(), moved, "machine", "auto")
	if err != nil || len(result.Sources) != 2 || result.Sources[0].Identity != liveIdentity || result.Sources[1].Identity != identity {
		t.Fatalf("default branch rename changed source identity: %#v %v %v", result.Sources, result.Diagnostics, err)
	}
}

func TestDiscoveryIncludesIndependentClonesWithSameRemote(t *testing.T) {
	original := repository(t)
	write(t, original, "file.txt", "before\n")
	git(t, original, "add", ".")
	git(t, original, "commit", "-m", "initial")
	workspace := t.TempDir()
	first, second := filepath.Join(workspace, "first"), filepath.Join(workspace, "second")
	git(t, workspace, "clone", original, first)
	git(t, workspace, "clone", original, second)
	result, err := Discover(context.Background(), workspace, "machine", "auto")
	if err != nil || len(result.Sources) != 2 {
		t.Fatalf("independent clones skipped: %#v %v %v", result.Sources, result.Diagnostics, err)
	}
	paths := make(map[string]bool)
	for _, source := range result.Sources {
		paths[source.Path] = true
	}
	if !paths[first] || !paths[second] || result.Sources[0].Identity == result.Sources[1].Identity {
		t.Fatalf("clones lost distinct checkout identities: %#v", result.Sources)
	}
}

func TestDiscoveryReportsCopiedGitIdentity(t *testing.T) {
	workspace := t.TempDir()
	original := repository(t)
	write(t, original, "file.txt", "before\n")
	git(t, original, "add", ".")
	git(t, original, "commit", "-m", "initial")
	if _, err := SourceIdentity(t.Context(), original, "machine", "", "auto"); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(workspace, "first"), filepath.Join(workspace, "second")
	if err := os.CopyFS(first, os.DirFS(original)); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(second, os.DirFS(original)); err != nil {
		t.Fatal(err)
	}
	result, err := Discover(t.Context(), workspace, "machine", "auto")
	if err != nil || len(result.Sources) != 1 {
		t.Fatalf("copied identity discovery: %#v %v %v", result.Sources, result.Diagnostics, err)
	}
	for _, diagnostic := range result.Diagnostics {
		if strings.Contains(diagnostic, "identity copied") && strings.Contains(diagnostic, first) && strings.Contains(diagnostic, second) {
			return
		}
	}
	t.Fatalf("copied identities silently merged: %v", result.Diagnostics)
}

func TestDiscoveryLiveWorktreesSuppressObjectDuplicate(t *testing.T) {
	root := repository(t)
	write(t, root, "file.txt", "before\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "feature", linked)
	write(t, linked, "file.txt", "after\n")
	git(t, linked, "commit", "-am", "change")
	result, err := Discover(context.Background(), root, "machine", "auto")
	if err != nil || len(result.Sources) != 2 {
		t.Fatalf("unexpected sources: %#v %v %v", result.Sources, result.Diagnostics, err)
	}
	for _, source := range result.Sources {
		if source.Branch != "" {
			t.Fatalf("live branch duplicated as recovery: %#v", source)
		}
	}
}

func TestDiscoveryBoundsDepthAndReportsMissingInput(t *testing.T) {
	workspace := t.TempDir()
	deep := filepath.Join(workspace, "one", "two", "three")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, deep, "init", "--initial-branch=main")
	result, err := Discover(context.Background(), workspace, "machine", "auto")
	if err != nil || len(result.Sources) != 0 || len(result.Diagnostics) == 0 {
		t.Fatalf("deep checkout escaped discovery bound: %#v %v", result, err)
	}
	result, err = Discover(context.Background(), filepath.Join(workspace, "missing"), "machine", "auto")
	if err != nil || len(result.Sources) != 0 || len(result.Diagnostics) == 0 {
		t.Fatalf("missing path lacks diagnostics: %#v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, workspace, "machine", "auto"); err == nil {
		t.Fatal("canceled discovery succeeded")
	}
}

func TestDiscoveryBoundsTotalSourcesAcrossLiveAndRecoveredBranches(t *testing.T) {
	root := repository(t)
	write(t, root, "file.txt", "before\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "initial")
	git(t, root, "switch", "-c", "temporary")
	write(t, root, "file.txt", "after\n")
	git(t, root, "commit", "-am", "change")
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "switch", "main")
	git(t, root, "branch", "-D", "temporary")
	var refs strings.Builder
	for index := 0; index < discoveryLimit; index++ {
		fmt.Fprintf(&refs, "create refs/heads/feature-%03d %s\n", index, head)
	}
	command := exec.Command("git", "update-ref", "--stdin")
	command.Dir, command.Stdin = root, strings.NewReader(refs.String())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create branch refs: %s %v", output, err)
	}
	result, err := Discover(context.Background(), root, "machine", "auto")
	if err != nil || len(result.Sources) != discoveryLimit {
		t.Fatalf("global discovery bound: %d %v %v", len(result.Sources), result.Diagnostics, err)
	}
	limits := 0
	for _, diagnostic := range result.Diagnostics {
		if strings.Contains(diagnostic, "global 128-source limit") {
			limits++
		}
	}
	if limits != 1 {
		t.Fatalf("source limit lacks single diagnostic: %v", result.Diagnostics)
	}
}
