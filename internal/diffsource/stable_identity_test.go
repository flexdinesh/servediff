package diffsource

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func stableTestRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "original")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, root, "init", "-qb", "main")
	testGit(t, root, "config", "user.email", "diffx@example.com")
	testGit(t, root, "config", "user.name", "diffx")
	testGit(t, root, "commit", "--allow-empty", "-qm", "initial")
	return root
}

func mustStableIdentity(t *testing.T, root, branch string) StableRepositoryIdentity {
	t.Helper()
	identity, err := StableIdentity(t.Context(), root, "installation", branch)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestStableIdentityAdoptsLegacyAndSurvivesMovedCheckout(t *testing.T) {
	root := stableTestRepository(t)
	facts, err := Metadata(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	first := mustStableIdentity(t, root, "main")
	if first.RepositoryKey != identityHash("repository", "installation", facts.CommonDir) || first.CheckoutKey != identityHash("checkout", "installation", facts.GitDir) {
		t.Fatalf("did not preserve legacy keys: %#v", first)
	}
	if _, err := uuid.Parse(first.BranchID); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"repository.json", "checkout.json", "identity.lock"} {
		info, err := os.Stat(filepath.Join(facts.GitDir, "diffx", file))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private %s: %v, %v", file, info, err)
		}
	}
	moved := filepath.Join(filepath.Dir(root), "renamed")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	testGit(t, moved, "remote", "add", "origin", "https://example.com/other/name.git")
	if next := mustStableIdentity(t, moved, "main"); next != first {
		t.Fatalf("move/remote change altered provenance: %#v -> %#v", first, next)
	}
	other, err := StableIdentity(t.Context(), moved, "another-installation", "main")
	if err != nil || other.CheckoutKey == first.CheckoutKey {
		t.Fatalf("different installation merged checkout: %#v, %v", other, err)
	}
}

func TestStableIdentityBranchRenameCopyAndNameReuse(t *testing.T) {
	root := stableTestRepository(t)
	first := mustStableIdentity(t, root, "main")
	testGit(t, root, "branch", "-m", "main", "renamed")
	if renamed := mustStableIdentity(t, root, "renamed"); renamed != first {
		t.Fatalf("branch rename altered identity: %#v -> %#v", first, renamed)
	}
	testGit(t, root, "branch", "-c", "renamed", "copy")
	copy := mustStableIdentity(t, root, "copy")
	if copy.BranchID == first.BranchID || mustStableIdentity(t, root, "renamed").BranchID != first.BranchID {
		t.Fatalf("branch copy failed to preserve original and separate copy: %#v", copy)
	}
	testGit(t, root, "branch", "main")
	reused := mustStableIdentity(t, root, "main")
	if reused.BranchID == first.BranchID || reused.BranchID == copy.BranchID {
		t.Fatalf("reused branch name inherited old identity: %#v", reused)
	}
	if BranchCheckoutKey(first.RepositoryKey, first.BranchID) != BranchCheckoutKey(first.RepositoryKey, mustStableIdentity(t, root, "renamed").BranchID) || BranchCheckoutKey(first.RepositoryKey, copy.BranchID) == BranchCheckoutKey(first.RepositoryKey, first.BranchID) {
		t.Fatal("object-only checkout key failed stable branch identity")
	}
}

func TestStableIdentityLinkedWorktreeMoveAndConcurrentAdoption(t *testing.T) {
	root := stableTestRepository(t)
	main := mustStableIdentity(t, root, "main")
	linked := filepath.Join(t.TempDir(), "linked")
	testGit(t, root, "worktree", "add", "-b", "feature", linked)
	identities := make(chan StableRepositoryIdentity, 8)
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			identity, err := StableIdentity(t.Context(), linked, "installation", "feature")
			identities <- identity
			errors <- err
		})
	}
	workers.Wait()
	close(identities)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := <-identities
	for identity := range identities {
		if identity != first {
			t.Fatalf("concurrent adoption split identity: %#v, %#v", first, identity)
		}
	}
	if first.RepositoryKey != main.RepositoryKey || first.CheckoutKey == main.CheckoutKey {
		t.Fatalf("linked checkout identity: %#v, %#v", first, main)
	}
	moved := linked + "-moved"
	testGit(t, root, "worktree", "move", linked, moved)
	if next := mustStableIdentity(t, moved, "feature"); next != first {
		t.Fatalf("worktree move altered provenance: %#v, %#v", first, next)
	}
}

func TestStableIdentityCorruptionAndAmbiguousCopyFailVisibly(t *testing.T) {
	root := stableTestRepository(t)
	id := uuid.NewString()
	testGit(t, root, "branch", "copy")
	testGit(t, root, "config", "branch.main.diffx-id", id)
	testGit(t, root, "config", "branch.copy.diffx-id", id)
	if _, err := StableIdentity(t.Context(), root, "installation", "main"); err == nil || !strings.Contains(err.Error(), "ambiguous copied") {
		t.Fatalf("ambiguous identity should fail: %v", err)
	}
	testGit(t, root, "config", "--unset", "branch.copy.diffx-id")
	mustStableIdentity(t, root, "main")
	path := filepath.Join(root, ".git", "diffx", "repository.json")
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StableIdentity(t.Context(), root, "installation", "main"); err == nil || !strings.Contains(err.Error(), "invalid collector identity") {
		t.Fatalf("corrupted identity should fail: %v", err)
	}
}

func TestStableIdentityDetachedHead(t *testing.T) {
	root := stableTestRepository(t)
	testGit(t, root, "checkout", "--detach")
	identity := mustStableIdentity(t, root, "detached at HEAD")
	if identity.BranchID != "" {
		t.Fatalf("detached HEAD acquired branch identity: %#v", identity)
	}
}

func TestStableIdentityRenameThenCopyCannotSwapOriginal(t *testing.T) {
	root := stableTestRepository(t)
	original := mustStableIdentity(t, root, "main")
	testGit(t, root, "branch", "-m", "main", "renamed")
	testGit(t, root, "branch", "-c", "renamed", "main")
	for _, branch := range []string{"main", "renamed"} {
		identity, err := StableIdentity(t.Context(), root, "installation", branch)
		if err != nil && strings.Contains(err.Error(), "ambiguous copied") {
			continue
		}
		if err != nil || branch == "main" && identity.BranchID == original.BranchID || branch == "renamed" && identity.BranchID != original.BranchID {
			t.Fatalf("rename/copy swapped identity for %s: %#v, %v", branch, identity, err)
		}
	}
}
